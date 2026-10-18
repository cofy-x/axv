//go:build !linux && !darwin && !windows

package logs

import (
	"fmt"
	"os"
)

func owned(info os.FileInfo) bool      { return false }
func singleLink(info os.FileInfo) bool { return false }
func openRootFile(root *os.Root, path string, flags int, mode os.FileMode) (*os.File, error) {
	return nil, fmt.Errorf("private profile logging is unsupported on this platform")
}
func tryLock(f *os.File) (bool, error) {
	return false, fmt.Errorf("profile log locking is unsupported on this platform")
}
func unlock(f *os.File) {}
