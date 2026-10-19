//go:build linux

package guest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"golang.org/x/sys/unix"
)

func TestProjectRootIdentityDistinguishesReusedInodes(t *testing.T) {
	filesystem := unix.Statfs_t{Fsid: unix.Fsid{Val: [2]int32{1234, 5678}}}
	// ext4's export handle includes an inode and its generation, making two
	// incarnations distinct even if their inode and creation timestamp match.
	first, err := projectFileIdentityHandle(unix.NewFileHandle(1, []byte{123, 0, 0, 0, 1, 0, 0, 0}), filesystem)
	if err != nil {
		t.Fatal(err)
	}
	second, err := projectFileIdentityHandle(unix.NewFileHandle(1, []byte{123, 0, 0, 0, 2, 0, 0, 0}), filesystem)
	if err != nil || first == second {
		t.Fatalf("reused inode retained old root identity: %q, %q, %v", first, second, err)
	}
	otherType, err := projectFileIdentityHandle(unix.NewFileHandle(2, []byte{123, 0, 0, 0, 1, 0, 0, 0}), filesystem)
	if err != nil || first == otherType {
		t.Fatalf("different handle type retained identity: %q, %q, %v", first, otherType, err)
	}
	filesystem.Fsid.Val[0]++
	otherFilesystem, err := projectFileIdentityHandle(unix.NewFileHandle(1, []byte{123, 0, 0, 0, 1, 0, 0, 0}), filesystem)
	if err != nil || first == otherFilesystem {
		t.Fatalf("replacement filesystem retained root identity: %q, %q, %v", first, otherFilesystem, err)
	}
	if _, err := projectFileIdentityHandle(unix.NewFileHandle(1, nil), filesystem); err == nil {
		t.Fatal("missing filesystem handle accepted")
	}
	largest, err := projectFileIdentityHandle(unix.NewFileHandle(-2147483648, make([]byte, 128)), filesystem)
	if err != nil || len(largest) > 128 {
		t.Fatalf("identity exceeds public wire limit: %d bytes, %v", len(largest), err)
	}
}

func TestProjectFilesRejectOldBindingAfterRootInodeReuse(t *testing.T) {
	s := testProjectFileService(t)
	name := filepath.Join(s.config.ProjectRoot, "repo")
	if err := os.Mkdir(name, 0700); err != nil {
		t.Fatal(err)
	}
	scope := projectFileScope(t, s, "repo")
	var original unix.Stat_t
	if err := unix.Stat(name, &original); err != nil {
		t.Fatal(err)
	}
	// Filesystems may recycle a deleted directory's inode after just a handful
	// of creations. Keep the real regression bounded for filesystems that do not.
	for attempt := 0; attempt < 1000; attempt++ {
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
		var replacement unix.Stat_t
		if err := unix.Stat(name, &replacement); err != nil {
			t.Fatal(err)
		}
		if replacement.Ino != original.Ino {
			continue
		}
		assertProblem(t, projectFileRequest(t, s, "list", protocol.ProjectFileListRequest{ProjectFileScope: scope}), 409, "project_changed")
		assertProblem(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "unrelated", "old request", "")), 409, "project_changed")
		if _, err := os.Stat(filepath.Join(name, "unrelated")); !os.IsNotExist(err) {
			t.Fatalf("old binding wrote into replacement project: %v", err)
		}
		return
	}
	t.Skip("filesystem did not reuse the directory inode within 1000 creations")
}

func TestProjectRootIdentitySurvivesDirectoryChanges(t *testing.T) {
	name := t.TempDir()
	root, err := os.OpenRoot(name)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	first, err := projectFileRootIdentity(root, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(name, "new-child"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, 0750); err != nil {
		t.Fatal(err)
	}
	second, err := projectFileRootIdentity(root, info)
	if err != nil || first != second {
		t.Fatalf("directory modification changed persistent identity: %q, %q, %v", first, second, err)
	}
}
