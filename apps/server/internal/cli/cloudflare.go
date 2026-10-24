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

type cloudflareStatus struct {
	InstanceID string `json:"instanceId,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
	Configured *bool  `json:"configured,omitempty"`
	State      string `json:"state"`
	URL        string `json:"url,omitempty"`
	Listen     string `json:"listen,omitempty"`
	Error      string `json:"error,omitempty"`
}

func newCloudflare() *cobra.Command {
	cmd := &cobra.Command{
		Use: "cloudflare", Short: "Manage Cloudflare access and published project services",
		Long: "Configure desktop Access and publish reviewed VM project services through an existing Cloudflare Tunnel. Cloudflare settings belong to the selected AXV profile; the cloudflared connector remains externally managed. Read commands never start a service.",
		Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	var asJSON bool
	status := &cobra.Command{
		Use: "status", Short: "Show the local entrypoint status without changing services",
		Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			value, err := readCloudflareStatus(cmd.Context(), home)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
			}
			var output strings.Builder
			fmt.Fprintf(&output, "Cloudflare entrypoint: %s\n", value.State)
			if value.Enabled != nil {
				fmt.Fprintf(&output, "Enabled: %t\n", *value.Enabled)
			}
			if value.Configured != nil {
				fmt.Fprintf(&output, "Configured: %t\n", *value.Configured)
			}
			for _, entry := range []struct{ label, value string }{
				{"URL", value.URL}, {"Local listener", value.Listen}, {"Error", value.Error},
			} {
				if entry.value != "" {
					fmt.Fprintf(&output, "%s: %s\n", entry.label, entry.value)
				}
			}
			if value.State == "listening" {
				output.WriteString("The local entrypoint is listening. Public tunnel connectivity has not been verified.\n")
			}
			_, err = io.WriteString(cmd.OutOrStdout(), output.String())
			return err
		},
	}
	status.Flags().BoolVar(&asJSON, "json", false, "print machine-readable status")
	cmd.AddCommand(status)
	addCloudflareManagementCommands(cmd)
	return cmd
}

func readCloudflareStatus(ctx context.Context, home string) (cloudflareStatus, error) {
	body, err := readDesktopStatus(ctx, home, "/api/cloudflare/status")
	if err != nil {
		return cloudflareStatus{}, err
	}
	if body == nil {
		return cloudflareStatus{State: "stopped"}, nil
	}
	var value cloudflareStatus
	if err := json.Unmarshal(body, &value); err != nil {
		return cloudflareStatus{}, fmt.Errorf("invalid Cloudflare status: %w", err)
	}
	return value, nil
}
