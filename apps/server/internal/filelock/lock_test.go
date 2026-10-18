//go:build linux || darwin || windows

package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockContendsUntilCloseWithoutReplacingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordination.lock")
	if err := os.WriteFile(path, []byte("retained lock identity"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := TryLock(path)
	if err == nil {
		second.Close()
		t.Fatal("a competing owner acquired the lock")
	}
	if errors.Unwrap(err) == nil {
		t.Fatalf("contention lost its underlying platform error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := TryLock(path)
	if err != nil {
		t.Fatalf("closing the owner did not release its lock: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "retained lock identity" {
		t.Fatalf("locking replaced coordination data: %q %v", data, err)
	}
}
