//go:build linux || darwin

package logs

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func owned(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid())
}
func singleLink(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}
func openRootFile(root *os.Root, path string, flags int, mode os.FileMode) (*os.File, error) {
	return root.OpenFile(path, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK, mode)
}
func tryLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
func unlock(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
