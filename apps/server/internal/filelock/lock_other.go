//go:build !linux && !darwin && !windows

package filelock

import (
	"fmt"
	"os"
)

// TryLock opens path and attempts an exclusive lock without waiting.
func TryLock(string) (*os.File, error) {
	return nil, fmt.Errorf("exclusive file locking is unavailable on this platform")
}
