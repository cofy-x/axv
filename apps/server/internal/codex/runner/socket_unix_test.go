//go:build darwin || linux

package runner

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func shortSocketHome(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "axv-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func TestSocketPathsStayInProfileAndCleanOnlyTheirOwnRun(t *testing.T) {
	root := filepath.Join(shortSocketHome(t), "run", "codex")
	first, cleanFirst, err := createSocketPath(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanFirst)
	second, cleanSecond, err := createSocketPath(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanSecond)
	if first == second || filepath.Dir(filepath.Dir(first)) != root || filepath.Dir(filepath.Dir(second)) != root {
		t.Fatalf("socket routing is not isolated: %q, %q", first, second)
	}
	for _, directory := range []string{root, filepath.Dir(first), filepath.Dir(second)} {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("nonprivate socket directory %q: %v, %v", directory, info, err)
		}
	}
	listener, err := net.Listen("unix", second)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cleanFirst()
	if _, err := os.Stat(filepath.Dir(first)); !os.IsNotExist(err) {
		t.Fatalf("first run directory survived cleanup: %v", err)
	}
	connection, err := net.Dial("unix", second)
	if err != nil {
		t.Fatalf("cleaning one run damaged another: %v", err)
	}
	_ = connection.Close()
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("cleanup removed the shared profile root: %v", err)
	}
}

func TestSocketCleanupDoesNotDeleteReplacementDirectory(t *testing.T) {
	root := filepath.Join(shortSocketHome(t), "run", "codex")
	path, cleanup, err := createSocketPath(root)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(path)
	if err := os.Rename(directory, directory+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(directory, "other-run")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cleanup removed a directory it did not create: %v", err)
	}
}

func TestSocketRootRejectsUnsafePathsWithoutFallback(t *testing.T) {
	home := shortSocketHome(t)
	public := filepath.Join(home, "public")
	if err := os.Mkdir(public, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(public, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", "relative", public, link, file, filepath.Join(home, strings.Repeat("x", 120))} {
		t.Run(filepath.Base(root), func(t *testing.T) {
			path, cleanup, err := createSocketPath(root)
			if err == nil || path != "" || cleanup != nil {
				t.Fatalf("unsafe root accepted: %q, %v", path, err)
			}
		})
	}
	info, err := os.Stat(public)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("unsafe root was silently repaired: %v, %v", info, err)
	}
}

func TestSocketRootLengthCountsBytesAndReservesRunDirectory(t *testing.T) {
	limit := len(syscall.RawSockaddrUnix{}.Path) - 1
	rootLength := limit - socketDirectoryNameLength - len("//s")
	root := "/" + strings.Repeat("x", rootLength-1)
	if err := ValidateSocketRoot(root); err != nil {
		t.Fatalf("maximum usable root rejected: %v", err)
	}
	for _, tooLong := range []string{root + "x", "/" + strings.Repeat("界", rootLength/3+1)} {
		if err := ValidateSocketRoot(tooLong); err == nil || !strings.Contains(err.Error(), "AXV_DEV_HOME") {
			t.Fatalf("long byte path lacks actionable error: %v", err)
		}
	}
}
