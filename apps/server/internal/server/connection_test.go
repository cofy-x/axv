package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/desktop"
	cfhttpapi "github.com/cofy-x/axv/apps/server/internal/cloudflare/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/tailnet"
	"github.com/cofy-x/axv/apps/server/internal/tailnet/access"
)

func TestRemoteConnectionProjectionNeverIncludesEnrollmentOrDiagnostics(t *testing.T) {
	for _, state := range []tailnet.State{tailnet.StateDisabled, tailnet.StateStarting, tailnet.StateNeedsLogin, tailnet.StateNeedsHTTPS, tailnet.StateReady, tailnet.StateError, tailnet.StateStopped} {
		status := remoteConnection(remoteSnapshot{Enabled: true, Supported: true, Status: tailnet.Status{State: state, URL: "https://workstation.example.ts.net", AuthURL: "https://login.tailscale.com/a/private", OwnerUserID: "private-owner", Error: "/private/state https://login.tailscale.com/a/private"}})
		if status.State != state || state == tailnet.StateReady && (status.URL == "" || status.Message == "") || state != tailnet.StateReady && status.URL != "" {
			t.Fatalf("remote state was lost: %+v", status)
		}
		data, err := json.Marshal(status)
		var value map[string]any
		if err != nil || json.Unmarshal(data, &value) != nil || strings.Contains(string(data), "private") {
			t.Fatalf("private remote state escaped: %s, %v", data, err)
		}
		for key := range value {
			if key != "state" && key != "url" && key != "message" && key != "enabled" && key != "supported" {
				t.Fatalf("unexpected remote field %q: %s", key, data)
			}
		}
	}
	for _, address := range []string{
		"http://workstation.example.ts.net", "https://login.tailscale.com/a/private", "https://private@workstation.example.ts.net",
		"https://workstation.example.ts.net/private", "https://workstation.example.ts.net?private", "https://workstation.example.ts.net?", "https://workstation.example.ts.net#private",
		"https://workstation.example.ts.net:443", "https://workstation.example.ts.net/", "https:workstation.example.ts.net",
	} {
		status := remoteConnection(remoteSnapshot{Status: tailnet.Status{State: tailnet.StateReady, URL: address}})
		if status.State != tailnet.StateError || status.URL != "" {
			t.Fatalf("unsafe remote address was exposed: %s: %+v", address, status)
		}
	}
	if status := remoteConnection(remoteSnapshot{Status: tailnet.Status{State: tailnet.State("private-unknown")}}); status.State != tailnet.StateError {
		t.Fatalf("unknown remote status was exposed: %+v", status)
	}
}

func TestConnectionStatusRequiresTrustedTransportAndUsesRawLocalIdentity(t *testing.T) {
	calls := 0
	handler := connectionHandler(func() remoteSnapshot {
		calls++
		return remoteSnapshot{Enabled: true, Supported: true, Status: tailnet.Status{State: tailnet.StateNeedsLogin, AuthURL: "https://login.tailscale.com/a/private", OwnerUserID: "private-owner", Error: "/private/state"}}
	}, func() desktop.Snapshot {
		return desktop.Snapshot{Status: desktop.Status{State: "disabled", Listen: "127.0.0.1:3002", Error: "private diagnostics"}}
	})
	for _, tc := range []struct{ name, host, peer, origin string }{
		{"untrusted-peer", "localhost:3001", "192.0.2.1:1234", ""},
		{"untrusted-host", "attacker.example", "127.0.0.1:1234", ""},
		{"cross-origin", "localhost:3001", "127.0.0.1:1234", "https://attacker.example"},
	} {
		r := httptest.NewRequest("GET", "/api/connection", nil)
		r.Host, r.RemoteAddr = tc.host, tc.peer
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("X-Forwarded-Host", "localhost:3001")
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || calls != 0 || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("%s bypassed access checks: HTTP %d: %s", tc.name, w.Code, w.Body.String())
		}
	}
	assertResponse := func(t *testing.T, response *httptest.ResponseRecorder, local bool) {
		t.Helper()
		var value map[string]any
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &value) != nil || value["local"] != local || strings.Contains(response.Body.String(), "private") || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("invalid connection response: HTTP %d: %s", response.Code, response.Body.String())
		}
		remote := map[string]any{"enabled": true, "supported": true, "state": "needs-login", "message": "Complete Tailscale enrollment on the computer running AXV."}
		cf := map[string]any{"enabled": false, "configured": false, "state": "disabled", "message": "Cloudflare access is disabled in this profile."}
		if len(value) != 3 || !reflect.DeepEqual(value["remote"], remote) || !reflect.DeepEqual(value["cloudflare"], cf) {
			t.Fatalf("connection projection changed: %s", response.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/api/connection", nil)
	r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assertResponse(t, w, true)
	owner := "123"
	middleware, err := access.NewMiddleware(access.MiddlewareOptions{
		Origin: "https://workstation.example.ts.net", OwnerUserID: "123", AllowRoute: remoteRoute, LocalOnlyRoute: remoteLocalOnlyRoute,
		WhoIs: func(context.Context, string) (access.Identity, error) {
			return access.Identity{UserID: owner, NodeID: "peer-node"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer middleware.Close()
	remote := middleware.Wrap(handler)
	r = httptest.NewRequest("GET", "https://workstation.example.ts.net/api/connection", nil)
	r.RemoteAddr, r.TLS = "100.64.1.2:1234", &tls.ConnectionState{}
	w = httptest.NewRecorder()
	remote.ServeHTTP(w, r)
	assertResponse(t, w, false)
	owner = "456"
	w = httptest.NewRecorder()
	remote.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || calls != 2 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("another owner read connection status: HTTP %d: %s", w.Code, w.Body.String())
	}
}

func TestCloudflareConnectionProjectionHidesLocalDetailsAndRejectsUnsafeURLs(t *testing.T) {
	for _, state := range []string{"disabled", "starting", "listening", "stopped", "error"} {
		status := cfhttpapi.Connection(desktop.Snapshot{Enabled: true, Configured: true, Status: desktop.Status{State: state, URL: "https://axv.example.com", Listen: "127.0.0.1:3002", Error: "private diagnostic"}})
		if status.State != state || state == "listening" && status.URL == "" || state != "listening" && status.URL != "" || status.Message == "" {
			t.Fatalf("lost Cloudflare state: %+v", status)
		}
		data, _ := json.Marshal(status)
		if strings.Contains(string(data), "private") || strings.Contains(string(data), "127.0.0.1") || strings.Contains(string(data), "ready") {
			t.Fatalf("unsafe or unverified connection details escaped: %s", data)
		}
		var fields map[string]any
		if json.Unmarshal(data, &fields) != nil {
			t.Fatal("invalid connection JSON")
		}
		for field := range fields {
			if field != "enabled" && field != "configured" && field != "state" && field != "url" && field != "message" {
				t.Fatalf("unexpected field %q: %s", field, data)
			}
		}
	}
	for _, address := range []string{"https://localhost", "http://axv.example.com", "https://axv.example.com:443", "https://private@axv.example.com", "https://axv.example.com/", "https://axv.example.com?private", "https://axv.example.com#private"} {
		status := cfhttpapi.Connection(desktop.Snapshot{Status: desktop.Status{State: "listening", URL: address}})
		if status.State != "error" || status.URL != "" {
			t.Fatalf("unsafe Cloudflare URL exposed: %+v", status)
		}
	}
	if status := cfhttpapi.Connection(desktop.Snapshot{Status: desktop.Status{State: "private"}}); status.State != "error" {
		t.Fatal("unknown Cloudflare state was exposed")
	}
}
