package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/notifications"
	"github.com/cofy-x/axv/apps/server/internal/tailnet/access"
)

func TestRemoteNotificationAdmissionPreservesOwnerAndLocalAdministration(t *testing.T) {
	fixture := newWorkstationNotificationFixture(t)
	manager, shared := fixture.open(t)
	owner := "123"
	middleware, err := access.NewMiddleware(access.MiddlewareOptions{Origin: "https://workstation.example.ts.net", OwnerUserID: "123", AllowRoute: remoteRoute, LocalOnlyRoute: remoteLocalOnlyRoute, WhoIs: func(context.Context, string) (access.Identity, error) {
		return access.Identity{UserID: owner, NodeID: "exact-node"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer middleware.Close()
	handler := middleware.Wrap(shared)
	call := func(method, path, origin, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://workstation.example.ts.net"+path, strings.NewReader(body))
		r.Host = "workstation.example.ts.net"
		r.RemoteAddr = "100.64.1.2:12345"
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/notifications", "/api/notifications/push"} {
		w := call("GET", path, "", "")
		if w.Code != http.StatusOK {
			t.Fatalf("remote owner notification read denied: %s %d %s", path, w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("notification read allowed protected caching")
		}
	}
	// Exact event reads reach the notification owner; absent history stays absent.
	id := strings.Repeat("b", 32)
	w := call("GET", "/api/notifications/"+id+"?storeId="+manager.Snapshot().StoreID, "", "")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "notification_unavailable") {
		t.Fatalf("event read did not reach original-history resolution: %d %s", w.Code, w.Body)
	}
	initial := manager.PushSnapshot()
	body, _ := json.Marshal(notifications.PushPrepare{PushIdentity: notifications.PushIdentity{StoreID: initial.StoreID, Revision: initial.Revision}, Contact: "mailto:owner@example.com"})
	for _, origin := range []string{"", "https://other.example.ts.net", "http://localhost:3001"} {
		if w := call("POST", "/api/notifications/push/prepare", origin, string(body)); w.Code != http.StatusForbidden {
			t.Fatalf("push mutation accepted wrong origin: %q %d", origin, w.Code)
		}
	}
	w = call("POST", "/api/notifications/push/prepare", "https://workstation.example.ts.net", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("authorized remote push prepare denied: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/notifications/config", "/api/notifications/test", "/api/notifications/disable", "/api/notifications/push/rotate"} {
		w := call("POST", path, "https://workstation.example.ts.net", string(body))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "remote_local_only") {
			t.Fatalf("local notification administration admitted remotely: %s %d %s", path, w.Code, w.Body)
		}
	}
	for _, path := range []string{"/api/notifications/future", "/api/notifications/push/future", "/api/notifications/config", "/api/runtime"} {
		if w := call("GET", path, "", ""); w.Code != http.StatusNotFound {
			t.Fatalf("unreviewed remote route admitted: %s %d", path, w.Code)
		}
	}
	owner = "456"
	for _, path := range []string{"/api/notifications", "/api/notifications/push", "/api/notifications/" + id} {
		w := call("GET", path, "", "")
		if w.Code != http.StatusForbidden {
			t.Fatalf("non-owner notification read admitted: %s %d", path, w.Code)
		}
	}
}
