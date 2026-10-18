//go:build linux

package guest

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/mdlayher/vsock"
)

func Run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("guest", flag.ContinueOnError)
	flags.SetOutput(output)
	configPath := flags.String("config", "/etc/axv/guest.json", "guest identity and project-root JSON")
	listen := flags.String("listen", "vsock", "vsock or unix:/absolute/path")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("guest does not accept positional arguments")
	}
	return RunOptions(Options{ConfigPath: *configPath, Listen: *listen}, output)
}

func RunOptions(options Options, output io.Writer) error {
	if options.ConfigPath == "" {
		options.ConfigPath = "/etc/axv/guest.json"
	}
	if options.Listen == "" {
		options.Listen = "vsock"
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("run the guest execution service as the unprivileged axv user")
	}
	installedImage, err := image.ReadInstalled()
	if err != nil {
		return fmt.Errorf("incompatible guest image: %w", err)
	}
	config, err := readConfig(options.ConfigPath)
	if err != nil {
		return err
	}
	if config.MachineID == "" || len(config.Token) < 32 || !filepath.IsAbs(config.ProjectRoot) {
		return fmt.Errorf("invalid guest configuration")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	s := newService(ctx, config, strings.TrimSpace(string(boot)), installedImage.Guest.Capabilities, func() error { return projectVolumeReady(config.ProjectRoot) })
	defer s.close()
	listener, err := listenGuest(options.Listen)
	if err != nil {
		return err
	}
	return s.serve(listener, output)
}

func listenVSOCK() (net.Listener, error) {
	return vsock.Listen(protocol.Port, nil)
}
