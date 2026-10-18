//go:build darwin || linux

package hostdir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalDirectoryClassifiesUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read directories without permission bits")
	}
	directory := filepath.Join(t.TempDir(), "unreadable")
	if err := os.Mkdir(directory, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0700) })
	_, err := CanonicalDirectory(directory)
	var directoryError *Error
	if !errors.As(err, &directoryError) || directoryError.Kind != Unreadable || directoryError.Code != "directory_forbidden" {
		t.Fatalf("unreadable directory was not classified: %v", err)
	}
}
