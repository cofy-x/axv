package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/setup"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newSetup() *cobra.Command {
	o := setup.Options{}
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
			if f.Name != "yes" && f.Name != "retry" {
				o.Explicit = true
			}
		})
		if err := setup.Normalize(&o); err != nil {
			return usageError(err)
		}
		if !o.Yes {
			o.Confirm = setupConfirmation(cmd.InOrStdin(), cmd.OutOrStdout())
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
	f.StringVar(&o.Retry, "retry", "", "retry an interrupted or failed preparation task ID")
	return cmd
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
		if _, err := fmt.Fprint(output, "Approve these actions and downloads? Type yes: "); err != nil {
			return err
		}
		line, err := setupConfirmationLine(ctx, file)
		if err != nil && err != io.EOF {
			return err
		}
		if strings.TrimSpace(line) != "yes" {
			return fmt.Errorf("preparation was not approved")
		}
		return ctx.Err()
	}
}
