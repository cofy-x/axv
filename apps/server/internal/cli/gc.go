package cli

import (
	"fmt"

	"github.com/cofy-x/axv/apps/server/internal/daemon"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/spf13/cobra"
)

func newGC() *cobra.Command {
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Clean up unreferenced pinned AXV executables and stale launch records",
		Long:  "Safely remove stale copies under AXV_HOME/bin and AXV_HOME/run that are no longer referenced by running services or recorded Codex runs.",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			report, err := daemon.GC(cmd.Context(), home, dryRun)
			if err != nil {
				return err
			}
			if asJSON {
				return jsonResult(cmd.OutOrStdout(), report)
			}
			if len(report.Removed) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No stale AXV runtime artifacts found in %s.\n", report.Home)
				return nil
			}
			verb := "Removed"
			if dryRun {
				verb = "Would remove"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %d stale artifact(s) (%.2f MiB):\n", verb, len(report.Removed), float64(report.ReclaimedBytes)/(1<<20))
			for _, item := range report.Removed {
				fmt.Fprintf(cmd.OutOrStdout(), "  [%s] %s (%d bytes)\n", item.Kind, item.Path, item.Bytes)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list stale artifacts without deleting them")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON summary")
	return cmd
}
