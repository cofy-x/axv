package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/spf13/cobra"
)

type remoteStatus struct {
	InstanceID  string `json:"instanceId,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Supported   *bool  `json:"supported,omitempty"`
	State       string `json:"state"`
	Hostname    string `json:"hostname,omitempty"`
	AuthURL     string `json:"authUrl,omitempty"`
	URL         string `json:"url,omitempty"`
	Error       string `json:"error,omitempty"`
	OwnerUserID string `json:"ownerUserId,omitempty"`
}

func newRemote() *cobra.Command {
	cmd := &cobra.Command{
		Use: "remote", Short: "Manage private Tailscale remote access to this workstation",
		Long: "Inspect, enable or disable the embedded Tailscale HTTPS transport for this AXV profile. Status never starts a Tailscale node or changes the profile.",
		Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	var asJSON bool
	status := &cobra.Command{
		Use: "status", Short: "Show remote access status and any pending authorization URL",
		Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			value, err := readRemoteStatus(cmd.Context(), home)
			if err != nil {
				return err
			}
			return writeRemoteStatus(cmd.OutOrStdout(), value, asJSON)
		},
	}
	status.Flags().BoolVar(&asJSON, "json", false, "print machine-readable status")
	cmd.AddCommand(status, newRemoteEnable(), newRemoteDisable())
	return cmd
}

func newRemoteEnable() *cobra.Command {
	var hostname, ownerUserID string
	var wait, asJSON bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable Tailscale HTTPS remote access for this workstation",
		Long:  "Persist remote.enabled=true in the profile and activate the embedded Tailscale node on the running desktop. Prints the Tailscale authorization URL or ready HTTPS URL.",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := nonemptyFlags(cmd, "hostname", "owner-user-id"); err != nil {
				return err
			}
			if timeout <= 0 {
				return usageError(fmt.Errorf("--timeout must be positive"))
			}
			candidate := profile.Remote{Hostname: hostname, OwnerUserID: ownerUserID}
			if err := profile.ValidateRemote(candidate); err != nil {
				return usageError(err)
			}
			home, err := profile.Home()
			if err != nil {
				return err
			}
			patch := profile.RemotePatch{}
			if cmd.Flags().Changed("hostname") {
				patch.Hostname = &hostname
			}
			if cmd.Flags().Changed("owner-user-id") {
				patch.OwnerUserID = &ownerUserID
			}
			value, err := enableRemote(cmd.Context(), home, patch, wait, timeout)
			if err != nil {
				return err
			}
			if err := writeRemoteStatus(cmd.OutOrStdout(), value, asJSON); err != nil {
				return err
			}
			if !asJSON && value.State == "stopped" {
				fmt.Fprintln(cmd.OutOrStdout(), "Start AXV with `axv daemon start` or `axv serve`, then run `axv remote status` to authorize this device.")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&hostname, "hostname", "", "Tailscale DNS label for this workstation (default: axv)")
	f.StringVar(&ownerUserID, "owner-user-id", "", "restrict first enrollment to this numeric Tailscale user ID")
	f.BoolVar(&wait, "wait", true, "wait briefly for an authorization URL or ready HTTPS URL when desktop is running")
	f.DurationVar(&timeout, "timeout", 15*time.Second, "maximum time to wait for Tailscale startup")
	f.BoolVar(&asJSON, "json", false, "print machine-readable status")
	return cmd
}

func newRemoteDisable() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable Tailscale HTTPS remote access without stopping local work",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			value, err := disableRemote(cmd.Context(), home)
			if err != nil {
				return err
			}
			return writeRemoteStatus(cmd.OutOrStdout(), value, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable status")
	return cmd
}

func enableRemote(ctx context.Context, home string, patch profile.RemotePatch, wait bool, timeout time.Duration) (remoteStatus, error) {
	if err := ctx.Err(); err != nil {
		return remoteStatus{}, err
	}
	updateIdentity := patch.Hostname != nil || patch.OwnerUserID != nil
	if updateIdentity {
		if _, err := profile.Initialize(home); err != nil {
			return remoteStatus{}, err
		}
		lock, err := profile.Lock(home)
		if err != nil {
			return remoteStatus{}, err
		}
		_, saveErr := profile.SaveRemote(home, patch)
		unlockErr := lock.Close()
		if saveErr != nil {
			return remoteStatus{}, saveErr
		}
		if unlockErr != nil {
			return remoteStatus{}, unlockErr
		}
	}
	// If identity settings changed on an already-enabled live desktop, bounce
	// disabled -> enabled so the embedded node restarts with the new settings.
	if updateIdentity {
		if current, err := readRemoteStatus(ctx, home); err == nil && current.Enabled != nil && *current.Enabled {
			if _, _, err := putLocalDesktop(ctx, home, "/api/remote", map[string]bool{"enabled": false}); err != nil {
				return remoteStatus{}, err
			}
		}
	}
	_, running, err := putLocalDesktop(ctx, home, "/api/remote", map[string]bool{"enabled": true})
	if err != nil {
		return remoteStatus{}, err
	}
	if !running {
		if _, err := profile.Initialize(home); err != nil {
			return remoteStatus{}, err
		}
		lock, err := profile.Lock(home)
		if err != nil {
			return remoteStatus{}, err
		}
		enabled := true
		patch.Enabled = &enabled
		saved, saveErr := profile.SaveRemote(home, patch)
		unlockErr := lock.Close()
		if saveErr != nil {
			return remoteStatus{}, saveErr
		}
		if unlockErr != nil {
			return remoteStatus{}, unlockErr
		}
		return remoteStatus{
			State:       "stopped",
			Enabled:     &enabled,
			Hostname:    saved.Hostname,
			OwnerUserID: saved.OwnerUserID,
		}, nil
	}
	if !wait {
		return readRemoteStatus(ctx, home)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		value, statusErr := readRemoteStatus(waitCtx, home)
		if statusErr != nil {
			if ctx.Err() != nil {
				return remoteStatus{}, ctx.Err()
			}
			return remoteStatus{}, statusErr
		}
		if value.State != "starting" && value.State != "disabled" {
			return value, nil
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return remoteStatus{}, ctx.Err()
			}
			return value, nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func disableRemote(ctx context.Context, home string) (remoteStatus, error) {
	if err := ctx.Err(); err != nil {
		return remoteStatus{}, err
	}
	_, running, err := putLocalDesktop(ctx, home, "/api/remote", map[string]bool{"enabled": false})
	if err != nil {
		return remoteStatus{}, err
	}
	if running {
		return readRemoteStatus(ctx, home)
	}
	disabled := false
	if _, statErr := os.Stat(filepath.Join(home, "config.json")); os.IsNotExist(statErr) {
		return remoteStatus{State: "stopped", Enabled: &disabled}, nil
	}
	lock, err := profile.Lock(home)
	if err != nil {
		return remoteStatus{}, err
	}
	_, saveErr := profile.SaveRemoteEnabled(home, false)
	unlockErr := lock.Close()
	if saveErr != nil {
		return remoteStatus{}, saveErr
	}
	if unlockErr != nil {
		return remoteStatus{}, unlockErr
	}
	return remoteStatus{State: "stopped", Enabled: &disabled}, nil
}

func writeRemoteStatus(out io.Writer, value remoteStatus, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(value)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Remote access: %s\n", value.State)
	if value.Enabled != nil {
		fmt.Fprintf(&output, "Enabled: %t\n", *value.Enabled)
	}
	for _, entry := range []struct{ label, value string }{
		{"Hostname", value.Hostname},
		{"URL", value.URL},
		{"Authorize", value.AuthURL},
		{"Owner user ID", value.OwnerUserID},
		{"Error", value.Error},
	} {
		if entry.value != "" {
			fmt.Fprintf(&output, "%s: %s\n", entry.label, entry.value)
		}
	}
	_, err := io.WriteString(out, output.String())
	return err
}

func readRemoteStatus(ctx context.Context, home string) (remoteStatus, error) {
	body, err := readDesktopStatus(ctx, home, "/api/remote/status")
	if err != nil {
		return remoteStatus{}, err
	}
	if body == nil {
		return remoteStatus{State: "stopped"}, nil
	}
	var value remoteStatus
	if err := json.Unmarshal(body, &value); err != nil {
		return remoteStatus{}, fmt.Errorf("invalid remote status: %w", err)
	}
	return value, nil
}
