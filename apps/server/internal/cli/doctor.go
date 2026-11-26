package cli

import (
	"github.com/cofy-x/axv/apps/server/internal/doctor"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/spf13/cobra"
)

func newDoctor(version string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check AXV services, virtualization prerequisites, tools and storage hygiene",
		Long:  "Inspect the local workstation environment without modifying configuration or starting services.",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			report := doctor.Run(cmd.Context(), home, version)
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			if asJSON {
				return jsonResult(cmd.OutOrStdout(), report)
			}
			return doctor.WriteText(cmd.OutOrStdout(), report)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON diagnostics")
	return cmd
}
