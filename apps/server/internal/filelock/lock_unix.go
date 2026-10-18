//go:build linux || darwin

package filelock

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// TryLock opens path and attempts an exclusive lock without waiting.
func TryLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another AXV process already owns this directory: %w", err)
	}
	return f, nil
}
