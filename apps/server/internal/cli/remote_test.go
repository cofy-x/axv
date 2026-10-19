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
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

func remoteStatusFixture(t *testing.T, runtimeID string, handler http.HandlerFunc) string {
	t.Helper()
	home := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runtime", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"instanceId": runtimeID, "launchId": "remote-fixture"})
	})
	mux.HandleFunc("GET /api/remote/status", handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	if err := os.MkdirAll(filepath.Join(home, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	state := launch.State{InstanceID: "desktop-fixture", LaunchID: "remote-fixture", URL: server.URL}
	if err := jsonfile.Write(filepath.Join(home, "run", "desktop.json"), state, 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestRemoteStatusPrintsVerifiedEnrollmentAndHasNoConfigurationSideEffects(t *testing.T) {
	want := remoteStatus{InstanceID: "desktop-fixture", State: "needs-login", AuthURL: "https://login.tailscale.com/a/test-only", OwnerUserID: "123"}
	home := remoteStatusFixture(t, want.InstanceID, func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(want)
	})
	t.Setenv("AXV_HOME", home)
	t.Setenv("ADDR", "invalid-and-unrelated")
	var output bytes.Buffer
	if err := Execute(context.Background(), []string{"remote", "status", "--json"}, nil, &output, nil, "dev"); err != nil {
		t.Fatal(err)
	}
	var got remoteStatus
	if err := json.Unmarshal(output.Bytes(), &got); err != nil || got != want {
		t.Fatalf("status = %+v, %v; want %+v", got, err, want)
	}
	output.Reset()
	if err := Execute(context.Background(), []string{"remote", "status"}, nil, &output, nil, "dev"); err != nil || !strings.Contains(output.String(), "Authorize: "+want.AuthURL) || !strings.Contains(output.String(), "Owner user ID: 123") {
		t.Fatalf("status output = %q, %v", output.String(), err)
	}
	for _, name := range []string{"config.json", "tailscale", "data", "workspace"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatalf("status initialized %s: %v", name, err)
		}
	}
}

func TestRemoteStatusWithoutDesktopDoesNotInitializeTheProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	t.Setenv("AXV_HOME", home)
	var output bytes.Buffer
	if err := Execute(context.Background(), []string{"remote", "status", "--json"}, nil, &output, nil, "dev"); err != nil || strings.TrimSpace(output.String()) != `{"state":"stopped"}` {
		t.Fatalf("stopped status = %q, %v", output.String(), err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("status initialized the profile: %v", err)
	}
}

func TestRemoteStatusRejectsUnverifiedOrReplacedDesktop(t *testing.T) {
	var contacted atomic.Bool
	home := remoteStatusFixture(t, "another-instance", func(w http.ResponseWriter, _ *http.Request) {
		contacted.Store(true)
		json.NewEncoder(w).Encode(remoteStatus{InstanceID: "desktop-fixture", State: "ready"})
	})
	if _, err := readRemoteStatus(context.Background(), home); err == nil || contacted.Load() {
		t.Fatalf("queried remote state before verifying desktop ownership: %v", err)
	}
	home = remoteStatusFixture(t, "desktop-fixture", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(remoteStatus{InstanceID: "replacement", State: "ready"})
	})
	if _, err := readRemoteStatus(context.Background(), home); err == nil || !strings.Contains(err.Error(), "another desktop instance") {
		t.Fatalf("accepted a listener replaced after verification: %v", err)
	}
}

func TestRemoteStatusDoesNotFollowRedirectsOrAcceptUnboundedResponses(t *testing.T) {
	var contacted atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted.Store(true)
		json.NewEncoder(w).Encode(remoteStatus{InstanceID: "desktop-fixture", State: "ready"})
	}))
	t.Cleanup(target.Close)
	home := remoteStatusFixture(t, "desktop-fixture", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	})
	if _, err := readRemoteStatus(context.Background(), home); err == nil || contacted.Load() {
		t.Fatalf("remote status followed a redirect: %v", err)
	}
	home = remoteStatusFixture(t, "desktop-fixture", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(strings.Repeat(" ", 1<<20+1)))
	})
	if _, err := readRemoteStatus(context.Background(), home); err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("accepted unbounded remote status: %v", err)
	}
}
