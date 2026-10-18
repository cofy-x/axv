package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCommitPublishesTheCompletedArtifactAndPreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	temporary, path := filepath.Join(dir, "partial"), filepath.Join(dir, "artifact")
	if err := os.WriteFile(temporary, []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("previous"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(temporary, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "complete" {
		t.Fatalf("published artifact = %q, %v", data, err)
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatalf("staging path still exists: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("published permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestFailedCommitPreservesStagingAndExistingDestination(t *testing.T) {
	dir := t.TempDir()
	temporary, path := filepath.Join(dir, "partial"), filepath.Join(dir, "destination")
	if err := os.WriteFile(temporary, []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(path, "previous")
	if err := os.WriteFile(previous, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Commit(temporary, path); err == nil {
		t.Fatal("replaced a populated directory")
	}
	for filename, want := range map[string]string{temporary: "complete", previous: "keep"} {
		data, err := os.ReadFile(filename)
		if err != nil || string(data) != want {
			t.Fatalf("contents of %s after failure = %q, %v", filename, data, err)
		}
	}
}
