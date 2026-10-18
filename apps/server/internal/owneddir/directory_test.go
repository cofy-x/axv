package owneddir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureKeepsTheOriginalOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime")
	if err := Ensure(path, "host:first"); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(path, "host:first"); err != nil {
		t.Fatalf("the owner could not reopen its directory: %v", err)
	}
	if err := Ensure(path, "host:other"); err == nil {
		t.Fatal("another owner adopted the directory")
	}
	if err := Check(path, "host:other"); err == nil {
		t.Fatal("an ownership mismatch was accepted")
	}
	if err := Check(path, "host:first"); err != nil {
		t.Fatalf("a rejected adoption changed ownership: %v", err)
	}
}

func TestEnsureRefusesUnownedContents(t *testing.T) {
	path := t.TempDir()
	existing := filepath.Join(path, "user-data")
	if err := os.WriteFile(existing, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(path, "host:first"); err == nil {
		t.Fatal("adopted a nonempty directory without an ownership marker")
	}
	if _, err := os.Stat(filepath.Join(path, ".axv-owner")); !os.IsNotExist(err) {
		t.Fatalf("a rejected adoption published ownership: %v", err)
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("a rejected adoption changed existing data: %q %v", data, err)
	}
}

func TestEnsureRefusesSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := Ensure(link, "host:first"); err == nil {
		t.Fatal("a symlink was accepted as the owned directory")
	}
	if _, err := os.Stat(filepath.Join(real, ".axv-owner")); !os.IsNotExist(err) {
		t.Fatalf("a rejected symlink changed its target: %v", err)
	}
}
