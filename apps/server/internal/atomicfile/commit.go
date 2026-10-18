package atomicfile

import (
	"os"
	"path/filepath"
)

// Commit flushes a completed file before publishing its final name, then syncs
// its directory. The caller retains responsibility for cleanup after a failure.
func Commit(temporary, path string) error {
	f, err := os.OpenFile(temporary, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(temporary, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
