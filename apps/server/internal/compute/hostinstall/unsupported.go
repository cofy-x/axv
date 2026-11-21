//go:build !linux || !amd64

package hostinstall

import (
	"context"
	"fmt"
	"io"
)

func DefaultImageInstallAvailable() bool { return false }

func ReadImageReload(string, string, string) (ImageReload, bool) {
	return ImageReload{}, false
}

func InstallImage(context.Context, string, io.Writer) error {
	return fmt.Errorf("host install-image supports Linux amd64 with the installed Firecracker system service")
}

func Run(context.Context, bool, string, io.Writer) error {
	return fmt.Errorf("host install-service supports Linux amd64 with systemd and KVM")
}
