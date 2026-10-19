//go:build darwin || linux

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/filelock"
)

// AcquireOwnership serializes a managed conversation's provider processes.
// Keep the returned file open until all owned processes have been reaped.
// Lock files retain their identity across runs and must never be removed.
func AcquireOwnership(ctx context.Context, path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
		return nil, ErrUnavailable
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || !ownershipPrivate(info) {
		return nil, ErrUnavailable
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if current, err := os.Lstat(path); err == nil {
			if !current.Mode().IsRegular() || !ownershipPrivate(current) {
				return nil, ErrUnavailable
			}
		} else if !os.IsNotExist(err) {
			return nil, ErrUnavailable
		}
		lock, err := filelock.TryLock(path)
		if err == nil {
			opened, statErr := lock.Stat()
			current, pathErr := os.Lstat(path)
			if statErr != nil || pathErr != nil || !opened.Mode().IsRegular() ||
				!ownershipPrivate(opened) || !os.SameFile(opened, current) {
				_ = lock.Close()
				return nil, ErrUnavailable
			}
			if err := ctx.Err(); err != nil {
				_ = lock.Close()
				return nil, err
			}
			return lock, nil
		}
		if !contextLockContended(err) {
			return nil, ErrUnavailable
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func ownershipPrivate(info os.FileInfo) bool {
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && owner.Uid == uint32(os.Getuid()) && info.Mode().Perm()&0o077 == 0 &&
		(info.IsDir() || owner.Nlink == 1)
}
