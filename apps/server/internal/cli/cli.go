// Package cli owns command-line parsing and adapts commands to service APIs.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cofy-x/axv/apps/server/internal/app"
	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	computehost "github.com/cofy-x/axv/apps/server/internal/compute/host"
	"github.com/cofy-x/axv/apps/server/internal/daemon"
	"github.com/cofy-x/axv/apps/server/internal/guest"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const appName = "axv"

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}
func (e *exitError) Unwrap() error { return e.err }

func ExitCode(err error) int {
	var status *exitError
	if errors.As(err, &status) {
		return status.code
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if err != nil {
		return 1
	}
	return 0
}

func usageError(err error) error { return &exitError{code: 2, err: err} }

// Run owns process signals for convenience callers. Execute uses only the
// caller's context and streams, including the main executable's signal context.
func Run(args []string, output io.Writer, version string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Execute(ctx, args, os.Stdin, output, os.Stderr, version)
}

func Execute(ctx context.Context, args []string, input io.Reader, output, diagnostic io.Writer, version string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if input == nil {
		input = strings.NewReader("")
	}
	if output == nil {
		output = io.Discard
	}
	if diagnostic == nil {
		diagnostic = io.Discard
	}
	root := newRoot(version)
	root.SetArgs(args)
	root.SetIn(input)
	root.SetOut(output)
	root.SetErr(diagnostic)
	err := root.ExecuteContext(ctx)
	if err != nil && strings.HasPrefix(err.Error(), "unknown command ") {
		return usageError(err)
	}
	return err
}

func arguments(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return usageError(err)
		}
		return nil
	}
}

func newRoot(version string) *cobra.Command {
	root := &cobra.Command{
		Use: appName, Short: "Your personal cloud for VMs and coding agents",
		Long:    "AXV is a personal cloud workstation. With no command, start or reuse the background workstation and open its browser desktop.",
		Version: version, Args: arguments(cobra.NoArgs), SilenceErrors: true, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			return daemon.Launch(cmd.Context(), home, cmd.OutOrStdout(), func() (app.Settings, error) { return resolvedSettings(serveOptions{}) })
		},
	}
	root.SetVersionTemplate("axv {{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err) })
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print the version", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "axv %s\n", version)
		return err
	}})
	root.AddCommand(&cobra.Command{Use: "init", Short: "Create a starter profile without starting services", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := profile.Home()
		if err != nil {
			return err
		}
		if _, err = profile.Initialize(home); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "AXV configuration: %s\nRun axv setup to prepare VM hosting, or axv to open the desktop.\n", filepath.Join(home, "config.json"))
		return err
	}})
	root.AddCommand(newServe(), newDaemon(), newSetup(), newVM(), newSSHProxy(), newCache())
	var hostConfig string
	host := &cobra.Command{Use: "host", Short: "Run the independent VM host service", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := nonemptyFlags(cmd, "config"); err != nil {
			return err
		}
		if !filepath.IsAbs(hostConfig) {
			return usageError(fmt.Errorf("--config requires an absolute host configuration path"))
		}
		return computehost.Run(hostConfig, cmd.OutOrStdout())
	}}
	host.Flags().StringVar(&hostConfig, "config", "", "absolute host configuration JSON path (required)")
	root.AddCommand(host)
	guestOptions := guest.Options{ConfigPath: "/etc/axv/guest.json", Listen: "vsock"}
	guestCmd := &cobra.Command{Use: "guest", Short: "Run the Linux guest service", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := nonemptyFlags(cmd, "config", "listen"); err != nil {
			return err
		}
		return guest.RunOptions(guestOptions, cmd.OutOrStdout())
	}}
	guestCmd.Flags().StringVar(&guestOptions.ConfigPath, "config", guestOptions.ConfigPath, "guest identity and project-root JSON")
	guestCmd.Flags().StringVar(&guestOptions.Listen, "listen", guestOptions.Listen, "vsock or unix:/absolute/path")
	root.AddCommand(guestCmd)
	root.AddCommand(&cobra.Command{Use: "internal-codex-run <manifest>", Hidden: true, Args: arguments(cobra.ExactArgs(1)), RunE: func(_ *cobra.Command, args []string) error { return runner.Run(args[0]) }})
	var snapshot string
	desktop := &cobra.Command{Use: "_desktop", Hidden: true, Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return runDesktopSnapshot(cmd.Context(), []string{"--snapshot", snapshot}, cmd.OutOrStdout())
	}}
	desktop.Flags().StringVar(&snapshot, "snapshot", "", "immutable desktop snapshot")
	root.AddCommand(desktop)
	return root
}

func nonemptyFlags(cmd *cobra.Command, names ...string) error {
	for _, name := range names {
		flag := cmd.Flags().Lookup(name)
		if flag == nil || !flag.Changed {
			continue
		}
		value := flag.Value.String()
		if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "-") {
			return usageError(fmt.Errorf("--%s requires a non-empty value (prefix paths starting with '-' with './')", name))
		}
	}
	return nil
}

func serveFlags(flags *pflag.FlagSet, options *serveOptions, guest bool) {
	flags.StringVar(&options.addr, "addr", "", "desktop HTTP listen address")
	flags.StringVar(&options.dataDir, "data-dir", "", "directory containing desktop.sqlite")
	if guest {
	}
}

func newServe() *cobra.Command {
	var options serveOptions
	cmd := &cobra.Command{Use: "serve", Short: "Run the desktop and configured VM host in the foreground", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := nonemptyFlags(cmd, "addr", "data-dir"); err != nil {
			return err
		}
		settings, err := resolvedSettings(options)
		if err != nil {
			return err
		}
		return app.ServeContext(cmd.Context(), settings, cmd.OutOrStdout(), true)
	}}
	serveFlags(cmd.Flags(), &options, true)
	return cmd
}

func newDaemon() *cobra.Command {
	cmd := &cobra.Command{Use: "daemon", Short: "Manage background services for this login session", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	for _, action := range []string{"start", "restart", "stop", "status", "logs"} {
		var options serveOptions
		var open bool
		child := &cobra.Command{Use: action, Short: map[string]string{"start": "Start or reuse the background desktop", "restart": "Restart the desktop, preserving the VM host and VMs", "stop": "Stop owned services, retaining VMs and project volumes", "status": "Show verified desktop and VM host readiness", "logs": "Print recent owned service logs"}[action], Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
			if action == "start" || action == "restart" {
				if err := nonemptyFlags(cmd, "addr", "data-dir"); err != nil {
					return err
				}
				settings, err := resolvedSettings(options)
				if err != nil {
					return err
				}
				if action == "start" {
					return daemon.Start(cmd.Context(), settings, cmd.OutOrStdout(), open)
				}
				return daemon.Restart(cmd.Context(), settings, cmd.OutOrStdout(), open)
			}
			home, err := profile.Home()
			if err != nil {
				return err
			}
			switch action {
			case "stop":
				return daemon.Stop(cmd.Context(), home, cmd.OutOrStdout())
			case "status":
				return daemon.Status(cmd.Context(), home, cmd.OutOrStdout())
			default:
				return daemon.Logs(cmd.Context(), home, cmd.OutOrStdout())
			}
		}}
		if action == "start" || action == "restart" {
			serveFlags(child.Flags(), &options, false)
			child.Flags().BoolVar(&open, "open", false, "open the verified desktop in a browser")
		}
		cmd.AddCommand(child)
	}
	return cmd
}
