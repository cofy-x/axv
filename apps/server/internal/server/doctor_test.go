package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoctorHandlerEnforcesLocalAccessAndReturnsChecks(t *testing.T) {
	home := t.TempDir()
	handler := doctorHandler("desktop-test", home, "0.1.0-test")

	for _, tc := range []struct{ host, peer, origin string }{
		{"workstation.example.ts.net", "100.64.1.2:1234", ""},
		{"localhost:3001", "192.0.2.1:1234", ""},
		{"localhost:3001", "127.0.0.1:1234", "https://attacker.example"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/doctor", nil)
		r.Host, r.RemoteAddr = tc.host, tc.peer
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "doctor_local_only") {
			t.Fatalf("doctor exposed over untrusted request %+v: HTTP %d: %s", tc, w.Code, w.Body.String())
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/api/doctor", nil)
	r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("local doctor failed: HTTP %d %q", w.Code, w.Body.String())
	}
	var payload struct {
		InstanceID string `json:"instanceId"`
		Version    string `json:"version"`
		Checks     []any  `json:"checks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || payload.InstanceID != "desktop-test" || payload.Version != "0.1.0-test" || len(payload.Checks) == 0 {
		t.Fatalf("unexpected doctor payload: %v %s", err, w.Body.String())
	}
}
