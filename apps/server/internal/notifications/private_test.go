//go:build darwin || linux

package notifications

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPrivateDirectoryRequiresOwnedExactPermissionsAndNoSymlink(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if !privateDirectory(directory) {
		t.Fatal("owned private directory rejected")
	}
	link := filepath.Join(t.TempDir(), "directory-link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if privateDirectory(link) {
		t.Fatal("directory symlink accepted")
	}
	for _, mode := range []os.FileMode{0o755, 0o750, 0o600, 0o500, 0o700 | os.ModeSticky} {
		if err := os.Chmod(directory, mode); err != nil {
			t.Fatal(err)
		}
		if privateDirectory(directory) {
			t.Fatalf("non-private directory mode accepted: %v", mode)
		}
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(directory, 1, -1); err != nil {
			t.Fatal(err)
		}
		defer os.Chown(directory, 0, -1)
		if privateDirectory(directory) {
			t.Fatal("another user's directory accepted")
		}
	}
}

func TestOpenPrivateAcceptsStateAndEmptyLocksAndKeepsMissingDistinct(t *testing.T) {
	for _, contents := range []string{"", "private metadata"} {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := openPrivate(path)
		if err != nil || !validatePrivateFile(path, file) {
			t.Fatalf("owned private file rejected: %v", err)
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil || string(data) != contents {
			t.Fatalf("private read = %q, %v", data, err)
		}
	}
	if file, err := openPrivate(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) || file != nil {
		t.Fatalf("missing file confused with damaged established state: %v", err)
	}
}

func TestOpenPrivateRejectsUnsafeFilesWithoutChangingThem(t *testing.T) {
	for _, kind := range []string{"public", "readonly", "writeonly", "directory", "symlink", "hardlink", "fifo", "owner"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "owner" && os.Geteuid() != 0 {
				t.Skip("changing ownership requires root")
			}
			directory := t.TempDir()
			path := filepath.Join(directory, "state.json")
			if err := os.WriteFile(path, []byte("private state"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public", "readonly", "writeonly":
				mode := map[string]os.FileMode{"public": 0o644, "readonly": 0o400, "writeonly": 0o200}[kind]
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			case "directory", "symlink", "hardlink", "fifo":
				original := filepath.Join(directory, "original")
				if err := os.Rename(path, original); err != nil {
					t.Fatal(err)
				}
				var err error
				switch kind {
				case "directory":
					err = os.Mkdir(path, 0o700)
				case "symlink":
					err = os.Symlink(original, path)
				case "hardlink":
					err = os.Link(original, path)
				case "fifo":
					err = syscall.Mkfifo(path, 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "owner":
				if err := os.Chown(path, 1, -1); err != nil {
					t.Fatal(err)
				}
				defer os.Chown(path, 0, -1)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			file, err := openPrivate(path)
			if !errors.Is(err, ErrStore) || file != nil {
				if file != nil {
					file.Close()
				}
				t.Fatalf("unsafe file accepted: %v", err)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("unsafe file was replaced or changed: %v", err)
			}
		})
	}
}

func TestValidatePrivateFileRejectsReplacedPathAndClosedDescriptors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openPrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if validatePrivateFile(path, file) || validatePrivateFile(path, nil) {
		t.Fatal("replaced path or nil descriptor accepted")
	}
	replacement, err := openPrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement.Close()
	if validatePrivateFile(path, replacement) {
		t.Fatal("closed descriptor accepted")
	}
}
