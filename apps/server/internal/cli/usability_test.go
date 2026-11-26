package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestRemoteEnableAndDisableOfflineAndLive(t *testing.T) {
	// 1. Offline enable creates config.json with remote.enabled=true and hostname.
	home := filepath.Join(t.TempDir(), "offline-profile")
	t.Setenv("AXV_HOME", home)
	var out bytes.Buffer
	if err := Execute(context.Background(), []string{"remote", "enable", "--hostname", "work-box", "--json"}, nil, &out, nil, "dev"); err != nil {
		t.Fatal(err)
	}
	var st remoteStatus
	if err := json.Unmarshal(out.Bytes(), &st); err != nil || st.State != "stopped" || st.Enabled == nil || !*st.Enabled || st.Hostname != "work-box" {
		t.Fatalf("offline enable = %+v, %v", st, err)
	}
	saved, err := profile.ReadRemote(home)
	if err != nil || !saved.Enabled || saved.Hostname != "work-box" {
		t.Fatalf("profile remote = %+v, %v", saved, err)
	}

	// 2. Offline disable updates config.json to enabled=false.
	out.Reset()
	if err := Execute(context.Background(), []string{"remote", "disable", "--json"}, nil, &out, nil, "dev"); err != nil {
		t.Fatal(err)
	}
	saved, err = profile.ReadRemote(home)
	if err != nil || saved.Enabled {
		t.Fatalf("offline disable did not save false: %+v, %v", saved, err)
	}

	// 3. Offline disable on absent profile does not create files.
	absent := filepath.Join(t.TempDir(), "never-created")
	t.Setenv("AXV_HOME", absent)
	out.Reset()
	if err := Execute(context.Background(), []string{"remote", "disable"}, nil, &out, nil, "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("disable created absent profile: %v", err)
	}

	// 4. Live desktop enable sends PUT /api/remote with Origin and waits for authUrl.
	liveHome := t.TempDir()
	t.Setenv("AXV_HOME", liveHome)
	var enabledState atomic.Bool
	var polls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runtime", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": "desktop-live", "launchId": "live-1"})
	})
	mux.HandleFunc("PUT /api/remote", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == "" {
			http.Error(w, "missing origin", http.StatusForbidden)
			return
		}
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		enabledState.Store(req.Enabled)
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": req.Enabled, "state": "starting"})
	})
	mux.HandleFunc("GET /api/remote/status", func(w http.ResponseWriter, _ *http.Request) {
		if !enabledState.Load() {
			_ = json.NewEncoder(w).Encode(map[string]any{"instanceId": "desktop-live", "enabled": false, "state": "disabled"})
			return
		}
		if polls.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"instanceId": "desktop-live", "enabled": true, "state": "starting"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"instanceId": "desktop-live",
			"enabled":    true,
			"state":      "needs-login",
			"authUrl":    "https://login.tailscale.com/a/live-fixture",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := os.MkdirAll(filepath.Join(liveHome, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.Write(filepath.Join(liveHome, "run", "desktop.json"), launch.State{InstanceID: "desktop-live", LaunchID: "live-1", URL: srv.URL}, 0600); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := Execute(context.Background(), []string{"remote", "enable"}, nil, &out, nil, "dev"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Authorize: https://login.tailscale.com/a/live-fixture") {
		t.Fatalf("live enable output missing authUrl:\n%s", out.String())
	}

	out.Reset()
	if err := Execute(context.Background(), []string{"remote", "disable"}, nil, &out, nil, "dev"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Remote access: disabled") {
		t.Fatalf("live disable output = %q", out.String())
	}
}

func TestDoctorAndGCCommandsAreSafeOnAbsentProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "doctor-absent")
	t.Setenv("AXV_HOME", home)
	var out bytes.Buffer
	if err := Execute(context.Background(), []string{"doctor", "--json"}, nil, &out, nil, "0.1.0-test"); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report["version"] != "0.1.0-test" {
		t.Fatalf("doctor json = %q, %v", out.String(), err)
	}
	out.Reset()
	if err := Execute(context.Background(), []string{"gc", "--dry-run", "--json"}, nil, &out, nil, "0.1.0-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("doctor or gc created absent profile: %v", err)
	}
}

func TestDaemonStatusJSONIsReadOnly(t *testing.T) {
	home := filepath.Join(t.TempDir(), "status-absent")
	t.Setenv("AXV_HOME", home)
	var out bytes.Buffer
	if err := Execute(context.Background(), []string{"daemon", "status", "--json"}, nil, &out, nil, "0.1.0-test"); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil || payload["version"] != "0.1.0-test" {
		t.Fatalf("daemon status json = %q, %v", out.String(), err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("daemon status initialized absent profile: %v", err)
	}
}

func TestVMProjectFilesAutoCapturesLiveBindingAndOptionalRootPath(t *testing.T) {
	_, saved := projectBindingFixture(t)
	var capturedBinding bool
	var listedRoot bool
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/project-cli/binding":
			capturedBinding = true
			_ = json.NewEncoder(w).Encode(saved)
		case "/v1/projects/project-cli/files/list":
			var req compute.ProjectFileRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.Path == "" && req.Binding == saved.Binding {
				listedRoot = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": []any{}})
		default:
			http.NotFound(w, r)
		}
	})
	out, receipt, err := cliInvoke("vm", "project", "files", "project-cli")
	if err != nil || !capturedBinding || !listedRoot || !strings.Contains(receipt, "Host: host-cli") || !strings.Contains(out, `"entries"`) {
		t.Fatalf("auto-binding files failed: out=%q receipt=%q err=%v", out, receipt, err)
	}
}
