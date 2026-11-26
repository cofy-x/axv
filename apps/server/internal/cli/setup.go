package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute/hostinstall"
	"github.com/cofy-x/axv/apps/server/internal/setup"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newSetup() *cobra.Command {
	o := setup.Options{}
	var applySysctl bool
	cmd := &cobra.Command{Use: "setup", Short: "Review and prepare VM hosting", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := nonemptyFlags(cmd, "socket", "host-config", "image-bundle", "image-source", "storage-root", "lima-home", "retry"); err != nil {
			return err
		}
		for name, minimum := range map[string]int64{"cpus": 1, "memory-mib": 256, "disk-gib": 1, "machines": 1} {
			if !cmd.Flags().Changed(name) {
				continue
			}
			var value int64
			switch name {
			case "cpus":
				value = int64(o.Request.CPUs)
			case "memory-mib":
				value = o.Request.MemoryMiB
			case "disk-gib":
				value = o.Request.DiskGiB
			case "machines":
				value = int64(o.Request.Machines)
			}
			if value < minimum {
				return usageError(fmt.Errorf("--%s must be at least %d", name, minimum))
			}
		}
		cmd.Flags().Visit(func(f *pflag.Flag) {
			if f.Name != "yes" && f.Name != "retry" && f.Name != "apply-sysctl" {
				o.Explicit = true
			}
		})
		if err := setup.Normalize(&o); err != nil {
			return usageError(err)
		}
		if !o.Yes {
			o.Confirm = setupConfirmation(cmd.InOrStdin(), cmd.OutOrStdout())
		}
		// On Linux, unify setup when no custom socket/host-config/retry is requested
		// and axv-host.service is missing or stopped.
		if runtime.GOOS == "linux" && !o.Explicit && o.Retry == "" && os.Getenv("AXV_COMPUTE_SOCKET") == "" {
			if handled, err := maybePrepareLinuxHost(cmd, o, applySysctl); handled || err != nil {
				return err
			}
		}
		return setup.RunOptions(cmd.Context(), o, cmd.OutOrStdout(), cmd.ErrOrStderr())
	}}
	f := cmd.Flags()
	f.StringVar(&o.Request.Socket, "socket", "", "local Unix socket for an independently managed VM service")
	f.StringVar(&o.Request.HostConfig, "host-config", "", "existing host configuration on this computer")
	f.StringVar(&o.Request.ImageSource, "image-source", "", "ubuntu-base (default; basic VM), official (publication pending), or bundle")
	f.StringVar(&o.Request.ImageBundle, "image-bundle", "", "local AXV image bundle manifest with verified immutable rootfs")
	f.StringVar(&o.Request.StorageRoot, "storage-root", "", "dedicated directory for new VM storage and images")
	f.StringVar(&o.Request.LimaHome, "lima-home", "", "short dedicated Lima state directory")
	f.IntVar(&o.Request.CPUs, "cpus", 0, "total VM CPU allocation limit")
	f.Int64Var(&o.Request.MemoryMiB, "memory-mib", 0, "total VM memory limit in MiB")
	f.Int64Var(&o.Request.DiskGiB, "disk-gib", 0, "total VM disk limit in GiB")
	f.IntVar(&o.Request.Machines, "machines", 0, "maximum number of machines")
	f.BoolVar(&o.Yes, "yes", false, "approve the displayed preparation actions and downloads")
	f.BoolVar(&applySysctl, "apply-sysctl", false, "on Linux as root, enable and persist net.ipv4.ip_forward=1")
	f.StringVar(&o.Retry, "retry", "", "retry an interrupted or failed preparation task ID")
	return cmd
}

func maybePrepareLinuxHost(cmd *cobra.Command, o setup.Options, applySysctl bool) (bool, error) {
	if err := cmd.Context().Err(); err != nil {
		return true, err
	}
	// Do not interfere when a desktop is already running or profile is already configured;
	// let setup.RunOptions follow or inspect the live desktop preparation engine.
	if _, statErr := os.Stat(hostinstall.StandardSocket); statErr == nil {
		return false, nil
	}
	st := hostinstall.Status(cmd.Context(), "")
	if !st.Supported || st.Active {
		return false, nil
	}
	if !st.Installed && runtime.GOARCH == "amd64" {
		if os.Geteuid() == 0 {
			if !o.Yes {
				if o.Confirm == nil {
					return true, fmt.Errorf("Linux VM service (%s) is not installed; rerun with --yes or run `sudo axv host install-service --apply-sysctl`", hostinstall.UnitName)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Linux VM service is not installed. This will install Firecracker, the default AXV Guest image, enable axv-host.service, and connect this profile.")
				if err := o.Confirm(cmd.Context()); err != nil {
					return true, err
				}
			}
			return true, hostinstall.Install(cmd.Context(), hostinstall.InstallOptions{
				Enable:         true,
				ApplySysctl:    applySysctl,
				ConnectProfile: true,
			}, cmd.OutOrStdout())
		}
		return false, nil
	}
	if st.Installed && !st.Active && os.Geteuid() == 0 {
		if err := hostinstall.StartService(cmd.Context(), cmd.OutOrStdout()); err != nil {
			return true, err
		}
		return false, nil
	}
	return false, nil
}

func setupConfirmation(input io.Reader, output io.Writer) func(context.Context) error {
	file, ok := input.(*os.File)
	if !ok || !isatty.IsTerminal(file.Fd()) {
		return nil
	}
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := fmt.Fprint(output, "Approve these actions and downloads? [y/N]: "); err != nil {
			return err
		}
		line, err := setupConfirmationLine(ctx, file)
		if err != nil && err != io.EOF {
			return err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return fmt.Errorf("preparation was not approved")
		}
		return ctx.Err()
	}
}
