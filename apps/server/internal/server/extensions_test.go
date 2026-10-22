package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	codexhttp "github.com/cofy-x/axv/apps/server/internal/codex/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/extensions"
	"github.com/cofy-x/axv/apps/server/internal/storage"
	"github.com/cofy-x/axv/apps/server/internal/terminal"
)

func TestOpaqueExtensionRequestsCannotReachAnyDesktopAPI(t *testing.T) {
	reached := false
	s, err := storage.Open(filepath.Join(t.TempDir(), "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	manager, err := codex.New(s, codex.Options{DataDir: t.TempDir(), WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	agents := codexhttp.New(manager)
	defer agents.Close()
	terminals := terminal.New()
	defer terminals.Close()
	app := http.NewServeMux()
	app.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true; w.WriteHeader(200) }))
	extensionsStore, err := extensions.Open(filepath.Join(t.TempDir(), "extensions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer extensionsStore.Close()
	extensions.New(extensionsStore).Register(app)
	h := newHandler(app, terminals, agents)
	for _, path := range []string{"/api/runtime", "/api/entries", "/api/terminal/info", "/api/host/codex/info", "/api/extensions", "/api/project-previews/project-one"} {
		for _, origin := range []string{"null", ""} {
			r := httptest.NewRequest("GET", "http://localhost:3001"+path, nil)
			r.RemoteAddr = "127.0.0.1:1234"
			if origin != "" {
				r.Header.Set("Origin", origin)
			} else {
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 || !strings.Contains(w.Body.String(), "sandbox_request_forbidden") || reached {
				t.Fatalf("opaque API reached %s: %d %s", path, w.Code, w.Body.String())
			}
		}
	}
	for _, site := range []string{"same-site", "cross-site"} {
		r := httptest.NewRequest("GET", "http://localhost:3001/api/entries", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Sec-Fetch-Site", site)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || reached {
			t.Fatalf("preview-origin API read was accepted: %s %d", site, w.Code)
		}
	}
	// Content alone may be framed. Even direct navigation receives a CSP
	// sandbox that strips same-origin privileges from the response itself.
	packageBody := `{"package":{"manifest":{"schemaVersion":1,"id":"local.example","title":"Example","version":"1","apiVersion":1,"capabilities":["storage"]},"html":"<!doctype html><title>Example</title>"}}`
	r := httptest.NewRequest("POST", "http://localhost:3001/api/extensions/install", strings.NewReader(packageBody))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "http://localhost:3001")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(extensions.StoreHeader, extensionsStore.ID())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	var installed struct {
		App struct {
			Digest string `json:"digest"`
		} `json:"app"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "http://localhost:3001/api/extensions/local.example/content/"+extensionsStore.ID()+"/"+installed.App.Digest, nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Security-Policy") != extensions.ContentPolicy || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("content sandbox was overridden: %d %v", w.Code, w.Header())
	}
}

func TestRemoteExtensionRoutesAreExplicit(t *testing.T) {
	for _, tc := range []struct {
		method, path       string
		allowed, localOnly bool
	}{
		{"GET", "/api/extensions", true, false},
		{"GET", "/api/extensions/local.app/data", true, false},
		{"GET", "/api/extensions/local.app/content/store-id/digest", true, false},
		{"POST", "/api/extensions/local.app/sessions", true, false},
		{"POST", "/api/extensions/local.app/sessions/key/rpc", true, false},
		{"DELETE", "/api/extensions/local.app/sessions/key", true, false},
		{"POST", "/api/extensions/install", false, true},
		{"PUT", "/api/extensions/local.app", false, true},
		{"DELETE", "/api/extensions/local.app", false, true},
		{"DELETE", "/api/extensions/local.app/data", false, true},
		{"GET", "/api/extensions/local.app/future", false, false},
		{"POST", "/api/extensions/local.app/content/store-id/digest", false, false},
		{"GET", "/api/extensions/local.app/content/digest", false, false},
		{"PUT", "/api/extensions/local.app/sessions/key/rpc", false, false},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(""))
		if got := remoteRoute(r); got != tc.allowed {
			t.Errorf("remote %s %s: %v", tc.method, tc.path, got)
		}
		if got := remoteLocalOnlyRoute(r); got != tc.localOnly {
			t.Errorf("local-only %s %s: %v", tc.method, tc.path, got)
		}
	}
}
