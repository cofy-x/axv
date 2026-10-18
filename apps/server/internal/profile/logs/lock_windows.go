package logs

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func owned(info os.FileInfo) bool      { return true }
func singleLink(info os.FileInfo) bool { return true }
func openRootFile(root *os.Root, path string, flags int, mode os.FileMode) (*os.File, error) {
	return root.OpenFile(path, flags, mode)
}
func tryLock(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}
func unlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
