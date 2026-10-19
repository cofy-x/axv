package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestDesktopOwnershipAndShutdown(t *testing.T) {
	previousOutput, previousFlags, previousPrefix := log.Writer(), log.Flags(), log.Prefix()
	home := t.TempDir()
	s := Settings{Inputs: launch.Inputs{Home: home, LaunchID: "test-native-launch", Desktop: config.Config{Addr: "127.0.0.1:0", DataDir: filepath.Join(home, "data"), Home: home, ConfigPath: filepath.Join(home, "config.json"), WorkspaceDir: filepath.Join(home, "workspace")}}, ComputeSettings: ComputeSettings{Compute: profile.Compute{Mode: "disabled"}}}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeContext(ctx, s, &bytes.Buffer{}, false)
		close(done)
	}()
	t.Cleanup(func() {
		client.CloseIdleConnections()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("desktop cleanup failed to stop")
		}
	})
	var state launch.State
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _ = launch.ReadState(home)
		if launch.Verify(ctx, state) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := launch.Verify(ctx, state); err != nil {
		t.Fatal(err)
	}
	serviceOutput := log.Writer()
	if serviceOutput == previousOutput {
		t.Fatal("desktop diagnostics were not connected to the service sink")
	}
	if state.LaunchID != s.LaunchID || state.InstanceID == state.LaunchID {
		t.Fatalf("process identity lost its separate launch ownership: %#v", state)
	}
	if filepath.Dir(state.Executable) != filepath.Join(home, "bin") {
		t.Fatalf("readiness was published before pinning the runtime: %q", state.Executable)
	}
	if pinned, err := os.Stat(state.Executable); err != nil || !pinned.Mode().IsRegular() {
		t.Fatalf("ready runtime executable is unavailable: %v", err)
	}
	for _, path := range []string{"/api/compute/info", "/api/host/codex/sessions", "/api/health"} {
		response, err := client.Get(state.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("combined service route %s returned HTTP %d", path, response.StatusCode)
		}
	}
	response, err := client.Get(state.URL + "/api/compute/environment")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var environment computegateway.Environment
	if err := json.Unmarshal(body, &environment); err != nil {
		t.Fatal(err)
	}
	resolvedHome, _ := filepath.EvalSymlinks(home)
	if runtime.GOOS != "windows" && environment.LogPath != filepath.Join(resolvedHome, "logs", "daemon.log") {
		t.Fatalf("actual log path missing: %+v", environment)
	}
	if response.StatusCode != http.StatusOK || environment.Configured || environment.ProfileHome != s.Desktop.Home || environment.ConfigPath != s.Desktop.ConfigPath {
		t.Fatalf("desktop environment does not match startup settings: HTTP %d, %#v", response.StatusCode, environment)
	}
	if err := ServeContext(ctx, s, &bytes.Buffer{}, false); err == nil {
		t.Fatal("second service acquired same profile")
	}
	other := s
	other.Home = t.TempDir()
	if err := ServeContext(ctx, other, &bytes.Buffer{}, false); err == nil {
		t.Fatal("second profile acquired same database")
	}
	if log.Writer() != serviceOutput {
		t.Fatal("rejected service startup changed the active diagnostic sink")
	}
	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("desktop failed to stop")
	}
	if _, err := launch.ReadState(home); err == nil {
		t.Fatal("shutdown retained stale readiness record")
	}
	if log.Writer() != previousOutput || log.Flags() != previousFlags || log.Prefix() != previousPrefix {
		t.Fatal("desktop shutdown did not restore standard diagnostics")
	}
}

func TestUnsafeLoggingSinkPreventsDesktopStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows logging uses stderr")
	}
	home := t.TempDir()
	logs := filepath.Join(home, "logs")
	if err := os.Mkdir(logs, 0755); err != nil {
		t.Fatal(err)
	}
	err := ServeContext(context.Background(), Settings{Inputs: launch.Inputs{Home: home}}, &bytes.Buffer{}, false)
	if err == nil {
		t.Fatal("desktop started without a private log sink")
	}
	if _, err := launch.ReadState(home); !os.IsNotExist(err) {
		t.Fatal("failed startup published runtime state")
	}
}
