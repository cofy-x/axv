//go:build !windows

package projectfiles

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// The persisted device/inode identity refuses a different directory occupying
// an old registered path; operations stay anchored to the opened root handle.
func rootIdentity(info fs.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", problem("unsupported", "Stable project directory identity is unavailable on this platform.")
	}
	return fmt.Sprintf("%v:%v", stat.Dev, stat.Ino), nil
}

// A racing replacement with a FIFO must not block a request before f.Stat.
func openNonblocking(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
