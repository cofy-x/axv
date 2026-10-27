package server

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

func TestRemoteSwitchRequiresRawLoopbackAndExactOrigin(t *testing.T) {
	c, nodes, _ := remoteControllerFixture(t)
	handler := remoteSettingsHandler(c)
	for _, test := range []struct {
		peer, host, origin string
		authenticated      bool
	}{
		{"127.0.0.1:1234", "localhost:3001", "", false},
		{"127.0.0.1:1234", "localhost:3001", "https://attacker.example", false},
		{"192.0.2.1:1234", "localhost:3001", "http://localhost:3001", false},
		{"100.64.1.2:1234", "workstation.example.ts.net", "https://workstation.example.ts.net", true},
	} {
		r := httptest.NewRequest(http.MethodPut, "http://"+test.host+"/api/remote", strings.NewReader(`{"enabled":true}`))
		r.RemoteAddr = test.peer
		r.Header.Set("Origin", test.origin)
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		if test.authenticated {
			r.TLS = &tls.ConnectionState{}
			var err error
			r, err = httpaccess.WithAuthenticatedRemote(r, test.origin)
			if err != nil {
				t.Fatal(err)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "remote_local_only") || len(*nodes) != 0 {
			t.Fatalf("remote mutation bypassed local identity: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestRemoteSwitchRejectsAmbiguousBodiesBeforeMutation(t *testing.T) {
	c, nodes, _ := remoteControllerFixture(t)
	for _, body := range []string{"", `null`, `[]`, `{}`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":false,"unknown":true}`, `{"enabled":false} {}`, `{"enabled":false`, strings.Repeat(" ", 4096) + `{"enabled":true}`} {
		r := httptest.NewRequest(http.MethodPut, "http://localhost:3001/api/remote", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://localhost:3001")
		w := httptest.NewRecorder()
		remoteSettingsHandler(c).ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_remote_request") || len(*nodes) != 0 {
			t.Fatalf("invalid switch body accepted %q: %d %s", body, w.Code, w.Body.String())
		}
	}
}

func TestRemoteSwitchReturnsSafeStateAndVisibleWriteErrors(t *testing.T) {
	c, nodes, _ := remoteControllerFixture(t)
	request := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "http://localhost:3001/api/remote", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://localhost:3001")
		w := httptest.NewRecorder()
		remoteSettingsHandler(c).ServeHTTP(w, r)
		return w
	}
	w := request(`{"enabled":true}`)
	var value map[string]any
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &value) != nil || value["enabled"] != true || value["supported"] != true || value["state"] != "ready" || value["url"] == nil || len(*nodes) != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid enable response: %d %s", w.Code, w.Body.String())
	}
	if err := os.WriteFile(filepath.Join(c.home, "config.json"), []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	w = request(`{"enabled":false}`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "remote_config_failed") || (*nodes)[0].closeCount() != 0 {
		t.Fatalf("failed save changed live connection: %d %s", w.Code, w.Body.String())
	}
}
