//go:build linux || darwin

package logs

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDirectoryHandleKeepsWritesInsideVerifiedDirectory(t *testing.T) {
	home := t.TempDir()
	original := filepath.Join(home, "logs")
	moved := filepath.Join(home, "moved")
	outside := t.TempDir()
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(original)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, original); err != nil {
		t.Fatal(err)
	}
	f, err := checkedFile(root, "daemon.log", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("owned\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := os.Stat(filepath.Join(outside, "daemon.log")); !os.IsNotExist(err) {
		t.Fatal("directory replacement redirected log writes")
	}
	if data, err := os.ReadFile(filepath.Join(moved, "daemon.log")); err != nil || string(data) != "owned\n" {
		t.Fatalf("anchored write missing: %q %v", data, err)
	}
}

func TestNonblockingOpenRejectsRacedFIFO(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := unix.Mkfifo(filepath.Join(dir, "daemon.log"), 0600); err != nil {
		t.Fatal(err)
	}
	if f, err := openRootFile(root, "daemon.log", os.O_WRONLY|os.O_APPEND, 0600); err == nil {
		f.Close()
		t.Fatal("FIFO open unexpectedly succeeded without a reader")
	}
}
