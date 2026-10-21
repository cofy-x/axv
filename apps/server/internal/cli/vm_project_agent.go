package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/vmwork"
	"github.com/spf13/cobra"
)

type vmProjectContextKey struct{}

func boundVMProject(cmd *cobra.Command) (vmwork.Context, bool) {
	value, ok := cmd.Context().Value(vmProjectContextKey{}).(vmwork.Context)
	return value, ok
}

func projectBindingForCommand(cmd *cobra.Command, filename, projectID string) (compute.ProjectBinding, error) {
	if bound, ok := boundVMProject(cmd); ok {
		if cmd.Flags().Changed("binding-file") || filename != "" || projectID != bound.Project.Project.ID {
			return compute.ProjectBinding{}, usageError(fmt.Errorf("the bound VM project command cannot replace its original project or environment"))
		}
		return bound.Project.Binding, nil
	}
	return readProjectBinding(filename, projectID)
}

// The private entry point exposes only the existing file/task operations. It
// supplies one captured project and client without changing process environment,
// reading a mutable profile, activating a service or capturing a newer binding.
func newVMProjectAgent() *cobra.Command {
	return &cobra.Command{
		Use: "internal-vm-project <context-file> <command> [args...]", Hidden: true,
		DisableFlagParsing: true, Args: arguments(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			bound, err := vmwork.ReadContext(args[0])
			if err != nil {
				return err
			}
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			running, runningErr := os.Stat(executable)
			captured, capturedErr := os.Stat(bound.Executable)
			if runningErr != nil || capturedErr != nil || !os.SameFile(running, captured) {
				return fmt.Errorf("use the original immutable AXV executable recorded for this VM project run")
			}
			commands := &cobra.Command{
				Use: "VM-project", Short: "Access this run's original VM project and persistent tasks",
				SilenceErrors: true, SilenceUsage: true,
				Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
			}
			commands.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err) })
			for _, action := range []string{"files", "read", "write", "mkdir"} {
				commands.AddCommand(newVMProjectFile(action))
			}
			commands.AddCommand(newVMProjectTask())
			var boundHelp func(*cobra.Command)
			boundHelp = func(command *cobra.Command) {
				command.Use = strings.ReplaceAll(command.Use, "<project-id> ", "")
				command.Use = strings.TrimSuffix(command.Use, " <project-id>")
				for _, name := range []string{"binding-file", "host-id"} {
					if flag := command.Flags().Lookup(name); flag != nil {
						flag.Hidden = true
					}
				}
				for _, child := range command.Commands() {
					boundHelp(child)
				}
			}
			boundHelp(commands)
			forward := append([]string(nil), args[1:]...)
			switch forward[0] {
			case "files", "read", "write", "mkdir":
				forward = append([]string{forward[0], bound.Project.Project.ID}, forward[1:]...)
			case "task":
				if len(forward) > 1 && forward[1] != "--help" && forward[1] != "-h" {
					forward = append([]string{"task", forward[1], bound.Project.Project.ID}, forward[2:]...)
				}
			case "--help", "-h":
			default:
				return usageError(fmt.Errorf("the bound VM project command supports only files, read, write, mkdir and task operations"))
			}
			commands.SetArgs(forward)
			commands.SetIn(cmd.InOrStdin())
			commands.SetOut(cmd.OutOrStdout())
			commands.SetErr(cmd.ErrOrStderr())
			return commands.ExecuteContext(context.WithValue(cmd.Context(), vmProjectContextKey{}, bound))
		},
	}
}
