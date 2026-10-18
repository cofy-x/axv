package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteReplacesCompleteContentsAndPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("previous contents"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("published contents = %q, error = %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("published permissions = %o, want 600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "state" {
		t.Fatalf("write left temporary files: %v, error = %v", entries, err)
	}
}

func TestFailedPublicationPreservesExistingFilesAndCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(path, "previous")
	if err := os.WriteFile(previous, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// Replacing this populated directory must fail after the new file is written.
	if err := Write(path, []byte("replacement"), 0600); err == nil {
		t.Fatal("replaced a populated directory")
	}
	data, err := os.ReadFile(previous)
	if err != nil || string(data) != "keep" {
		t.Fatalf("previous file changed after failure: %q, error = %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "state" {
		t.Fatalf("failed write left temporary files: %v, error = %v", entries, err)
	}
}

func TestFailureBeforePublicationPreservesPreviousContents(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions enforced for an unprivileged user")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
	if err := Write(path, []byte("replacement"), 0600); err == nil {
		t.Fatal("published a file in a non-writable directory")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "previous" {
		t.Fatalf("previous contents changed after failure: %q, error = %v", data, err)
	}
}
