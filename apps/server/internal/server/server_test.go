package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	codexhttp "github.com/cofy-x/axv/apps/server/internal/codex/httpapi"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/storage"
	"github.com/cofy-x/axv/apps/server/internal/terminal"
)

func TestRoutesRetainOriginChecksAndRejectFraming(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	terminals := terminal.New()
	defer terminals.Close()
	codexSessions, err := codex.New(store, codex.Options{DataDir: t.TempDir(), WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer codexSessions.Close()
	codexAPI := codexhttp.New(codexSessions)
	defer codexAPI.Close()
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("desktop UI"))
	})
	gateway := computegateway.NewGateway("", computegateway.GatewayOptions{})
	defer gateway.Close()
	application := http.NewServeMux()
	application.Handle("/api/compute/", gateway)
	application.Handle("/", httpapi.New(store, ui))
	handler := newHandler(application, terminals, codexAPI)
	for _, tc := range []struct {
		name, method, path, origin, contains string
		status                               int
	}{
		{"UI", "GET", "/", "", "desktop UI", http.StatusOK},
		{"UI deep link", "GET", "/files/documents", "", "desktop UI", http.StatusOK},
		{"health", "GET", "/api/health", "", `"status":"ok"`, http.StatusOK},
		{"terminal missing origin", "GET", "/api/terminal", "", "terminal_forbidden", http.StatusForbidden},
		{"terminal cross origin", "GET", "/api/terminal", "http://localhost:9999", "terminal_forbidden", http.StatusForbidden},
		{"terminal invalid size", "GET", "/api/terminal?cols=0", "http://localhost:3001", "invalid_terminal_size", http.StatusBadRequest},
		{"terminal info cross origin", "GET", "/api/terminal/info", "https://example.com", "terminal_forbidden", http.StatusForbidden},
		{"Codex info cross origin", "GET", "/api/host/codex/info", "http://localhost:9999", "host_forbidden", http.StatusForbidden},
		{"host directories cross origin", "GET", "/api/host/directories", "https://example.com", "host_forbidden", http.StatusForbidden},
		{"Codex create missing origin", "POST", "/api/host/codex/sessions", "", "host_forbidden", http.StatusForbidden},
		{"Codex end missing origin", "POST", "/api/host/codex/sessions/test/runs/test-run/end", "", "host_forbidden", http.StatusForbidden},
		{"compute unconfigured", "GET", "/api/compute/info", "", `"configured":false`, http.StatusOK},
		{"compute environment", "GET", "/api/compute/environment", "", `"configured":false`, http.StatusOK},
		{"compute environment cross origin", "GET", "/api/compute/environment", "https://example.com", "compute_forbidden", http.StatusForbidden},
		{"compute cross origin", "GET", "/api/compute/info", "https://example.com", "compute_forbidden", http.StatusForbidden},
		{"compute create missing origin", "POST", "/api/compute/machines", "", "compute_forbidden", http.StatusForbidden},
		{"compute origin query delimiter", "GET", "/api/compute/info", "http://localhost:3001?", "compute_forbidden", http.StatusForbidden},
		{"VFS cross origin", "POST", "/api/entries", "https://example.com", "invalid_origin", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("HTTP %d: %s; want %d containing %q", w.Code, w.Body.String(), tc.status, tc.contains)
			}
			if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Content-Security-Policy") != "frame-ancestors 'none'; frame-src 'self'" {
				t.Fatalf("missing anti-framing headers: %v", w.Header())
			}
		})
	}
}
