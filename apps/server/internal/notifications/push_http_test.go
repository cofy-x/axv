package notifications

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
	"github.com/cofy-x/axv/apps/server/internal/notifications/webpush"
)

func pushHTTPSnapshot(t *testing.T, r *httptest.ResponseRecorder) PushSnapshot {
	t.Helper()
	var s PushSnapshot
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &s) != nil {
		t.Fatalf("push request failed: HTTP %d %s", r.Code, r.Body)
	}
	for _, private := range []string{"private-subscription-path", `"endpoint":`, `"auth":`, `"privateKey":`} {
		if strings.Contains(r.Body.String(), private) {
			t.Fatal("push response exposed private delivery material")
		}
	}
	return s
}

func TestPushHTTPExplicitEnrollmentOriginAndSeparatePocketAuthorization(t *testing.T) {
	m := openNotificationTest(t, Options{})
	mux := http.NewServeMux()
	m.Register(mux)
	state := pushHTTPSnapshot(t, notificationHTTPCall(t, mux, "GET", "/api/notifications/push", "", nil))
	if state.KeyID != "" || state.PublicKey != "" || len(state.Subscriptions) != 0 {
		t.Fatal("default read initialized push enrollment")
	}
	prepared := pushHTTPSnapshot(t, notificationHTTPCall(t, mux, "POST", "/api/notifications/push/prepare", notificationHTTPJSON(t, PushPrepare{PushIdentity: PushIdentity{StoreID: state.StoreID, Revision: state.Revision}, Contact: "mailto:owner@example.com"}), nil))
	_, key, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	input := PushSubscribe{PushIdentity: PushIdentity{StoreID: prepared.StoreID, Revision: prepared.Revision}, KeyID: prepared.KeyID, InstallationID: strings.Repeat("c", 32), Label: "Test browser", EventTypes: []string{"task_failed"}, Subscription: webpush.Subscription{Endpoint: "https://fcm.googleapis.com/wp/private-subscription-path", Keys: webpush.Keys{P256DH: key, Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}}
	remote := func(r *http.Request) {
		r.Host = "workstation.example.ts.net"
		r.RemoteAddr = "100.64.1.2:12345"
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Origin", "https://workstation.example.ts.net")
		authorized, err := httpaccess.WithAuthenticatedRemote(r, "https://workstation.example.ts.net")
		if err != nil {
			t.Fatal(err)
		}
		*r = *authorized
	}
	registered := pushHTTPSnapshot(t, notificationHTTPCall(t, mux, "POST", "/api/notifications/push/subscribe", notificationHTTPJSON(t, input), remote))
	if len(registered.Subscriptions) != 1 || !registered.Subscriptions[0].Enabled || m.Snapshot().Config.Enabled {
		t.Fatal("push enrollment depended on Pocket Alert or did not enroll")
	}
	id := PushSubscriptionIdentity{PushIdentity: PushIdentity{StoreID: registered.StoreID, Revision: registered.Revision}, KeyID: registered.KeyID, InstallationID: input.InstallationID, SubscriptionID: registered.Subscriptions[0].ID}
	// Same labels/IDs never grant an untrusted request remote owner authority.
	for _, change := range []func(*http.Request){
		func(r *http.Request) {
			r.Host = "workstation.example.ts.net"
			r.RemoteAddr = "100.64.1.2:12345"
			r.Header.Set("Origin", "https://workstation.example.ts.net")
		},
		func(r *http.Request) { remote(r); r.Header.Set("Origin", "https://other.example.ts.net") },
		func(r *http.Request) { remote(r); r.Header.Del("Origin") },
		func(r *http.Request) { remote(r); r.Header.Add("Origin", "https://workstation.example.ts.net") },
	} {
		if r := notificationHTTPCall(t, mux, "POST", "/api/notifications/push/test", notificationHTTPJSON(t, id), change); r.Code != http.StatusForbidden {
			t.Fatalf("untrusted push mutation admitted: %d", r.Code)
		}
	}
	// The origin is derived from transport authority, never supplied by JSON.
	if r := notificationHTTPCall(t, mux, "POST", "/api/notifications/push/test", notificationHTTPJSON(t, id), nil); r.Code != http.StatusConflict {
		t.Fatalf("another origin controlled the subscription: %d", r.Code)
	}
	tested := pushHTTPSnapshot(t, notificationHTTPCall(t, mux, "POST", "/api/notifications/push/test", notificationHTTPJSON(t, id), remote))
	if tested.Revision != registered.Revision || len(m.Snapshot().Events) != 1 {
		t.Fatal("synthetic push test mutated configuration revision or failed to queue")
	}
	for _, path := range []string{"/api/notifications/config", "/api/notifications/test", "/api/notifications/disable", "/api/notifications/push/rotate"} {
		if r := notificationHTTPCall(t, mux, "POST", path, notificationHTTPJSON(t, PushPrepare{PushIdentity: id.PushIdentity, Contact: "mailto:owner@example.com"}), remote); r.Code != http.StatusForbidden {
			t.Fatalf("local administration admitted remotely: %s %d", path, r.Code)
		}
	}
	disabled := pushHTTPSnapshot(t, notificationHTTPCall(t, mux, "POST", "/api/notifications/push/disable", notificationHTTPJSON(t, id), remote))
	if disabled.Subscriptions[0].Enabled {
		t.Fatal("server disable left subscription sending")
	}
	id.Revision = disabled.Revision
	removed := pushHTTPSnapshot(t, notificationHTTPCall(t, mux, "POST", "/api/notifications/push/unsubscribe", notificationHTTPJSON(t, id), remote))
	if len(removed.Subscriptions) != 0 {
		t.Fatal("explicit unsubscribe retained server subscription")
	}
}

func TestPushHTTPRejectsAmbiguousInputAndUnknownRoutes(t *testing.T) {
	m := openNotificationTest(t, Options{})
	mux := http.NewServeMux()
	m.Register(mux)
	before := m.PushSnapshot()
	body := notificationHTTPJSON(t, PushPrepare{PushIdentity: PushIdentity{StoreID: before.StoreID, Revision: before.Revision}, Contact: "mailto:owner@example.com"})
	for _, invalid := range []string{"null", "[]", body + " {}", strings.TrimSuffix(body, "}") + `,"revision":0}`, strings.TrimSuffix(body, "}") + `,"origin":"https://other.example"}`, body + strings.Repeat(" ", 64<<10)} {
		if r := notificationHTTPCall(t, mux, "POST", "/api/notifications/push/prepare", invalid, nil); r.Code != http.StatusBadRequest {
			t.Fatalf("ambiguous push input admitted: %d", r.Code)
		}
	}
	for _, path := range []string{"/api/notifications/push?", "/api/notifications/push?unknown=1"} {
		if r := notificationHTTPCall(t, mux, "GET", path, "", nil); r.Code != http.StatusBadRequest {
			t.Fatalf("push snapshot query admitted: %d", r.Code)
		}
	}
	for _, path := range []string{"/api/notifications/push/future", "/api/notifications/push/prepare/"} {
		if r := notificationHTTPCall(t, mux, "POST", path, body, nil); r.Code != http.StatusNotFound {
			t.Fatalf("unknown push route admitted: %d", r.Code)
		}
	}
	if after := m.PushSnapshot(); after.KeyID != "" || after.Revision != before.Revision {
		t.Fatal("rejected request changed push state")
	}
}
