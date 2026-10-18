//go:build linux || darwin

package guest

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListenerPreservesExistingFilesAndLiveSockets(t *testing.T) {
	path := shortSocketPath(t)
	for _, address := range []string{"tcp:127.0.0.1:0", "unix:relative.sock"} {
		if listener, err := listenGuest(address); err == nil {
			listener.Close()
			t.Fatalf("accepted unsupported listener %q", address)
		}
	}
	original := []byte("an existing user file")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if listener, err := listenGuest("unix:" + path); err == nil {
		listener.Close()
		t.Fatal("replaced an existing regular file")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("existing file changed: %q, %v", got, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	active, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if listener, err := listenGuest("unix:" + path); err == nil {
		listener.Close()
		t.Fatal("replaced another service's live socket")
	}
	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("original listener was lost: %v", err)
	}
	connection.Close()
}

func TestListenerReplacesOnlyStaleSocketWithPrivateListener(t *testing.T) {
	path := shortSocketPath(t)
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Net: "unix", Name: path})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err = stale.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := listenGuest("unix:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatalf("replacement socket is not private: %v, %v", info, err)
	}
	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	// Avoid t.TempDir's test-name component while retaining the launcher's
	// repository-local TMPDIR and invocation-specific cleanup ownership.
	dir, err := os.MkdirTemp(os.TempDir(), "sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join(dir, "guest.sock")
	if len(path) >= 104 {
		t.Skip("the configured test root exceeds the Unix socket path limit")
	}
	return path
}
