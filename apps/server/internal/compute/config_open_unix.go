//go:build unix

package compute

import (
	"os"

	"golang.org/x/sys/unix"
)

func openHostConfig(root *os.Root, name string) (*os.File, error) {
	// Opening a replaced path must not block on a named pipe.
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
}
