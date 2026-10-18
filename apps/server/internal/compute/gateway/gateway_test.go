package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
)

func TestEnvironmentAvailableWithoutHostOrProfile(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "unconfigured"
		if configured {
			name = "offline"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "missing-profile")
			configPath := filepath.Join(home, "config.json")
			socket := ""
			if configured {
				socket = filepath.Join(root, "missing.sock")
			}
			gateway := NewGateway(socket, GatewayOptions{ConfigPath: configPath, ProfileHome: home})
			defer gateway.Close()
			r := httptest.NewRequest(http.MethodGet, "/api/compute/environment", nil)
			r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("environment must not be cached: %v", w.Header())
			}
			var got Environment
			decoder := json.NewDecoder(w.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&got); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			want := Environment{
				Platform:                runtime.GOOS,
				Configured:              configured,
				ConfigPath:              configPath,
				ProfileHome:             home,
				ExecutablePath:          executable,
				RequiredLimaVersion:     compute.SupportedLimaVersion,
				AutomaticSetupSupported: runtime.GOOS == "darwin",
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("environment = %#v; want %#v", got, want)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("environment request changed the filesystem: %v, %v", entries, err)
			}
		})
	}
}

func TestEnvironmentRetainsLocalAccessBoundary(t *testing.T) {
	gateway := NewGateway("", GatewayOptions{ProfileHome: "/private/profile"})
	defer gateway.Close()
	for _, tc := range []struct {
		name, method, host, peer, origin string
		status                           int
	}{
		{"local GET", "GET", "localhost:3001", "127.0.0.1:12345", "", http.StatusOK},
		{"same origin", "GET", "localhost:3001", "127.0.0.1:12345", "http://localhost:3001", http.StatusOK},
		{"remote peer", "GET", "localhost:3001", "192.0.2.1:12345", "http://localhost:3001", http.StatusForbidden},
		{"remote host", "GET", "example.com:3001", "127.0.0.1:12345", "http://example.com:3001", http.StatusForbidden},
		{"cross origin", "GET", "localhost:3001", "127.0.0.1:12345", "https://example.com", http.StatusForbidden},
		{"missing mutation origin", "POST", "localhost:3001", "127.0.0.1:12345", "", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/compute/environment", nil)
			r.Host, r.RemoteAddr = tc.host, tc.peer
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			r.Header.Set("X-Forwarded-Host", "localhost:3001")
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("HTTP %d: %s; want %d", w.Code, w.Body.String(), tc.status)
			}
			if tc.status == http.StatusForbidden && (strings.Contains(w.Body.String(), "/private/profile") || !strings.Contains(w.Body.String(), "compute_forbidden")) {
				t.Fatalf("forbidden request exposed environment metadata: %s", w.Body.String())
			}
		})
	}
}

func TestEnvironmentOnlyAllowsGET(t *testing.T) {
	gateway := NewGateway("/missing/host.sock", GatewayOptions{})
	defer gateway.Close()
	for _, method := range []string{http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			r := httptest.NewRequest(method, "/api/compute/environment", nil)
			r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
			r.Header.Set("Origin", "http://localhost:3001")
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, r)
			if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodGet {
				t.Fatalf("method %s: HTTP %d, Allow %q", method, w.Code, w.Header().Get("Allow"))
			}
		})
	}
}

func TestEnvironmentIncludesPreparationSnapshotAndAvailableActions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		check   prepare.Check
		actions []string
	}{
		{"review", prepare.Check{ConfigurationSource: "profile"}, []string{"review"}},
		{"retry", prepare.Check{ConfigurationSource: "profile", LatestJob: &prepare.Job{ID: "failed", CanRetry: true}}, []string{"review", "retry"}},
		{"cancel", prepare.Check{ConfigurationSource: "profile", ActiveJob: &prepare.Job{ID: "active", CanCancel: true}}, []string{"cancel"}},
		{"committing", prepare.Check{ConfigurationSource: "profile", ActiveJob: &prepare.Job{ID: "active", Committed: true}}, nil},
		{"override", prepare.Check{ConfigurationSource: "environment", LockedReason: "launcher override", LatestJob: &prepare.Job{ID: "failed", CanRetry: true}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := NewGateway("", GatewayOptions{})
			defer gateway.Close()
			calls := 0
			gateway.SetPreparationSnapshot(func() prepare.Check {
				calls++
				return tc.check
			})
			request := httptest.NewRequest(http.MethodGet, "/api/compute/environment", nil)
			request.Host, request.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, request)
			var got Environment
			if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || calls != 1 || got.Preparation == nil || !reflect.DeepEqual(*got.Preparation, tc.check) || got.ConfigurationSource != tc.check.ConfigurationSource || !reflect.DeepEqual(got.AvailableActions, tc.actions) {
				t.Fatalf("snapshot response = %#v, calls = %d, HTTP %d", got, calls, response.Code)
			}
			request.RemoteAddr = "192.0.2.1:12345"
			response = httptest.NewRecorder()
			gateway.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || calls != 1 {
				t.Fatalf("forbidden request invoked snapshot: HTTP %d, calls = %d", response.Code, calls)
			}
			request.RemoteAddr, request.Method = "127.0.0.1:12345", http.MethodPost
			request.Header.Set("Origin", "http://localhost:3001")
			response = httptest.NewRecorder()
			gateway.ServeHTTP(response, request)
			if response.Code != http.StatusMethodNotAllowed || calls != 1 {
				t.Fatalf("mutation invoked snapshot: HTTP %d, calls = %d", response.Code, calls)
			}
		})
	}
}
