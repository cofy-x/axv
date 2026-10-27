package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	codexhttp "github.com/cofy-x/axv/apps/server/internal/codex/httpapi"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	projectfileshttp "github.com/cofy-x/axv/apps/server/internal/projectfiles/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/remoteaccess"
	"github.com/cofy-x/axv/apps/server/internal/storage"
	"github.com/cofy-x/axv/apps/server/internal/tailnet"
	"github.com/cofy-x/axv/apps/server/internal/terminal"
)

func TestAuthenticatedRemoteDesktopSharesFeatureHandlers(t *testing.T) {
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
	application := http.NewServeMux()
	projectfileshttp.Register(application, store)
	application.Handle("/api/compute/", gateway)
	application.Handle("/", httpapi.New(store, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("desktop UI"))
	})))
	shared := newHandler(application, terminals, agents)
	owner := "123"
	lookups := 0
	middleware, err := remoteaccess.NewMiddleware(remoteaccess.MiddlewareOptions{
		Origin: "https://workstation.example.ts.net", OwnerUserID: "123",
		AllowRoute: remoteRoute, LocalOnlyRoute: remoteLocalOnlyRoute,
		WhoIs: func(_ context.Context, address string) (remoteaccess.Identity, error) {
			lookups++
			if address != "100.64.1.2:12345" {
				t.Fatalf("identity lookup used a rewritten address: %s", address)
			}
			return remoteaccess.Identity{UserID: owner, NodeID: "peer-node"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer middleware.Close()
	remote := middleware.Wrap(shared)
	request := func(method, path, origin, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://workstation.example.ts.net"+path, strings.NewReader(body))
		r.RemoteAddr, r.TLS = "100.64.1.2:12345", &tls.ConnectionState{}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		remote.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		path, contains string
	}{
		{"/", "desktop UI"},
		{"/api/health", `"status":"ok"`},
		{"/api/bootstrap?clientId=remote-browser", `"databaseId"`},
		{"/api/projects", `"projects"`},
		{"/api/terminal/info", `"supported"`},
		{"/api/compute/info", `"configured":false`},
	} {
		w := request("GET", tc.path, "", "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: HTTP %d: %s", tc.path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/remote/status", "/api/compute/preparation/check"} {
		w := request("GET", path, "", "")
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "remote_local_only") {
			t.Fatalf("local operation %s: HTTP %d: %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/runtime", "/v1/machines", "/api/future/capability"} {
		if w := request("GET", path, "", ""); w.Code != http.StatusNotFound {
			t.Fatalf("private route %s: HTTP %d", path, w.Code)
		}
	}
	body := `{"parentId":"documents","name":"Remote document.txt","kind":"file","content":"saved remotely"}`
	for _, origin := range []string{"", "http://localhost:3001", "https://other.example.ts.net"} {
		if w := request("POST", "/api/entries", origin, body); w.Code != http.StatusForbidden {
			t.Fatalf("mutation origin %q: HTTP %d: %s", origin, w.Code, w.Body.String())
		}
	}
	w := request("POST", "/api/entries", "https://workstation.example.ts.net", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("authorized document save: HTTP %d: %s", w.Code, w.Body.String())
	}
	// The local listener immediately sees the same committed document.
	r := httptest.NewRequest("GET", "/api/entries", nil)
	r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
	local := httptest.NewRecorder()
	shared.ServeHTTP(local, r)
	if local.Code != http.StatusOK || !strings.Contains(local.Body.String(), "Remote document.txt") {
		t.Fatalf("shared local document: HTTP %d: %s", local.Code, local.Body.String())
	}
	owner = "456"
	for _, path := range []string{"/", "/assets/app.js", "/api/entries", "/api/terminal/info"} {
		w := request("GET", path, "", "")
		if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "saved remotely") {
			t.Fatalf("unauthorized %s: HTTP %d: %s", path, w.Code, w.Body.String())
		}
	}
	if lookups < 15 {
		t.Fatal("remote requests bypassed identity lookup")
	}
}

func TestRemoteStatusNeverExposesEnrollmentOverUntrustedRequests(t *testing.T) {
	handler := remoteStatusHandler("instance-test", func() remoteSnapshot {
		return remoteSnapshot{Enabled: true, Supported: true, Status: tailnet.Status{State: tailnet.StateNeedsLogin, AuthURL: "https://login.tailscale.com/a/example"}}
	})
	for _, tc := range []struct{ host, peer, origin string }{
		{"workstation.example.ts.net", "100.64.1.2:1234", ""},
		{"localhost:3001", "192.0.2.1:1234", ""},
		{"localhost:3001", "127.0.0.1:1234", "https://attacker.example"},
	} {
		r := httptest.NewRequest("GET", "/api/remote/status", nil)
		r.Host, r.RemoteAddr = tc.host, tc.peer
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "login.tailscale.com") {
			t.Fatalf("status exposed enrollment: HTTP %d: %s", w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/api/remote/status", nil)
	r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var value struct {
		InstanceID string `json:"instanceId"`
		tailnet.Status
	}
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil || value.InstanceID != "instance-test" || value.AuthURL == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid local status: %v: %s", err, w.Body.String())
	}
}

func TestDisabledRemoteDoesNotCreateStateAndFailurePreservesLocalStatus(t *testing.T) {
	home := t.TempDir()
	controller := newRemoteController(context.Background(), home, http.NotFoundHandler())
	if controller.Status().State != tailnet.StateDisabled || controller.Close() != nil {
		t.Fatal("default remote transport is not disabled")
	}
	if _, err := os.Lstat(filepath.Join(home, "tailscale")); !os.IsNotExist(err) {
		t.Fatalf("disabled transport created node state: %v", err)
	}
	// This is an explicit invalid state path, so no network operation starts.
	if err := os.WriteFile(filepath.Join(home, "tailscale"), []byte("unrelated file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.SaveRemoteEnabled(home, true); err != nil {
		t.Fatal(err)
	}
	controller = newRemoteController(context.Background(), home, http.NotFoundHandler())
	defer controller.Close()
	if controller.Status().State != tailnet.StateError || controller.Status().Error == "" {
		t.Fatalf("invalid state failure hidden: %+v", controller.Status())
	}
}
