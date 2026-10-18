// Package owneddir protects AXV-managed directories with explicit ownership markers.
// Marker ownership identifies an AXV resource; it does not change operating-system ownership.
package owneddir

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
)

// Ensure initializes an empty directory with an AXV ownership marker or verifies its existing marker.
// It refuses symlink directories, other owners and nonempty directories without a marker.
func Ensure(path, owner string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("runtime path is not a real directory: %s", path)
	}
	marker := filepath.Join(path, ".axv-owner")
	data, err := os.ReadFile(marker)
	if err == nil {
		if string(data) != owner {
			return fmt.Errorf("runtime directory belongs to another AXV host")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("refusing to adopt a non-empty runtime directory: %s", path)
	}
	return atomicfile.Write(marker, []byte(owner), 0600)
}

// Check verifies the existing AXV ownership marker without creating resources.
func Check(path, owner string) error {
	b, err := os.ReadFile(filepath.Join(path, ".axv-owner"))
	if err != nil {
		return err
	}
	if string(b) != owner {
		return fmt.Errorf("runtime resource has a different owner")
	}
	return nil
}
