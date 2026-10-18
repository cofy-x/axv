package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/hostdir"
)

func TestDirectorySelectionIsSeparateAndCanonical(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := filepath.Join(home, "project $'quote;#{}")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "not-a-directory"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/host/directories", nil)
	r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:44"
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	Register(mux)
	mux.ServeHTTP(w, r)
	var result struct {
		Path        string              `json:"path"`
		Directories []hostdir.Directory `json:"directories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(home)
	if w.Code != 200 || result.Path != canonical || len(result.Directories) != 1 {
		t.Fatalf("directory result: %d %+v", w.Code, result)
	}
}

func TestDirectoriesRejectHostAccess(t *testing.T) {
	// Invalid HOME would fail if rejected requests inspected the filesystem.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	mux := http.NewServeMux()
	Register(mux)
	for _, tc := range []struct{ name, peer, host, origin string }{
		{"remote peer", "192.0.2.1:44", "localhost:3001", "http://localhost:3001"},
		{"DNS rebinding", "127.0.0.1:44", "evil.test:3001", "http://evil.test:3001"},
		{"foreign origin", "127.0.0.1:44", "localhost:3001", "http://localhost:3002"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/host/directories", nil)
			r.RemoteAddr, r.Host = tc.peer, tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			r.Header.Set("X-Forwarded-Host", "localhost:3001")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "host_forbidden") {
				t.Fatalf("unexpected response %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDirectoryFailuresPreserveAPIContract(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	mux := http.NewServeMux()
	Register(mux)
	for _, tc := range []struct{ name, query, code string }{
		{"unknown parameter", "other=value", "invalid_directory"},
		{"duplicate path", "path=/a&path=/b", "invalid_directory"},
		{"relative path", "path=documents", "invalid_directory"},
		{"missing path", "path=" + url.QueryEscape(filepath.Join(home, "missing")), "directory_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/host/directories?"+tc.query, nil)
			r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:44"
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			var result struct{ Code, Error string }
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusBadRequest || result.Code != tc.code || result.Error == "" {
				t.Fatalf("unexpected response %d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("missing response headers: %v", w.Header())
			}
		})
	}
}

func TestRespondPreservesDirectoryFailureContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid path", &hostdir.Error{Kind: hostdir.InvalidPath, Code: "invalid_directory", Message: "Choose an absolute host directory path."}, 400, "invalid_directory"},
		{"unavailable", &hostdir.Error{Kind: hostdir.Unavailable, Code: "directory_unavailable", Message: "The host directory does not exist or cannot be accessed."}, 400, "directory_unavailable"},
		{"file", &hostdir.Error{Kind: hostdir.NotDirectory, Code: "invalid_directory", Message: "Choose an existing host directory, not a file."}, 400, "invalid_directory"},
		{"unreadable", &hostdir.Error{Kind: hostdir.Unreadable, Code: "directory_forbidden", Message: "The server user cannot read this directory."}, 403, "directory_forbidden"},
		{"home unavailable", &hostdir.Error{Kind: hostdir.HomeUnavailable, Code: "home_unavailable", Message: "The server user's home directory is unavailable."}, 503, "home_unavailable"},
		{"internal", errors.New("private diagnostic"), 500, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			respond(w, nil, tc.err)
			var result struct{ Code, Error string }
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || result.Code != tc.code || result.Error == "" {
				t.Fatalf("unexpected response %d %s", w.Code, w.Body.String())
			}
			if tc.status != 500 && result.Error != tc.err.Error() {
				t.Fatalf("directory error changed: %q", result.Error)
			}
			if tc.status == 500 && strings.Contains(w.Body.String(), "private diagnostic") {
				t.Fatal("internal error detail leaked to the client")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatal("missing private JSON response headers")
			}
		})
	}
}
