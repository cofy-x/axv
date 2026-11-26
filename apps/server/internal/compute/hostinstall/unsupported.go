//go:build !linux || !amd64

package hostinstall

import (
	"context"
	"fmt"
	"io"
)

func Install(context.Context, InstallOptions, io.Writer) error {
	return fmt.Errorf("host install-service supports Linux amd64 with systemd and KVM")
}
