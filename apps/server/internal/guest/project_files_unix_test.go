//go:build !windows

package guest

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func TestProjectFilesRejectLinksAndSpecialFilesAndConfineAncestors(t *testing.T) {
	s := testProjectFileService(t)
	repo := filepath.Join(s.config.ProjectRoot, "repo")
	outside := t.TempDir()
	for _, dir := range []string{repo, filepath.Join(repo, "inside")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"root-link": repo, "repo/link": outside, "repo/internal": "inside", "repo/final": "inside/file"} {
		if err := os.Symlink(target, filepath.Join(s.config.ProjectRoot, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "inside", "file"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	scope := projectFileScope(t, s, "repo")
	in := scope
	in.Root = "root-link"
	in.RootIdentity = ""
	assertProblem(t, projectFileRequest(t, s, "inspect", in), 400, "invalid_directory")
	assertProblem(t, projectFileRequest(t, s, "read", protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: "final"}), 400, "not_file")
	assertProblem(t, projectFileRequest(t, s, "list", protocol.ProjectFileListRequest{ProjectFileScope: scope, Path: "internal"}), 400, "invalid_directory")
	if got := decodeProjectFileResult(t, projectFileRequest(t, s, "read", protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: "internal/file"})); got.Content != "inside" {
		t.Fatalf("internal ancestor symlink read = %+v", got)
	}
	for _, operation := range []string{"read", "write", "mkdir"} {
		var request any = protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: "link/file"}
		if operation == "write" {
			request = projectWriteRequest(scope, "link/file", "escaped", "")
		} else if operation == "mkdir" {
			request = protocol.ProjectFileMkdirRequest{ProjectFileScope: scope, Path: "link/folder"}
		}
		w := projectFileRequest(t, s, operation, request)
		if w.Code < 400 {
			t.Fatalf("outside ancestor accepted by %s: %s", operation, w.Body)
		}
	}
	actual, err := os.ReadFile(filepath.Join(outside, "file"))
	if err != nil || string(actual) != "outside" {
		t.Fatalf("outside data changed: %q, %v", actual, err)
	}
	fifo := filepath.Join(repo, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	assertProblem(t, projectFileRequest(t, s, "read", protocol.ProjectFileReadRequest{ProjectFileScope: scope, Path: "fifo"}), 400, "not_file")
	if time.Since(start) > time.Second {
		t.Fatal("FIFO read blocked")
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Even if the Lstat check races with FIFO replacement, the final open does
	// not wait for a writer. A final symlink is never followed by that open.
	file, err := projectFileOpen(root, "fifo")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if file, err := projectFileOpen(root, "final"); err == nil {
		file.Close()
		t.Fatal("low-level open followed final symlink")
	}
}

func TestProjectFileReplacementDoesNotModifyOutsideHardlink(t *testing.T) {
	s := testProjectFileService(t)
	scope := projectFileScope(t, s, "")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("original"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(s.config.ProjectRoot, "linked")); err != nil {
		t.Fatal(err)
	}
	decodeProjectFileResult(t, projectFileRequest(t, s, "write", projectWriteRequest(scope, "linked", "replacement", projectDigest([]byte("original")))))
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "original" {
		t.Fatalf("atomic replacement changed hardlink alias: %q, %v", data, err)
	}
}
