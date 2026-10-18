package filelock

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// TryLock opens path and attempts an exclusive lock without waiting.
func TryLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		f.Close()
		return nil, fmt.Errorf("another AXV process already owns this directory: %w", err)
	}
	return f, nil
}
