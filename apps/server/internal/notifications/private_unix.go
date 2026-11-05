//go:build darwin || linux

package notifications

import (
	"os"
	"syscall"
)

func privateOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func privateDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode().Perm() == 0o700 &&
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 && privateOwner(info)
}

func privateRegular(info os.FileInfo) bool {
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !privateOwner(info) {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

// validatePrivateFile also applies to an acquired lock descriptor. Comparing
// both identities prevents a checked path from being replaced during opening.
func validatePrivateFile(path string, file *os.File) bool {
	if file == nil {
		return false
	}
	entry, err := os.Lstat(path)
	if err != nil || !privateRegular(entry) {
		return false
	}
	opened, err := file.Stat()
	return err == nil && privateRegular(opened) && os.SameFile(entry, opened)
}

// openPrivate reads secret-bearing metadata without following a replacement
// symlink or blocking on a replacement FIFO. A missing file remains distinct
// from unsafe established state for the caller's initial-creation policy.
func openPrivate(path string) (*os.File, error) {
	entry, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	if err != nil || !privateRegular(entry) {
		return nil, ErrStore
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrStore
	}
	if !validatePrivateFile(path, file) {
		_ = file.Close()
		return nil, ErrStore
	}
	return file, nil
}
