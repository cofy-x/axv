package cli

import (
	"fmt"
	"strings"

	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/spf13/cobra"
)

func newExec() *cobra.Command {
	var directory string
	var timeoutSeconds int
	var machineJSON bool
	cmd := &cobra.Command{
		Use:   "exec <name|id> -- <command> [args...]",
		Short: "Run a command directly inside a running VM",
		Args:  arguments(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeoutSeconds < 0 || timeoutSeconds > 300 {
				return usageError(fmt.Errorf("--timeout must be between 0 and 300 seconds"))
			}
			c, err := commandClient(cmd)
			if err != nil {
				return err
			}
			defer c.Close()
			m, err := c.Resolve(cmd.Context(), strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			result, err := c.Exec(cmd.Context(), m.ID, guestproto.ExecRequest{
				Argv:           args[1:],
				Directory:      directory,
				TimeoutSeconds: timeoutSeconds,
			})
			if err != nil {
				return err
			}
			if machineJSON {
				if err := jsonResult(cmd.OutOrStdout(), result); err != nil {
					return err
				}
			} else {
				if result.Stdout != "" {
					fmt.Fprint(cmd.OutOrStdout(), result.Stdout)
				}
				if result.Stderr != "" {
					fmt.Fprint(cmd.ErrOrStderr(), result.Stderr)
				}
			}
			if result.ExitCode != 0 {
				return &exitError{code: result.ExitCode}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&directory, "directory", "", "working directory inside the guest (default: guest home)")
	cmd.Flags().IntVar(&timeoutSeconds, "timeout", 60, "command timeout in seconds (1–300)")
	cmd.Flags().BoolVar(&machineJSON, "json", false, "write machine-readable JSON")
	return cmd
}
