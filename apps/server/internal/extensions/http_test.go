package extensions

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

func extensionRequest(t *testing.T, handler http.Handler, expectedStore, method, path, origin, body string, remote bool) *httptest.ResponseRecorder {
	t.Helper()
	base := "http://localhost:3001"
	if remote {
		base = "https://desktop.example.ts.net"
	}
	r := httptest.NewRequest(method, base+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:34567"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Content-Type", "application/json")
	if expectedStore != "" {
		r.Header.Set(StoreHeader, expectedStore)
	}
	if remote {
		r.RemoteAddr = "100.64.1.2:34567"
		r.TLS = &tls.ConnectionState{}
		var err error
		r, err = httpaccess.WithAuthenticatedRemote(r, base)
		if err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestHTTPManagementRuntimeAndSandboxHeaders(t *testing.T) {
	s := testStore(t)
	m := New(s)
	mux := http.NewServeMux()
	m.Register(mux)
	p := examplePackage("local.checklist")
	body, _ := json.Marshal(map[string]any{"package": p})
	for _, origin := range []string{"", "null", "http://localhost:9999"} {
		w := extensionRequest(t, mux, s.ID(), "POST", "/api/extensions/install", origin, string(body), false)
		if w.Code != http.StatusForbidden {
			t.Fatalf("accepted install origin %q: %d %s", origin, w.Code, w.Body.String())
		}
	}
	w := extensionRequest(t, mux, s.ID(), "POST", "/api/extensions/install", "http://localhost:3001", string(body), false)
	if w.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	var installed struct {
		App Record `json:"app"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	app := installed.App
	w = extensionRequest(t, mux, s.ID(), "GET", "/api/extensions", "", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), s.ID()) || strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
	}
	start, _ := json.Marshal(SessionInput{app.Digest, app.Generation, "browser", "window"})
	w = extensionRequest(t, mux, s.ID(), "POST", "/api/extensions/"+app.ID+"/sessions", "http://localhost:3001", string(start), false)
	if w.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		SessionID  string `json:"sessionId"`
		ContentURL string `json:"contentUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.SessionID) != 64 || strings.Contains(created.ContentURL, created.SessionID) || strings.Contains(created.ContentURL, "?") {
		t.Fatalf("session credential leaked into content URL: %+v", created)
	}
	w = extensionRequest(t, mux, s.ID(), "GET", created.ContentURL, "", "", false)
	if w.Code != 200 || w.Body.String() != p.HTML || w.Header().Get("Content-Security-Policy") != ContentPolicy || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("content headers: %d %v", w.Code, w.Header())
	}
	base := "/api/extensions/" + app.ID + "/sessions/" + created.SessionID
	w = extensionRequest(t, mux, s.ID(), "POST", base+"/rpc", "http://localhost:3001", `{"method":"storage.set","params":{"value":{"items":[1]},"expectedRevision":0}}`, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"revision":1`) {
		t.Fatalf("RPC save: %d %s", w.Code, w.Body.String())
	}
	w = extensionRequest(t, mux, s.ID(), "POST", base+"/rpc", "http://localhost:3001", `{"method":"storage.set","params":{"value":"stale","expectedRevision":0}}`, false)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "revision_conflict") {
		t.Fatalf("RPC conflict: %d %s", w.Code, w.Body.String())
	}
	for _, raw := range []string{`{"method":"fetch","params":{}}`, `{"method":"storage.get","params":{}}`, `{"method":"storage.get","extra":true}`, `{"method":"storage.get","method":"storage.set"}`, `{"method":"storage.set","params":{"value":null}}`} {
		w = extensionRequest(t, mux, s.ID(), "POST", base+"/rpc", "http://localhost:3001", raw, false)
		if w.Code != 400 {
			t.Fatalf("accepted invalid RPC %s: %d %s", raw, w.Code, w.Body.String())
		}
	}
	w = extensionRequest(t, mux, s.ID(), "PUT", "/api/extensions/"+app.ID, "http://localhost:3001", `{"enabled":false,"expectedGeneration":1}`, false)
	if w.Code != 200 {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	w = extensionRequest(t, mux, s.ID(), "POST", base+"/rpc", "http://localhost:3001", `{"method":"storage.get"}`, false)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "extension_session_expired") {
		t.Fatalf("disabled RPC accepted: %d %s", w.Code, w.Body.String())
	}
	w = extensionRequest(t, mux, s.ID(), "GET", created.ContentURL, "", "", false)
	if w.Code != 404 {
		t.Fatal("disabled content still available")
	}
	w = extensionRequest(t, mux, s.ID(), "DELETE", "/api/extensions/"+app.ID+"?generation=2", "http://localhost:3001", "", false)
	if w.Code != 200 {
		t.Fatalf("uninstall: %d %s", w.Code, w.Body.String())
	}
	w = extensionRequest(t, mux, s.ID(), "GET", "/api/extensions/"+app.ID+"/data", "", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[1]`) {
		t.Fatal("uninstalled data cannot be exported")
	}
	w = extensionRequest(t, mux, s.ID(), "DELETE", "/api/extensions/"+app.ID+"/data?revision=1", "http://localhost:3001", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"value":null`) {
		t.Fatalf("purge: %d %s", w.Code, w.Body.String())
	}
}

func TestHTTPRemoteAccessAndOpaqueBrowserRequests(t *testing.T) {
	s := testStore(t)
	app := installExample(t, s, "local.checklist")
	m := New(s)
	mux := http.NewServeMux()
	m.Register(mux)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/extensions", "", 200},
		{"GET", "/api/extensions/" + app.ID + "/data", "", 200},
		{"GET", "/api/extensions/" + app.ID + "/content/" + s.ID() + "/" + app.Digest, "", 200},
		{"POST", "/api/extensions/install", `{}`, 403},
		{"PUT", "/api/extensions/" + app.ID, `{"enabled":false,"expectedGeneration":1}`, 403},
		{"DELETE", "/api/extensions/" + app.ID + "?generation=1", "", 403},
		{"DELETE", "/api/extensions/" + app.ID + "/data?revision=0", "", 403},
		{"POST", "/api/extensions/" + app.ID + "/sessions", fmt.Sprintf(`{"digest":%q,"generation":1,"clientId":"remote","windowId":"window"}`, app.Digest), 201},
	} {
		w := extensionRequest(t, mux, s.ID(), tc.method, tc.path, "https://desktop.example.ts.net", tc.body, true)
		if w.Code != tc.status {
			t.Fatalf("remote %s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	for _, origin := range []string{"null", "https://attacker.example"} {
		w := extensionRequest(t, mux, s.ID(), "GET", "/api/extensions", origin, "", false)
		if w.Code != 403 {
			t.Fatal("opaque or cross-origin read accepted")
		}
	}
	r := httptest.NewRequest("GET", "http://localhost:3001/api/extensions", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("no-Origin opaque GET accepted")
	}
	r = httptest.NewRequest("GET", "http://localhost:3001/api/extensions", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("forwarded identity trusted")
	}
}

func TestHTTPStrictInstallAndPayloadLimits(t *testing.T) {
	mux := http.NewServeMux()
	s := testStore(t)
	New(s).Register(mux)
	p := examplePackage("local.checklist")
	valid, _ := json.Marshal(map[string]any{"package": p})
	for _, raw := range []string{
		strings.Replace(string(valid), `"manifest":{`, `"manifest":{"unknown":true,`, 1),
		strings.Replace(string(valid), `"package":{`, `"package":{"unknown":true,`, 1),
		`{"package":null}`,
		`{"package":{}}`,
		strings.Repeat(" ", MaxPackageBytes+4097) + string(valid),
	} {
		w := extensionRequest(t, mux, s.ID(), "POST", "/api/extensions/install", "http://localhost:3001", raw, false)
		if w.Code != 400 {
			t.Fatalf("invalid package accepted: %d", w.Code)
		}
	}
}

func TestHTTPStoreIdentityPreventsCrossStoreManagementAndRecovery(t *testing.T) {
	oldStore, currentStore := testStore(t), testStore(t)
	oldApp := installExample(t, oldStore, "local.checklist")
	app := installExample(t, currentStore, "local.checklist")
	if oldStore.ID() == currentStore.ID() || oldApp.Digest != app.Digest || oldApp.Generation != app.Generation {
		t.Fatal("test requires distinct stores with identical installation records")
	}
	m := New(currentStore)
	mux := http.NewServeMux()
	m.Register(mux)
	input, _ := json.Marshal(SessionInput{app.Digest, app.Generation, "client", "window"})
	code, _ := json.Marshal(map[string]any{"package": examplePackage(app.ID), "expectedGeneration": app.Generation})
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/extensions/install", string(code)},
		{"PUT", "/api/extensions/" + app.ID, `{"enabled":false,"expectedGeneration":1}`},
		{"DELETE", "/api/extensions/" + app.ID + "?generation=1", ""},
		{"GET", "/api/extensions/" + app.ID + "/data", ""},
		{"DELETE", "/api/extensions/" + app.ID + "/data?revision=0", ""},
		{"POST", "/api/extensions/" + app.ID + "/sessions", string(input)},
		{"POST", "/api/extensions/" + app.ID + "/sessions/stale/rpc", `{"method":"storage.get"}`},
		{"DELETE", "/api/extensions/" + app.ID + "/sessions/stale", ""},
	} {
		for _, expected := range []string{"", oldStore.ID()} {
			w := extensionRequest(t, mux, expected, tc.method, tc.path, "http://localhost:3001", tc.body, false)
			status, code := 400, "invalid_extension_request"
			if expected != "" {
				status, code = 409, "extension_store_changed"
			}
			if w.Code != status || !strings.Contains(w.Body.String(), code) {
				t.Fatalf("%s %s expectedStore=%q: %d %s", tc.method, tc.path, expected, w.Code, w.Body.String())
			}
		}
	}
	// Content carries the identity in its URL because iframe navigation cannot
	// set the host-only request header. A header cannot override the URL scope.
	staleContent := "/api/extensions/" + app.ID + "/content/" + oldStore.ID() + "/" + app.Digest
	w := extensionRequest(t, mux, currentStore.ID(), "GET", staleContent, "", "", false)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "extension_store_changed") {
		t.Fatalf("stale content: %d %s", w.Code, w.Body.String())
	}
	w = extensionRequest(t, mux, "", "GET", "/api/extensions/"+app.ID+"/content/"+currentStore.ID()+"/"+app.Digest, "", "", false)
	if w.Code != 200 {
		t.Fatalf("scoped content without header: %d %s", w.Code, w.Body.String())
	}
	// The catalog itself remains available without a previously known UUID.
	w = extensionRequest(t, mux, "", "GET", "/api/extensions", "", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), currentStore.ID()) {
		t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
	}
	apps, err := currentStore.List(context.Background())
	if err != nil || len(apps) != 1 || apps[0].Generation != 1 || !apps[0].Installed || !apps[0].Enabled {
		t.Fatalf("stale request changed new store: %+v %v", apps, err)
	}
	if len(m.sessions) != 0 {
		t.Fatal("old store created a new session")
	}
}

func TestHTTPIconCatalogContentAndUpgrade(t *testing.T) {
	s := testStore(t)
	installExample(t, s, "local.generic")
	mux := http.NewServeMux()
	New(s).Register(mux)
	p := examplePackage("local.chores")
	p.Manifest.Icon = IconChecklist
	body, _ := json.Marshal(map[string]any{"package": p})
	w := extensionRequest(t, mux, s.ID(), "POST", "/api/extensions/install", "http://localhost:3001", string(body), false)
	var installed struct {
		App Record `json:"app"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &installed); err != nil || w.Code != 201 || installed.App.Icon != IconChecklist {
		t.Fatalf("install icon: %d %s %v", w.Code, w.Body.String(), err)
	}
	w = extensionRequest(t, mux, "", "GET", "/api/extensions", "", "", false)
	var catalog struct {
		Apps []map[string]json.RawMessage `json:"apps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil || w.Code != 200 {
		t.Fatalf("catalog: %d %s %v", w.Code, w.Body.String(), err)
	}
	for _, app := range catalog.Apps {
		switch string(app["id"]) {
		case `"local.generic"`:
			if _, present := app["icon"]; present {
				t.Fatal("default icon was added to a package that omitted it")
			}
		case `"local.chores"`:
			if string(app["icon"]) != `"checklist"` {
				t.Fatalf("catalog lost declared icon: %s", app["icon"])
			}
		default:
			t.Fatalf("unexpected app in catalog: %s", app["id"])
		}
	}
	oldContent := "/api/extensions/" + p.Manifest.ID + "/content/" + s.ID() + "/" + installed.App.Digest
	w = extensionRequest(t, mux, "", "GET", oldContent, "", "", false)
	if w.Code != 200 || w.Body.String() != p.HTML {
		t.Fatalf("icon package content: %d %s", w.Code, w.Body.String())
	}
	p.Manifest.Icon = IconDocument
	body, _ = json.Marshal(map[string]any{"package": p, "expectedGeneration": installed.App.Generation})
	w = extensionRequest(t, mux, s.ID(), "POST", "/api/extensions/install", "http://localhost:3001", string(body), false)
	var upgraded struct {
		App Record `json:"app"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &upgraded); err != nil || w.Code != 200 || upgraded.App.Icon != IconDocument || upgraded.App.Digest == installed.App.Digest {
		t.Fatalf("icon upgrade: %d %s %v", w.Code, w.Body.String(), err)
	}
	w = extensionRequest(t, mux, "", "GET", oldContent, "", "", false)
	if w.Code != 404 {
		t.Fatalf("old icon digest content still active: %d", w.Code)
	}
	for _, icon := range []Icon{"https://example.com/icon.svg", "data:image/svg+xml,<svg/>", "<svg/>", "unknown"} {
		p.Manifest.Icon = icon
		body, _ = json.Marshal(map[string]any{"package": p, "expectedGeneration": upgraded.App.Generation})
		w = extensionRequest(t, mux, s.ID(), "POST", "/api/extensions/install", "http://localhost:3001", string(body), false)
		if w.Code != 400 {
			t.Fatalf("accepted untrusted icon %s: %d %s", icon, w.Code, w.Body.String())
		}
	}
}
