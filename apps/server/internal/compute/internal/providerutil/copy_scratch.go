package providerutil

import (
	"fmt"
	"os"
)

// RemoveCopyScratch releases one reserved staging filename in a verified,
// pinned copy directory. It never follows links or scans unrelated files.
// Callers must hold the copy operation's resource lease before using it.
func RemoveCopyScratch(name string) error {
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("copy staging must be a regular owned file; preserve unexpected storage for inspection")
	}
	return os.Remove(name)
}
