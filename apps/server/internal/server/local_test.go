package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalListenerRejectsPublicPeersAndTunnelHostBeforeAllReads(t *testing.T) {
	for _, path := range []string{"/", "/assets/app.js", "/api/bootstrap", "/api/entries", "/api/runtime"} {
		for _, tc := range []struct {
			host, peer string
			want       int
		}{
			{"localhost:3001", "127.0.0.1:12345", http.StatusOK},
			{"[::1]:3001", "[::1]:12345", http.StatusOK},
			{"localhost:3001", "192.0.2.1:12345", http.StatusForbidden},
			{"axv.example.com", "127.0.0.1:12345", http.StatusForbidden},
			{"preview.localhost:3001", "127.0.0.1:12345", http.StatusForbidden},
		} {
			r := httptest.NewRequest(http.MethodGet, "http://"+tc.host+path, nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			r.Header.Set("X-Forwarded-Host", "localhost:3001")
			r.Header.Set("Cf-Access-Authenticated-User-Email", "owner@example.com")
			called := false
			handler := localDesktopHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				_, _ = w.Write([]byte("private content"))
			}))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want || called != (tc.want == http.StatusOK) {
				t.Fatalf("%s host=%s peer=%s: %d handler=%v", path, tc.host, tc.peer, w.Code, called)
			}
			if tc.want != http.StatusOK && strings.Contains(w.Body.String(), "private content") {
				t.Fatal("rejected local read disclosed content")
			}
		}
	}
}
