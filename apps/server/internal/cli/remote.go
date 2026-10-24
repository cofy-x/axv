package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/spf13/cobra"
)

type remoteStatus struct {
	InstanceID  string `json:"instanceId,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Supported   *bool  `json:"supported,omitempty"`
	State       string `json:"state"`
	AuthURL     string `json:"authUrl,omitempty"`
	URL         string `json:"url,omitempty"`
	Error       string `json:"error,omitempty"`
	OwnerUserID string `json:"ownerUserId,omitempty"`
}

func newRemote() *cobra.Command {
	cmd := &cobra.Command{
		Use: "remote", Short: "Inspect private remote access to this workstation",
		Long: "Remote access is controlled from the local desktop and retained in the profile. Status never starts a Tailscale node or changes the profile.",
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
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
			}
			var output strings.Builder
			fmt.Fprintf(&output, "Remote access: %s\n", value.State)
			if value.Enabled != nil {
				fmt.Fprintf(&output, "Enabled: %t\n", *value.Enabled)
			}
			for _, entry := range []struct{ label, value string }{
				{"URL", value.URL}, {"Authorize", value.AuthURL}, {"Owner user ID", value.OwnerUserID}, {"Error", value.Error},
			} {
				if entry.value != "" {
					fmt.Fprintf(&output, "%s: %s\n", entry.label, entry.value)
				}
			}
			_, err = io.WriteString(cmd.OutOrStdout(), output.String())
			return err
		},
	}
	status.Flags().BoolVar(&asJSON, "json", false, "print machine-readable status")
	cmd.AddCommand(status)
	return cmd
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
