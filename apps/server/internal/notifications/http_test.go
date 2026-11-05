package notifications

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

const httpPrivateToken = "write-only-http-private-token"

func notificationHTTPCall(t *testing.T, handler http.Handler, method, path, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Host, request.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet {
		request.Header.Set("Origin", "http://localhost:3001")
	}
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if strings.Contains(response.Body.String(), httpPrivateToken) || strings.Contains(response.Body.String(), `"token":`) {
		t.Fatalf("notification HTTP response disclosed a credential: HTTP %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("notification response can be cached: %v", response.Header())
	}
	return response
}

func notificationHTTPJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func notificationHTTPSnapshot(t *testing.T, response *httptest.ResponseRecorder) Snapshot {
	t.Helper()
	var snapshot Snapshot
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
		t.Fatalf("notification snapshot: HTTP %d %s", response.Code, response.Body)
	}
	return snapshot
}

func TestNotificationHTTPTokenIsWriteOnlyAndMutationsBindStoreAndRevision(t *testing.T) {
	manager := openNotificationTest(t, Options{})
	mux := http.NewServeMux()
	manager.Register(mux)
	initial := notificationHTTPSnapshot(t, notificationHTTPCall(t, mux, http.MethodGet, "/api/notifications", "", nil))
	if !initial.CanConfigure || initial.Config.TokenConfigured {
		t.Fatalf("initial local configuration: %+v", initial)
	}
	update := Update{StoreID: initial.StoreID, Revision: initial.Revision, Enabled: true, TokenAction: "replace", Token: httpPrivateToken,
		ApplicationID: "private-app", DeviceID: "private-device", ReturnOrigin: "https://axv.example", EventTypes: []string{"task_failed"}}
	configured := notificationHTTPSnapshot(t, notificationHTTPCall(t, mux, http.MethodPost, "/api/notifications/config", notificationHTTPJSON(t, update), nil))
	if !configured.Config.Enabled || !configured.Config.TokenConfigured || configured.Revision != initial.Revision+1 {
		t.Fatalf("configuration did not persist without revealing its token: %+v", configured)
	}
	local := notificationHTTPSnapshot(t, notificationHTTPCall(t, mux, http.MethodGet, "/api/notifications", "", nil))
	if !local.Config.TokenConfigured {
		t.Fatal("local snapshot lost token-presence metadata")
	}
	remoteRequest := httptest.NewRequest(http.MethodGet, "https://axv.example/api/notifications", nil)
	remoteRequest.RemoteAddr, remoteRequest.TLS = "100.64.1.2:12345", &tls.ConnectionState{}
	remoteRequest, err := httpaccess.WithAuthenticatedRemote(remoteRequest, "https://axv.example")
	if err != nil {
		t.Fatal(err)
	}
	remoteResponse := httptest.NewRecorder()
	mux.ServeHTTP(remoteResponse, remoteRequest)
	remote := notificationHTTPSnapshot(t, remoteResponse)
	if remote.CanConfigure || !remote.Config.Enabled || remote.Config.TokenConfigured || remote.Config.ApplicationID != "" || remote.Config.DeviceID != "" || remote.Config.ReturnOrigin != "" || strings.Contains(remoteResponse.Body.String(), httpPrivateToken) {
		t.Fatalf("remote snapshot exposed local credential configuration: %+v", remote)
	}
	for _, path := range []string{"/api/notifications/config", "/api/notifications/test", "/api/notifications/disable"} {
		body := notificationHTTPJSON(t, map[string]any{"storeId": initial.StoreID, "revision": initial.Revision})
		if path == "/api/notifications/config" {
			body = notificationHTTPJSON(t, update)
		}
		response := notificationHTTPCall(t, mux, http.MethodPost, path, body, nil)
		if response.Code != http.StatusConflict {
			t.Fatalf("old configuration revision accepted by %s: HTTP %d %s", path, response.Code, response.Body)
		}
		body = notificationHTTPJSON(t, map[string]any{"storeId": strings.Repeat("f", 32), "revision": configured.Revision})
		if path == "/api/notifications/config" {
			wrongStore := update
			wrongStore.StoreID, wrongStore.Revision = strings.Repeat("f", 32), configured.Revision
			body = notificationHTTPJSON(t, wrongStore)
		}
		response = notificationHTTPCall(t, mux, http.MethodPost, path, body, nil)
		if response.Code != http.StatusConflict {
			t.Fatalf("replacement notification store accepted by %s: HTTP %d %s", path, response.Code, response.Body)
		}
	}
	receipt := notificationHTTPJSON(t, map[string]any{"storeId": configured.StoreID, "revision": configured.Revision})
	tested := notificationHTTPSnapshot(t, notificationHTTPCall(t, mux, http.MethodPost, "/api/notifications/test", receipt, nil))
	if len(tested.Events) != 1 || tested.Events[0].Kind != "test" {
		t.Fatalf("explicit test was not queued: %+v", tested)
	}
	disabled := notificationHTTPSnapshot(t, notificationHTTPCall(t, mux, http.MethodPost, "/api/notifications/disable", receipt, nil))
	if disabled.Config.Enabled || !disabled.Config.TokenConfigured || disabled.Events[0].Status != "disabled" {
		t.Fatalf("disable lost credential-presence metadata or left pending delivery: %+v", disabled)
	}
	cleared := notificationHTTPSnapshot(t, notificationHTTPCall(t, mux, http.MethodPost, "/api/notifications/config", notificationHTTPJSON(t, Update{
		StoreID: disabled.StoreID, Revision: disabled.Revision, TokenAction: "clear", EventTypes: []string{},
	}), nil))
	if cleared.Config.TokenConfigured {
		t.Fatal("explicit credential clear retained token-presence metadata")
	}
}

func TestNotificationHTTPRejectsMalformedAndAmbiguousBodiesWithoutMutation(t *testing.T) {
	manager := openNotificationTest(t, Options{})
	mux := http.NewServeMux()
	manager.Register(mux)
	for _, test := range []struct {
		name string
		body func(string) string
	}{
		{"incomplete JSON", func(string) string { return "{" }},
		{"null", func(string) string { return "null" }},
		{"array", func(string) string { return "[]" }},
		{"unknown field", func(body string) string { return strings.TrimSuffix(body, "}") + `,"unknown":true}` }},
		{"duplicate revision", func(body string) string { return strings.TrimSuffix(body, "}") + `,"revision":1}` }},
		{"case duplicate", func(body string) string { return strings.TrimSuffix(body, "}") + `,"ENABLED":false}` }},
		{"escaped duplicate", func(body string) string { return strings.TrimSuffix(body, "}") + `,"\u0065nabled":false}` }},
		{"invalid UTF-8", func(body string) string { return strings.TrimSuffix(body, "}") + ",\"token\":\"\xff\"}" }},
		{"lone high surrogate", func(body string) string { return strings.TrimSuffix(body, "}") + `,"token":"\ud800"}` }},
		{"lone low surrogate", func(body string) string { return strings.TrimSuffix(body, "}") + `,"token":"\udc00"}` }},
		{"trailing object", func(body string) string { return body + " {}" }},
		{"trailing primitive", func(body string) string { return body + " true" }},
		{"oversized whitespace", func(body string) string { return body + strings.Repeat(" ", 64<<10) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := manager.Snapshot()
			body := notificationHTTPJSON(t, Update{StoreID: before.StoreID, Revision: before.Revision, TokenAction: "keep", EventTypes: []string{}})
			response := notificationHTTPCall(t, mux, http.MethodPost, "/api/notifications/config", test.body(body), nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("ambiguous body accepted: HTTP %d %s", response.Code, response.Body)
			}
			if after := manager.Snapshot(); after.StoreID != before.StoreID || after.Revision != before.Revision || after.Config.Enabled != before.Config.Enabled || after.Config.TokenConfigured != before.Config.TokenConfigured {
				t.Fatalf("rejected body changed configuration: before=%+v after=%+v", before, after)
			}
		})
	}
	for _, path := range []string{"/api/notifications?unknown=1", "/api/notifications/absent", "/api/notifications/absent?storeId=one&storeId=two", "/api/notifications/absent?storeId=one&unknown=two", "/api/notifications/absent?storeId=%zz"} {
		response := notificationHTTPCall(t, mux, http.MethodGet, path, "", nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid read query accepted at %s: HTTP %d %s", path, response.Code, response.Body)
		}
	}
	before := manager.Snapshot()
	body := notificationHTTPJSON(t, Update{StoreID: before.StoreID, Revision: before.Revision, TokenAction: "keep", EventTypes: []string{}})
	for _, change := range []func(*http.Request){
		func(request *http.Request) { request.URL.RawQuery = "unexpected=1" },
		func(request *http.Request) { request.Header.Set("Content-Type", "text/plain") },
	} {
		response := notificationHTTPCall(t, mux, http.MethodPost, "/api/notifications/config", body, change)
		if response.Code != http.StatusBadRequest || manager.Snapshot().Revision != before.Revision {
			t.Fatalf("invalid mutation transport changed configuration: HTTP %d %s", response.Code, response.Body)
		}
	}
}

func TestNotificationHTTPPreservesLocalConfigurationAuthority(t *testing.T) {
	manager := openNotificationTest(t, Options{})
	mux := http.NewServeMux()
	manager.Register(mux)
	before := manager.Snapshot()
	body := notificationHTTPJSON(t, Update{StoreID: before.StoreID, Revision: before.Revision, TokenAction: "keep", EventTypes: []string{}})
	for _, test := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"missing origin", func(request *http.Request) { request.Header.Del("Origin") }},
		{"cross origin", func(request *http.Request) { request.Header.Set("Origin", "http://localhost:9999") }},
		{"null origin", func(request *http.Request) { request.Header.Set("Origin", "null") }},
		{"duplicate origin", func(request *http.Request) { request.Header.Add("Origin", "http://localhost:3001") }},
		{"non-loopback peer", func(request *http.Request) { request.RemoteAddr = "192.0.2.10:12345" }},
		{"forwarded authority", func(request *http.Request) {
			request.Host, request.RemoteAddr = "axv.example", "192.0.2.10:12345"
			request.Header.Set("Origin", "https://axv.example")
			request.Header.Set("X-Forwarded-For", "127.0.0.1")
			request.Header.Set("X-Forwarded-Host", "localhost:3001")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := notificationHTTPCall(t, mux, http.MethodPost, "/api/notifications/config", body, test.change)
			if response.Code != http.StatusForbidden || manager.Snapshot().Revision != before.Revision {
				t.Fatalf("untrusted configuration authority accepted: HTTP %d %s", response.Code, response.Body)
			}
		})
	}
	for _, path := range []string{"/api/notifications/config", "/api/notifications/test", "/api/notifications/disable"} {
		request := httptest.NewRequest(http.MethodPost, "https://axv.example"+path, strings.NewReader(body))
		request.RemoteAddr, request.TLS = "100.64.1.2:12345", &tls.ConnectionState{}
		request.Header.Set("Origin", "https://axv.example")
		request.Header.Set("Content-Type", "application/json")
		request, err := httpaccess.WithAuthenticatedRemote(request, "https://axv.example")
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || manager.Snapshot().Revision != before.Revision {
			t.Fatalf("authenticated remote owner bypassed local configuration at %s: HTTP %d %s", path, response.Code, response.Body)
		}
	}
}

func TestNotificationHTTPDetailRetainsOriginalTargetAndDoesNotMutateWork(t *testing.T) {
	var resolved []notificationevent.Target
	available := true
	manager := openNotificationTest(t, Options{Resolve: func(_ context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
		resolved = append(resolved, target)
		return notificationevent.Resolution{Available: available, Current: false, State: "failed", Target: target}, nil
	}})
	enableNotificationTest(t, manager)
	target := notificationevent.Target{Kind: "task", StoreID: "original-host", HostID: "original-host", ProjectID: "original-project", TaskID: "original-task", AttemptID: "original-attempt", EventID: "original-event"}
	manager.mu.Lock()
	next := clone(manager.state)
	manager.ingest(&next, notificationevent.Batch{SourceID: "compute:original-host", StoreID: target.StoreID, First: 1, Next: 1,
		Events: []notificationevent.Event{{ID: target.EventID, Sequence: 1, ObservedAt: time.Now().UTC().Add(time.Second), State: "failed", Target: target}}})
	err := manager.save(next)
	manager.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before := manager.Snapshot()
	if len(before.Events) != 1 {
		t.Fatalf("original work was not captured: %+v", before)
	}
	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	manager.Register(mux)
	path := fmt.Sprintf("/api/notifications/%s?storeId=%s", before.Events[0].ID, before.StoreID)
	for range 2 {
		response := notificationHTTPCall(t, mux, http.MethodGet, path, "", nil)
		var detail struct {
			Event      Notification                 `json:"event"`
			Resolution notificationevent.Resolution `json:"resolution"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.Event.Target != target || detail.Resolution.Target != target || detail.Resolution.Current || detail.Resolution.Available != available {
			t.Fatalf("old detail selected replacement work: HTTP %d %s", response.Code, response.Body)
		}
		available = false
	}
	if len(resolved) != 2 || resolved[0] != target || resolved[1] != target {
		t.Fatalf("detail resolver did not retain original identity: %+v", resolved)
	}
	wrongStore := strings.Replace(path, before.StoreID, strings.Repeat("f", 32), 1)
	response := notificationHTTPCall(t, mux, http.MethodGet, wrongStore, "", nil)
	if response.Code != http.StatusConflict || len(resolved) != 2 {
		t.Fatalf("old notification store reached a work resolver: HTTP %d calls=%d", response.Code, len(resolved))
	}
	after, err := os.ReadFile(manager.path)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatalf("reading an old notification mutated durable state: %v", err)
	}
}
