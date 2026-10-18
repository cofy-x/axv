//go:build linux || darwin

package artifactcache

import (
	"os"
	"syscall"
	"testing"
)

type foreignInfo struct{ os.FileInfo }

func (foreignInfo) Sys() any { return &syscall.Stat_t{Uid: uint32(os.Getuid() + 1), Nlink: 1} }
func TestOwnershipChecksRejectForeignUser(t *testing.T) {
	dir := t.TempDir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if owned(foreignInfo{info}) || privateDirectory(foreignInfo{info}) == nil {
		t.Fatal("foreign user's cache accepted")
	}
}
