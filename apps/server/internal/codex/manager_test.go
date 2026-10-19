package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	profile, err := os.MkdirTemp("", "axv-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(profile) })
	return testManagerWithPaths(t, dir, profile)
}

func testManagerWithPaths(t *testing.T, dir, profile string) *Manager {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m, err := New(store, Options{DataDir: dir, ProfileHome: profile, WorkspaceDir: filepath.Join(dir, "workspace"), ConfigPath: filepath.Join(profile, "config.json"), TmuxSocket: "axv-test-" + instanceID("test", dir)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

func TestHostEndpointsRejectAccessBeforeInspection(t *testing.T) {
	m := testManager(t)
	m.probe = func(context.Context) Info { t.Fatal("rejected access probed host"); return Info{} }
	m.run = func(context.Context, string, ...string) (string, error) {
		t.Fatal("rejected access executed process")
		return "", nil
	}
	mux := http.NewServeMux()
	m.Register(mux)
	for _, tc := range []struct{ name, method, path, peer, host, origin string }{
		{"remote info", "GET", "/api/host/codex/info", "192.0.2.1:44", "localhost:3001", "http://localhost:3001"},
		{"DNS rebinding", "GET", "/api/host/codex/info", "127.0.0.1:44", "evil.test:3001", "http://evil.test:3001"},
		{"foreign list origin", "GET", "/api/host/codex/sessions", "127.0.0.1:44", "localhost:3001", "http://localhost:3002"},
		{"remote recent projects", "GET", "/api/host/codex/projects", "192.0.2.1:44", "localhost:3001", "http://localhost:3001"},
		{"missing metadata origin", "PATCH", "/api/host/codex/sessions/id", "127.0.0.1:44", "localhost:3001", ""},
		{"foreign metadata origin", "PATCH", "/api/host/codex/sessions/id", "127.0.0.1:44", "localhost:3001", "http://localhost:3002"},
		{"missing create origin", "POST", "/api/host/codex/sessions", "127.0.0.1:44", "localhost:3001", ""},
		{"missing resume origin", "POST", "/api/host/codex/sessions/id/resume", "127.0.0.1:44", "localhost:3001", ""},
		{"missing end origin", "POST", "/api/host/codex/sessions/id/runs/run/end", "127.0.0.1:44", "localhost:3001", ""},
		{"missing attach origin", "GET", "/api/host/codex/sessions/id/runs/run/terminal", "127.0.0.1:44", "localhost:3001", ""},
		{"HTTPS mismatch", "POST", "/api/host/codex/sessions", "127.0.0.1:44", "localhost:3001", "https://localhost:3001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			r.RemoteAddr, r.Host = tc.peer, tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			r.Header.Set("X-Forwarded-Host", "localhost:3001")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 403 || !strings.Contains(w.Body.String(), "host_forbidden") {
				t.Fatalf("unexpected response %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestInstanceNamespace(t *testing.T) {
	namespace := instanceID("database", "/a")
	if namespace != "d55c38d8b58b0998ead51c9b" {
		t.Fatalf("socket identity changed: %q", namespace)
	}
	if namespace == instanceID("database", "/b") || namespace == instanceID("other", "/a") {
		t.Fatal("socket identity is unstable or shared")
	}
}
