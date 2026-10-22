package providerutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyScratchRejectsUnexpectedStorageWithoutFollowingLinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	if err := os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(dir, "source.raw.copy")
	if err := os.Symlink(outside, scratch); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := RemoveCopyScratch(scratch); err == nil {
		t.Fatal("a staging symlink was accepted")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve" {
		t.Fatal("staging recovery changed the link target", err)
	}
	if err := os.Remove(scratch); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, scratch); err != nil {
		t.Fatal(err)
	}
	// Unlinking a reserved hardlink is safe: its external referent is never
	// opened or truncated, and the replacement will be a fresh inode.
	if err := RemoveCopyScratch(scratch); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve" {
		t.Fatal("staging recovery modified an external inode", err)
	}
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCopyScratch(scratch); err == nil {
		t.Fatal("a staging directory was removed")
	}
}
