package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	"github.com/cofy-x/axv/apps/server/internal/hostdir"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func testHandler(t *testing.T) (*Handler, *codex.Manager) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager, err := codex.New(store, codex.Options{DataDir: dir, ProfileHome: dir, WorkspaceDir: filepath.Join(dir, "workspace")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	handler := New(manager)
	t.Cleanup(handler.Close)
	return handler, manager
}

type observedBody struct {
	io.ReadCloser
	entered chan struct{}
	once    sync.Once
}

func (b *observedBody) Read(data []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	return b.ReadCloser.Read(data)
}

func TestCloseDrainsIncompleteRequestWithoutStoppingService(t *testing.T) {
	handler, manager := testHandler(t)
	mux := http.NewServeMux()
	handler.Register(mux)
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = &observedBody{ReadCloser: r.Body, entered: entered}
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "POST /api/host/codex/sessions HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{", server.Listener.Addr(), server.URL); err != nil {
		t.Fatal(err)
	}
	<-entered
	closed := make(chan struct{})
	go func() { handler.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("adapter shutdown did not drain a partial request body")
	}
	if _, err := manager.Projects(context.Background()); err != nil {
		t.Fatalf("adapter shutdown stopped the service: %v", err)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/host/codex/projects", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "server_stopping") {
		t.Fatalf("adapter admitted work after shutdown: %d %s", w.Code, w.Body.String())
	}
}

func TestServiceShutdownStopsAdapterAdmission(t *testing.T) {
	handler, manager := testHandler(t)
	mux := http.NewServeMux()
	handler.Register(mux)
	manager.Close()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/host/codex/projects", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "server_stopping") {
		t.Fatalf("adapter admitted work after service shutdown: %d %s", w.Code, w.Body.String())
	}
}

func TestDecodeRequiresOneStrictBoundedJSONObject(t *testing.T) {
	for _, tc := range []struct {
		name, content, body string
		status              int
	}{
		{"valid", "application/json", `{"name":"fixture"}`, 200},
		{"charset", "application/json; charset=utf-8", `{"name":"fixture"}`, 200},
		{"wrong content type", "text/plain", `{}`, 415},
		{"unknown field", "application/json", `{"unknown":true}`, 400},
		{"trailing object", "application/json", `{} {}`, 400},
		{"invalid JSON", "application/json", `{`, 400},
		{"oversized body", "application/json", `{"name":"` + strings.Repeat("x", 16<<10) + `"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.content)
			w := httptest.NewRecorder()
			var value struct {
				Name string `json:"name"`
			}
			ok := decode(w, r, &value)
			if ok != (tc.status == 200) || w.Code != tc.status {
				t.Fatalf("decode=%v status=%d body=%s", ok, w.Code, w.Body.String())
			}
		})
	}
}

func TestRespondMapsDomainAndStorageErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid", &codex.Error{Kind: codex.Invalid, Code: "invalid_session", Message: "invalid"}, 400, "invalid_session"},
		{"conflict", &codex.Error{Kind: codex.Conflict, Code: "run_changed", Message: "changed"}, 409, "run_changed"},
		{"unavailable", &codex.Error{Kind: codex.Unavailable, Code: "session_unavailable", Message: "unavailable"}, 503, "session_unavailable"},
		{"not found", &storage.Error{Code: "not_found", Message: "missing"}, 404, "not_found"},
		{"metadata conflict", &storage.Error{Code: "metadata_conflict", Message: "changed"}, 409, "metadata_conflict"},
		{"invalid directory", &hostdir.Error{Kind: hostdir.InvalidPath, Code: "invalid_directory", Message: "invalid"}, 400, "invalid_directory"},
		{"unavailable directory", &hostdir.Error{Kind: hostdir.Unavailable, Code: "directory_unavailable", Message: "unavailable"}, 400, "directory_unavailable"},
		{"directory file", &hostdir.Error{Kind: hostdir.NotDirectory, Code: "invalid_directory", Message: "file"}, 400, "invalid_directory"},
		{"unreadable directory", &hostdir.Error{Kind: hostdir.Unreadable, Code: "directory_forbidden", Message: "unreadable"}, 403, "directory_forbidden"},
		{"internal", errors.New("private diagnostic"), 500, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			respond(w, nil, tc.err)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatal("missing private JSON response headers")
			}
			if tc.status == http.StatusInternalServerError && strings.Contains(w.Body.String(), "private diagnostic") {
				t.Fatal("internal error detail leaked to the client")
			}
		})
	}
}
