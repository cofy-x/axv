//go:build linux || darwin

package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
)

func TestEnvironmentExposesActualLogPathAndRuntimeFailure(t *testing.T) {
	home := t.TempDir()
	logger, err := logs.Open(home, "desktop", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	gateway := NewGateway("", GatewayOptions{ProfileHome: home, Logger: logger.Logger, LogPath: logger.Path()})
	defer gateway.Close()
	read := func() Environment {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/compute/environment", nil)
		r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
		w := httptest.NewRecorder()
		gateway.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("environment access: %d %v", w.Code, w.Header())
		}
		var result Environment
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := read()
	if first.Configured || first.LogPath != logger.Path() || first.LoggingError != "" {
		t.Fatalf("unconfigured diagnostics: %+v", first)
	}
	if err := os.Rename(filepath.Join(home, "logs"), filepath.Join(home, "old-logs")); err != nil {
		t.Fatal(err)
	}
	logger.Info("committed.event")
	failed := read()
	if failed.LogPath != first.LogPath || failed.LoggingError == "" {
		t.Fatalf("runtime log failure hidden: %+v", failed)
	}
}

func TestRepeatedGatewayPollingFailureIsLoggedOncePerBinding(t *testing.T) {
	home := t.TempDir()
	logger, err := logs.Open(home, "desktop", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	gateway := NewGateway(filepath.Join(home, "missing.sock"), GatewayOptions{Logger: logger.Logger})
	defer gateway.Close()
	request := func() {
		r := httptest.NewRequest(http.MethodGet, "/api/compute/machines", nil)
		r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
		w := httptest.NewRecorder()
		gateway.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("missing host: %d %s", w.Code, w.Body.String())
		}
	}
	request()
	request()
	request()
	data, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(data, []byte(`"event":"compute.transport_failed"`)); n != 1 {
		t.Fatalf("unchanged polling failure produced %d events", n)
	}
	gateway.Bind(filepath.Join(home, "another.sock"), "", "")
	request()
	data, err = os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(data, []byte(`"event":"compute.transport_failed"`)); n != 2 {
		t.Fatalf("new binding failure not logged: %d", n)
	}
}
