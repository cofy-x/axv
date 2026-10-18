package hostdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalDirectoryClassifiesFailures(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		kind       ErrorKind
		code       string
	}{
		{"empty", "", InvalidPath, "invalid_directory"},
		{"relative", "documents", InvalidPath, "invalid_directory"},
		{"oversized", home + strings.Repeat("x", 4096), InvalidPath, "invalid_directory"},
		{"nul", home + "\x00", InvalidPath, "invalid_directory"},
		{"invalid UTF-8", home + string([]byte{0xff}), InvalidPath, "invalid_directory"},
		{"missing", filepath.Join(home, "missing"), Unavailable, "directory_unavailable"},
		{"file", file, NotDirectory, "invalid_directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CanonicalDirectory(tc.path)
			var directoryError *Error
			if !errors.As(err, &directoryError) || directoryError.Kind != tc.kind || directoryError.Code != tc.code || directoryError.Message == "" {
				t.Fatalf("unexpected directory error: %#v", err)
			}
		})
	}
}

func TestListUsesCanonicalPathsAndListsOnlyDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, name := range []string{"zebra", "Alpha"} {
		if err := os.Mkdir(filepath.Join(home, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	result, err := List("")
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != canonical || result.HomePath != canonical || result.ParentPath == nil || *result.ParentPath != filepath.Dir(canonical) || result.Truncated {
		t.Fatalf("unexpected listing location: %+v", result)
	}
	if len(result.Directories) != 2 || result.Directories[0].Name != "Alpha" || result.Directories[1].Name != "zebra" {
		t.Fatalf("unexpected directory candidates: %+v", result.Directories)
	}
	for _, directory := range result.Directories {
		if directory.Path != filepath.Join(canonical, directory.Name) {
			t.Fatalf("unexpected candidate path: %+v", directory)
		}
	}
	alias := filepath.Join(home, "linked-directory")
	if err := os.Symlink(filepath.Join(home, "Alpha"), alias); err != nil {
		t.Skipf("host cannot create directory symlinks: %v", err)
	}
	result, err = List(home)
	if err != nil || len(result.Directories) != 3 || result.Directories[1].Path != filepath.Join(canonical, "linked-directory") {
		t.Fatalf("symlink candidate was not preserved: %+v %v", result, err)
	}
	result, err = List(alias)
	if err != nil || result.Path != filepath.Join(canonical, "Alpha") || result.Directories == nil || len(result.Directories) != 0 {
		t.Fatalf("navigation did not resolve directory symlink: %+v %v", result, err)
	}
}

func TestListBoundsDirectoryResults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for i := range 1001 {
		if err := os.Mkdir(filepath.Join(home, fmt.Sprintf("directory-%04d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	result, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Directories) != 1000 || !result.Truncated {
		t.Fatalf("directory limit was not preserved: count=%d truncated=%v", len(result.Directories), result.Truncated)
	}
}
