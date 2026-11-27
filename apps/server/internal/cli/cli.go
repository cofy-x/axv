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
	"runtime"
	"strings"
	"syscall"

	"github.com/cofy-x/axv/apps/server/internal/app"
	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	computehost "github.com/cofy-x/axv/apps/server/internal/compute/host"
	"github.com/cofy-x/axv/apps/server/internal/compute/hostinstall"
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
	root.AddCommand(newServe(version), newDaemon(version), newDoctor(version), newGC(), newSetup(), newVM(), newSSHProxy(), newCache(), newRemote(), newCloudflare())
	root.AddCommand(newHost(version))
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
		return runDesktopSnapshot(cmd.Context(), snapshot, version, cmd.OutOrStdout())
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

func serveFlags(flags *pflag.FlagSet, options *serveOptions) {
	flags.StringVar(&options.addr, "addr", "", "desktop HTTP listen address")
	flags.StringVar(&options.dataDir, "data-dir", "", "directory containing desktop.sqlite")
}

func newServe(version string) *cobra.Command {
	var options serveOptions
	cmd := &cobra.Command{Use: "serve", Short: "Run the desktop and configured VM host in the foreground", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := nonemptyFlags(cmd, "addr", "data-dir"); err != nil {
			return err
		}
		settings, err := resolvedSettings(options)
		if err != nil {
			return err
		}
		settings.Version = version
		return app.ServeContext(cmd.Context(), settings, cmd.OutOrStdout(), true)
	}}
	serveFlags(cmd.Flags(), &options)
	return cmd
}

func newHost(version string) *cobra.Command {
	var hostConfig string
	host := &cobra.Command{Use: "host", Short: "Run or manage the AXV VM host service", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := nonemptyFlags(cmd, "config"); err != nil {
			return err
		}
		if !filepath.IsAbs(hostConfig) {
			return usageError(fmt.Errorf("--config requires an absolute host configuration path"))
		}
		return computehost.Run(hostConfig, cmd.OutOrStdout())
	}}
	host.Flags().StringVar(&hostConfig, "config", "", "absolute host configuration JSON path (required)")

	var checkHost, noEnable, applySysctl, noConnect bool
	var socketGroup string
	installHost := &cobra.Command{
		Use:   "install-service",
		Short: "Install Firecracker and the default VM image, then enable and start the Linux host service",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := nonemptyFlags(cmd, "socket-group"); err != nil {
				return err
			}
			return hostinstall.Install(cmd.Context(), hostinstall.InstallOptions{
				Check:          checkHost,
				SocketGroup:    socketGroup,
				Enable:         !noEnable,
				ApplySysctl:    applySysctl,
				ConnectProfile: !noConnect,
			}, cmd.OutOrStdout())
		},
	}
	installHost.Flags().BoolVar(&checkHost, "check", false, "check this host without downloading, installing or starting the service")
	installHost.Flags().StringVar(&socketGroup, "socket-group", "", "explicitly grant VM control to all members of this existing group (name or GID)")
	installHost.Flags().BoolVar(&noEnable, "no-enable", false, "do not enable axv-host.service on boot")
	installHost.Flags().BoolVar(&applySysctl, "apply-sysctl", false, "enable and persist net.ipv4.ip_forward=1 in /etc/sysctl.d/99-axv.conf")
	installHost.Flags().BoolVar(&noConnect, "no-connect", false, "do not automatically connect the invoking desktop profile to the installed socket")

	var statusJSON bool
	hostStatus := &cobra.Command{
		Use:   "status",
		Short: "Show the AXV VM host service status",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHostStatus(cmd.Context(), version, statusJSON, cmd.OutOrStdout())
		},
	}
	hostStatus.Flags().BoolVar(&statusJSON, "json", false, "print machine-readable JSON status")

	vmHome := func() (string, error) {
		if runtime.GOOS == "linux" {
			return "", nil
		}
		return profile.Home()
	}

	hostStart := &cobra.Command{Use: "start", Short: "Start the AXV VM host service (Linux axv-host.service or macOS managed Lima)", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := vmHome()
		if err != nil {
			return err
		}
		return daemon.StartVMHost(cmd.Context(), home, cmd.OutOrStdout())
	}}
	hostStop := &cobra.Command{Use: "stop", Short: "Stop the AXV VM host service while preserving running VMs", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := vmHome()
		if err != nil {
			return err
		}
		return daemon.StopVMHost(cmd.Context(), home, cmd.OutOrStdout())
	}}
	hostRestart := &cobra.Command{Use: "restart", Short: "Restart the AXV VM host service while preserving running VMs", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := vmHome()
		if err != nil {
			return err
		}
		return daemon.RestartVMHost(cmd.Context(), home, cmd.OutOrStdout())
	}}
	hostUpgrade := &cobra.Command{Use: "upgrade", Short: "Apply this AXV binary to the VM host service and restart without killing VMs", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if runtime.GOOS == "linux" {
			return hostinstall.UpgradeService(cmd.Context(), cmd.OutOrStdout())
		}
		home, err := profile.Home()
		if err != nil {
			return err
		}
		if err := daemon.RestartVMHost(cmd.Context(), home, cmd.OutOrStdout()); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Upgraded managed VM host to AXV %s.\n", version)
		return nil
	}}
	var purgeConfig, purgeData bool
	hostUninstall := &cobra.Command{Use: "uninstall", Short: "Stop and remove the AXV VM host service", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if runtime.GOOS == "linux" {
			return hostinstall.UninstallService(cmd.Context(), hostinstall.UninstallOptions{PurgeConfig: purgeConfig, PurgeData: purgeData}, cmd.OutOrStdout())
		}
		home, err := profile.Home()
		if err != nil {
			return err
		}
		return daemon.StopVMHost(cmd.Context(), home, cmd.OutOrStdout())
	}}
	hostUninstall.Flags().BoolVar(&purgeConfig, "purge", false, "on Linux, also remove /etc/axv/host.json, /opt/axv/images and sysctl drop-in")
	hostUninstall.Flags().BoolVar(&purgeData, "purge-data", false, "on Linux, also delete /var/lib/axv VM storage and volumes")

	host.AddCommand(installHost, hostStatus, hostStart, hostStop, hostRestart, hostUpgrade, hostUninstall)
	return host
}

func newDaemon(version string) *cobra.Command {
	cmd := &cobra.Command{Use: "daemon", Short: "Manage AXV desktop and VM background services", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	// start
	var startOpts serveOptions
	var startOpen, startEnable, startWithVM bool
	var startOnly string
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start or reuse background desktop and/or VM services",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := nonemptyFlags(cmd, "addr", "data-dir", "only"); err != nil {
				return err
			}
			if startOnly != "" && startOnly != "desktop" && startOnly != "vm" {
				return usageError(fmt.Errorf("--only must be desktop or vm"))
			}
			home, err := profile.Home()
			if err != nil {
				return err
			}
			if startOnly == "vm" {
				return daemon.StartVMHost(cmd.Context(), home, cmd.OutOrStdout())
			}
			settings, err := resolvedSettings(startOpts)
			if err != nil {
				return err
			}
			if startWithVM {
				if err := daemon.StartVMHost(cmd.Context(), settings.Home, cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			if err := daemon.Start(cmd.Context(), settings, cmd.OutOrStdout(), startOpen); err != nil {
				return err
			}
			if startEnable {
				return daemon.EnableAutostart(cmd.Context(), settings.Home, "", cmd.OutOrStdout())
			}
			return nil
		},
	}
	serveFlags(startCmd.Flags(), &startOpts)
	startCmd.Flags().BoolVar(&startOpen, "open", false, "open the verified desktop in a browser")
	startCmd.Flags().BoolVar(&startEnable, "enable", false, "also enable boot/login autostart for this profile")
	startCmd.Flags().BoolVar(&startWithVM, "with-vm", false, "also ensure the local VM host service is started")
	startCmd.Flags().StringVar(&startOnly, "only", "", "start only desktop or vm")

	// restart
	var restartOpts serveOptions
	var restartOpen, restartAll bool
	var restartOnly string
	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart background desktop and/or VM services (preserving VMs and Codex runs)",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := nonemptyFlags(cmd, "addr", "data-dir", "only"); err != nil {
				return err
			}
			if restartOnly != "" && restartOnly != "desktop" && restartOnly != "vm" {
				return usageError(fmt.Errorf("--only must be desktop or vm"))
			}
			home, err := profile.Home()
			if err != nil {
				return err
			}
			if restartOnly == "vm" {
				return daemon.RestartVMHost(cmd.Context(), home, cmd.OutOrStdout())
			}
			if restartAll {
				if err := daemon.RestartVMHost(cmd.Context(), home, cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			settings, err := resolvedSettings(restartOpts)
			if err != nil {
				return err
			}
			return daemon.Restart(cmd.Context(), settings, cmd.OutOrStdout(), restartOpen)
		},
	}
	serveFlags(restartCmd.Flags(), &restartOpts)
	restartCmd.Flags().BoolVar(&restartOpen, "open", false, "open the verified desktop in a browser")
	restartCmd.Flags().BoolVar(&restartAll, "all", false, "restart both the desktop and the VM host service")
	restartCmd.Flags().StringVar(&restartOnly, "only", "", "restart only desktop or vm")

	// stop
	var stopAll bool
	var stopOnly string
	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop owned services, retaining running VMs, project volumes and Codex sessions",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := nonemptyFlags(cmd, "only"); err != nil {
				return err
			}
			if stopOnly != "" && stopOnly != "desktop" && stopOnly != "vm" {
				return usageError(fmt.Errorf("--only must be desktop or vm"))
			}
			home, err := profile.Home()
			if err != nil {
				return err
			}
			switch stopOnly {
			case "desktop":
				return daemon.StopDesktopOnly(cmd.Context(), home, cmd.OutOrStdout())
			case "vm":
				return daemon.StopVMHost(cmd.Context(), home, cmd.OutOrStdout())
			}
			if err := daemon.Stop(cmd.Context(), home, cmd.OutOrStdout()); err != nil {
				return err
			}
			if stopAll {
				return daemon.StopVMHost(cmd.Context(), home, cmd.OutOrStdout())
			}
			return nil
		},
	}
	stopCmd.Flags().BoolVar(&stopAll, "all", false, "also stop the system Linux VM host service (axv-host.service)")
	stopCmd.Flags().StringVar(&stopOnly, "only", "", "stop only desktop or vm")

	// status
	var statusJSON bool
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show verified desktop, autostart, remote and VM host readiness",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			return daemon.StatusWithOptions(cmd.Context(), home, daemon.StatusOptions{
				JSON:           statusJSON,
				CurrentVersion: version,
			}, cmd.OutOrStdout())
		},
	}
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "print machine-readable JSON status")

	// logs
	var logFollow, logDesktop, logVM bool
	var logLines int
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "Print or follow desktop and VM host logs",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if logLines < 0 {
				return usageError(fmt.Errorf("--lines must be zero or positive"))
			}
			home, err := profile.Home()
			if err != nil {
				return err
			}
			return daemon.LogsWithOptions(cmd.Context(), home, daemon.LogsOptions{
				Follow:  logFollow,
				Lines:   logLines,
				Desktop: logDesktop,
				VM:      logVM,
			}, cmd.OutOrStdout())
		},
	}
	logsCmd.Flags().BoolVarP(&logFollow, "follow", "f", false, "follow log output")
	logsCmd.Flags().IntVarP(&logLines, "lines", "n", 0, "limit output to the last N lines (0 prints the full bounded tail)")
	logsCmd.Flags().BoolVar(&logDesktop, "desktop", false, "show desktop service logs")
	logsCmd.Flags().BoolVar(&logVM, "vm", false, "show VM host service logs (including systemd journal on Linux)")

	// enable / disable
	enableCmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable persistent boot/login autostart for the desktop service",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			return daemon.EnableAutostart(cmd.Context(), home, "", cmd.OutOrStdout())
		},
	}
	disableCmd := &cobra.Command{
		Use:   "disable",
		Short: "Disable persistent boot/login autostart for the desktop service",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			return daemon.DisableAutostart(cmd.Context(), home, cmd.OutOrStdout())
		},
	}

	// upgrade
	var upgradeVMOnly, upgradeDesktopOnly bool
	upgradeCmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Apply this AXV binary to installed services (Linux VM host and/or desktop daemon)",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if upgradeVMOnly && upgradeDesktopOnly {
				return usageError(fmt.Errorf("choose at most one of --vm-only or --desktop-only"))
			}
			settings, err := resolvedSettings(serveOptions{})
			if err != nil {
				return err
			}
			return daemon.Upgrade(cmd.Context(), settings, upgradeVMOnly, upgradeDesktopOnly, cmd.OutOrStdout())
		},
	}
	upgradeCmd.Flags().BoolVar(&upgradeVMOnly, "vm-only", false, "upgrade only the VM host service")
	upgradeCmd.Flags().BoolVar(&upgradeDesktopOnly, "desktop-only", false, "restart only the desktop daemon")

	// uninstall
	var uninstallAll, uninstallVM, uninstallPurge, uninstallPurgeData bool
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and unregister AXV background services",
		Args:  arguments(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := profile.Home()
			if err != nil {
				return err
			}
			if !uninstallVM {
				if err := daemon.DisableAutostart(cmd.Context(), home, cmd.OutOrStdout()); err != nil {
					return err
				}
				if err := daemon.Stop(cmd.Context(), home, cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			if uninstallVM || uninstallAll {
				return hostinstall.UninstallService(cmd.Context(), hostinstall.UninstallOptions{
					PurgeConfig: uninstallPurge,
					PurgeData:   uninstallPurgeData,
				}, cmd.OutOrStdout())
			}
			return nil
		},
	}
	uninstallCmd.Flags().BoolVar(&uninstallAll, "all", false, "also uninstall the Linux axv-host.service")
	uninstallCmd.Flags().BoolVar(&uninstallVM, "vm", false, "uninstall only the Linux axv-host.service")
	uninstallCmd.Flags().BoolVar(&uninstallPurge, "purge", false, "with --vm/--all, also remove /etc/axv/host.json and /opt/axv/images")
	uninstallCmd.Flags().BoolVar(&uninstallPurgeData, "purge-data", false, "with --vm/--all, also delete /var/lib/axv VM storage")

	cmd.AddCommand(startCmd, restartCmd, stopCmd, statusCmd, logsCmd, enableCmd, disableCmd, upgradeCmd, uninstallCmd)
	return cmd
}
