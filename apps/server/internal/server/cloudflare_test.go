package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfhttpapi "github.com/cofy-x/axv/apps/server/internal/cloudflare/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/codex"
	codexhttp "github.com/cofy-x/axv/apps/server/internal/codex/httpapi"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	preparehttp "github.com/cofy-x/axv/apps/server/internal/compute/prepare/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/extensions"
	"github.com/cofy-x/axv/apps/server/internal/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
	"github.com/cofy-x/axv/apps/server/internal/projectfiles"
	"github.com/cofy-x/axv/apps/server/internal/projectpreview"
	"github.com/cofy-x/axv/apps/server/internal/storage"
	"github.com/cofy-x/axv/apps/server/internal/terminal"
)

func TestCloudflareControlsRequireRawLocalAccessAndExactOrigin(t *testing.T) {
	c, services, _ := cloudflareControllerFixture(t)
	for _, tc := range []struct{ peer, host, origin string }{
		{"127.0.0.1:1234", "localhost:3001", ""},
		{"127.0.0.1:1234", "localhost:3001", "https://attacker.example.com"},
		{"192.0.2.1:1234", "localhost:3001", "http://localhost:3001"},
		{"127.0.0.1:1234", "axv.example.com", "https://axv.example.com"},
	} {
		r := httptest.NewRequest(http.MethodPut, "http://"+tc.host+"/api/cloudflare", strings.NewReader(`{"enabled":true}`))
		r.RemoteAddr = tc.peer
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("X-Forwarded-Host", "localhost:3001")
		if tc.host == "axv.example.com" {
			var err error
			r, err = localaccess.WithAuthenticatedTunnel(r, tc.origin)
			if err != nil {
				t.Fatal(err)
			}
		}
		w := httptest.NewRecorder()
		cfhttpapi.DesktopSettings(c).ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "cloudflare_local_only") || len(*services) != 0 {
			t.Fatalf("remote control bypassed local identity: %d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{"", `null`, `[]`, `{}`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":false,"unknown":true}`, `{"enabled":false} {}`, `{"enabled":false`, strings.Repeat(" ", 4096) + `{"enabled":true}`} {
		r := httptest.NewRequest(http.MethodPut, "http://localhost:3001/api/cloudflare", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://localhost:3001")
		w := httptest.NewRecorder()
		cfhttpapi.DesktopSettings(c).ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_cloudflare_request") || len(*services) != 0 {
			t.Fatalf("ambiguous body mutated ingress: %q: %d %s", body, w.Code, w.Body.String())
		}
	}
}

func TestCloudflareLocalStatusAndSwitchNeverExposeAccessIdentity(t *testing.T) {
	c, services, _ := cloudflareControllerFixture(t)
	request := func(method, path, body string, local bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:3001"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://localhost:3001")
		if !local {
			r.Host = "axv.example.com"
			r.Header.Set("Origin", "https://axv.example.com")
			var err error
			r, err = localaccess.WithAuthenticatedTunnel(r, "https://axv.example.com")
			if err != nil {
				t.Fatal(err)
			}
		}
		w := httptest.NewRecorder()
		if method == http.MethodGet {
			cfhttpapi.DesktopStatus("instance-cf", c.Status).ServeHTTP(w, r)
		} else {
			cfhttpapi.DesktopSettings(c).ServeHTTP(w, r)
		}
		return w
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		w := request(method, "/api/cloudflare/status", `{"enabled":true}`, false)
		if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "private") || len(*services) != 0 {
			t.Fatalf("authenticated remote accessed local settings: %d %s", w.Code, w.Body.String())
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		w := request(method, "/api/cloudflare", `{"enabled":true}`, true)
		var fields map[string]any
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &fields) != nil || fields["configured"] != true || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "issuer") || strings.Contains(w.Body.String(), "audience") || strings.Contains(w.Body.String(), "owner") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unsafe local response: %d %s", w.Code, w.Body.String())
		}
		if method == http.MethodGet && (fields["instanceId"] != "instance-cf" || fields["state"] != "disabled" || fields["listen"] != "127.0.0.1:3002") {
			t.Fatalf("local status lost identity or listener settings: %s", w.Body.String())
		}
		if method == http.MethodPut && (fields["enabled"] != true || fields["state"] != "listening" || fields["listen"] != nil) {
			t.Fatalf("control response lost safe ingress state: %s", w.Body.String())
		}
	}
	if err := os.WriteFile(filepath.Join(c.home, "config.json"), []byte(`{"cloudflare":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	w := request(http.MethodPut, "/api/cloudflare", `{"enabled":false}`, true)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "cloudflare_config_failed") || (*services)[0].closeCount() != 0 {
		t.Fatalf("failed write changed ingress: %d %s", w.Code, w.Body.String())
	}
}

func TestAuthenticatedCloudflareContextSharesFeaturesAndRetainsLocalBoundaries(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	terminals := terminal.New()
	defer terminals.Close()
	manager, err := codex.New(store, codex.Options{DataDir: t.TempDir(), WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	agents := codexhttp.New(manager)
	defer agents.Close()
	gateway := computegateway.NewGateway("", computegateway.GatewayOptions{})
	defer gateway.Close()
	previews := projectpreview.New(context.Background(), projectpreview.GatewayResolver(gateway))
	defer previews.Close()
	extensionStore, err := extensions.Open(filepath.Join(t.TempDir(), "extensions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer extensionStore.Close()
	application := http.NewServeMux()
	projectfiles.Register(application, store)
	previews.Register(application)
	extensions.New(extensionStore).Register(application)
	application.Handle("/api/compute/", gateway)
	application.Handle("/api/compute/preparation/", preparehttp.Unavailable("desktop-test", errors.New("fixture unavailable")))
	c, _, _ := cloudflareControllerFixture(t)
	application.Handle("GET /api/cloudflare/status", cfhttpapi.DesktopStatus("desktop-test", c.Status))
	application.Handle("PUT /api/cloudflare", cfhttpapi.DesktopSettings(c))
	application.Handle("GET /api/remote/status", remoteStatusHandler("desktop-test", func() remoteSnapshot {
		t.Fatal("tunnel request reached local remote status")
		return remoteSnapshot{}
	}))
	application.Handle("/", httpapi.New(store, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("desktop UI"))
	})))
	shared := newHandler(application, terminals, agents)
	request := func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://axv.example.com"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-AXV-Host-ID", "original-host")
		r.Header.Set("Origin", "https://axv.example.com")
		if authenticated {
			r, err = localaccess.WithAuthenticatedTunnel(r, "https://axv.example.com")
			if err != nil {
				t.Fatal(err)
			}
			if localaccess.Request(r, false) || !localaccess.AllowRequest(r, true) || r.TLS != nil || r.RemoteAddr != "127.0.0.1:12345" {
				t.Fatal("tunnel authority changed transport facts or became local")
			}
		}
		w := httptest.NewRecorder()
		shared.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct{ path, contains string }{
		{"/", "desktop UI"}, {"/api/bootstrap?clientId=cf-browser", `"databaseId"`},
		{"/api/projects", `"projects"`}, {"/api/terminal/info", `"supported"`},
		{"/api/host/codex/info", `"supported"`}, {"/api/compute/info", `"configured":false`},
		{"/api/extensions", `"apps"`}, {"/api/project-previews/project-one", `"supported":false`},
	} {
		w := request(http.MethodGet, tc.path, "", true)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("authenticated %s: HTTP %d: %s", tc.path, w.Code, w.Body.String())
		}
	}
	body := `{"parentId":"documents","name":"Cloudflare document.txt","kind":"file","content":"explicit remote snapshot"}`
	if w := request(http.MethodPost, "/api/entries", body, true); w.Code != http.StatusCreated {
		t.Fatalf("authorized document save failed: %d %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest(http.MethodGet, "http://localhost:3001/api/entries", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	shared.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Cloudflare document.txt") {
		t.Fatalf("local desktop did not see committed snapshot: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ method, path, code string }{
		{http.MethodGet, "/api/cloudflare/status", "cloudflare_local_only"},
		{http.MethodPut, "/api/cloudflare", "cloudflare_local_only"},
		{http.MethodGet, "/api/remote/status", "remote_local_only"},
		{http.MethodGet, "/api/compute/preparation/check", "preparation_forbidden"},
		{http.MethodPost, "/api/extensions/install", "extension_forbidden"},
		{http.MethodPut, "/api/extensions/local.app", "extension_forbidden"},
		{http.MethodDelete, "/api/extensions/local.app/data", "extension_forbidden"},
	} {
		w := request(tc.method, tc.path, `{}`, true)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("tunnel bypassed local-only operation %s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if r := httptest.NewRequest(tc.method, tc.path, nil); !remoteLocalOnlyRoute(r) || remoteRoute(r) {
			t.Fatalf("local-only operation admitted to tunnel route policy: %s", tc.path)
		}
	}
	if w := request(http.MethodPost, "/api/project-previews/project-one", `{}`, true); w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), "preview_remote_unsupported") {
		t.Fatalf("tunnel created a local preview listener: %d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, "/api/compute/machines/vm-one/ssh", "", true); w.Code != http.StatusNotFound {
		t.Fatalf("tunnel exposed private guest SSH: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/runtime", "/v1/machines", "/api/future/capability"} {
		if remoteRoute(httptest.NewRequest(http.MethodGet, path, nil)) {
			t.Fatalf("private API admitted to remote policy: %s", path)
		}
	}
	for _, path := range []string{"/api/terminal/info", "/api/host/codex/info", "/api/compute/info"} {
		if w := request(http.MethodGet, path, "", false); w.Code != http.StatusForbidden {
			t.Fatalf("public Host created authority without authentication: %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
