package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/app"
	"github.com/cofy-x/axv/apps/server/internal/app/launch"
)

func TestInvalidCommands(t *testing.T) {
	for _, args := range [][]string{
		{"vm", "unknown"}, {"daemon", "unknown"}, {"vm", "operation", "unknown"},
		{"unknown"}, {"--unknown"}, {"--addr", "127.0.0.1:4000"},
		{"version", "extra"},
		{"serve", "--unknown"}, {"serve", "--version"}, {"serve", "--help", "--unknown"},
		{"serve", "--addr"}, {"serve", "--data-dir"},
		{"serve", "--addr="}, {"serve", "--data-dir="}, {"serve", "--data-dir", "  "},
		{"serve", "--addr", "--help"}, {"serve", "--data-dir", "--help"},
		{"serve", "extra"}, {"serve", "--", "extra"},
		{"serve", "--help=invalid"},
		{"internal-codex-run"}, {"internal-codex-run", "one", "two"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(args, &output, "dev"); err == nil || err.Error() == "" {
				t.Fatalf("run(%q) must return a readable error, got %v", args, err)
			}
			if output.Len() != 0 {
				t.Fatalf("invalid command wrote success output: %q", output.String())
			}
		})
	}
}

func TestCodexRunnerDoesNotInitializeTheService(t *testing.T) {
	dir := t.TempDir()
	dataDir, axvHome := filepath.Join(dir, "data"), filepath.Join(dir, "home")
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("AXV_HOME", axvHome)
	t.Setenv("ADDR", "invalid-address")
	var output bytes.Buffer
	err := Run([]string{"internal-codex-run", filepath.Join(dir, "missing.json")}, &output, "dev")
	if err == nil || strings.Contains(err.Error(), "listen address") || output.Len() != 0 {
		t.Fatalf("runner must validate its manifest without initializing the service: %v, %q", err, output.String())
	}
	for _, path := range []string{dataDir, axvHome} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("runner created service data: %s, %v", path, err)
		}
	}
}

func TestInformationalCommandsHaveNoStorageSideEffects(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "must-not-exist")
	axvHome := filepath.Join(t.TempDir(), "must-not-exist")
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("AXV_HOME", axvHome)
	t.Setenv("ADDR", "invalid-address")
	t.Setenv("PORT", "invalid-port")
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"help"}, "axv [flags]"},
		{[]string{"--help"}, "axv [flags]"},
		{[]string{"serve", "--help"}, "axv serve [flags]"},
		{[]string{"serve", "--addr", "invalid", "--data-dir", dataDir, "--help"}, "axv serve [flags]"},
		{[]string{"setup", "--help"}, "axv setup [flags]"},
		{[]string{"daemon", "--help"}, "axv daemon"},
		{[]string{"daemon", "start", "--help"}, "axv daemon"},
		{[]string{"version"}, appName + " test-version\n"},
		{[]string{"--version"}, appName + " test-version\n"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(tt.args, &output, "test-version"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), tt.want) {
				t.Fatalf("output = %q, want %q", output.String(), tt.want)
			}
			if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
				t.Fatalf("informational command created data directory: %v", err)
			}
			if _, err := os.Stat(axvHome); !os.IsNotExist(err) {
				t.Fatalf("informational command created AXV home: %v", err)
			}
		})
	}
}

func TestProfilePrecedenceAndExternalOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AXV_HOME", home)
	t.Setenv("ADDR", "")
	t.Setenv("PORT", "")
	t.Setenv("DATA_DIR", "")
	t.Setenv("AXV_COMPUTE_SOCKET", "")
	data := filepath.Join(home, "existing-data")
	b, err := json.Marshal(map[string]any{"desktop": map[string]string{"addr": "127.0.0.1:4100", "data_dir": data}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "config.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := resolvedSettings(serveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Desktop.Addr != "127.0.0.1:4100" || s.Desktop.DataDir != data {
		t.Fatalf("profile ignored: address=%q data directory=%q", s.Desktop.Addr, s.Desktop.DataDir)
	}
	t.Setenv("PORT", "4101")
	t.Setenv("DATA_DIR", filepath.Join(home, "environment"))
	t.Setenv("AXV_COMPUTE_SOCKET", filepath.Join(home, "external.sock"))
	s, err = resolvedSettings(serveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Desktop.Addr != "127.0.0.1:4101" || s.Compute.Mode != "external" {
		t.Fatalf("environment ignored: address=%q compute mode=%q", s.Desktop.Addr, s.Compute.Mode)
	}
	s, err = resolvedSettings(serveOptions{addr: "127.0.0.1:4102", dataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	if s.Desktop.Addr != "127.0.0.1:4102" || s.Desktop.DataDir != data {
		t.Fatalf("flags ignored: address=%q data directory=%q", s.Desktop.Addr, s.Desktop.DataDir)
	}
}

func TestDesktopDataDefaultsToSelectedProfileHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AXV_HOME", home)
	for _, key := range []string{"ADDR", "PORT", "DATA_DIR", "AXV_COMPUTE_SOCKET", "AXV_WORKSPACE_DIR"} {
		t.Setenv(key, "")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "unrelated-user-config"))
	s, err := resolvedSettings(serveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Home != home || s.Desktop.DataDir != filepath.Join(home, "data") || s.Desktop.WorkspaceDir != filepath.Join(home, "workspace") {
		t.Fatalf("profile defaults escaped AXV_HOME: %#v", s.Desktop)
	}
	if _, err := os.Stat(s.Desktop.DataDir); !os.IsNotExist(err) {
		t.Fatalf("configuration resolution initialized the database directory: %v", err)
	}
}

func TestSharedProfileAndSnapshotRetainAgentSettings(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"ADDR", "PORT", "DATA_DIR", "AXV_COMPUTE_SOCKET", "AXV_WORKSPACE_DIR"} {
		t.Setenv(key, "")
	}
	t.Setenv("AXV_HOME", home)
	t.Setenv("AXV_TMUX_SOCKET", "axv-integration-test")
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-config"))
	t.Setenv("HTTPS_PROXY", "http://test-user:test-password@127.0.0.1:7890")
	profile := map[string]any{
		"runtime_version": 1,
		"desktop":         map[string]string{"addr": "127.0.0.1:0", "data_dir": filepath.Join(home, "data")},
		"compute":         map[string]string{"mode": "external", "socket": filepath.Join(home, "host.sock")},
		"workspace_dir":   filepath.Join(home, "host-projects"),
		"proxy_url":       "http://127.0.0.1:7891",
		"future":          map[string]bool{"preserve": true},
	}
	original, _ := json.Marshal(profile)
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := resolvedSettings(serveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(settings.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	// Background startup must not re-read a changed profile or shell environment.
	t.Setenv("AXV_WORKSPACE_DIR", filepath.Join(home, "different-project"))
	t.Setenv("AXV_TMUX_SOCKET", "different-socket")
	var captured launch.Snapshot
	if err := json.Unmarshal(snapshot, &captured); err != nil {
		t.Fatal(err)
	}
	restored := app.SettingsFromSnapshot(captured)
	if restored.Desktop.WorkspaceDir != profile["workspace_dir"] || restored.Desktop.ProxyURL != profile["proxy_url"] || restored.Desktop.TmuxSocket != "axv-integration-test" || restored.Desktop.Home != home || restored.Desktop.ConfigPath != path || restored.Compute.Mode != "external" || restored.Compute.Socket != filepath.Join(home, "host.sock") {
		t.Fatal("resolved agent or compute configuration was lost in the snapshot")
	}
	if restored.Environment["CODEX_HOME"] != filepath.Join(home, "codex-config") || restored.Environment["HTTPS_PROXY"] != os.Getenv("HTTPS_PROXY") || restored.Fingerprint() != settings.Fingerprint() {
		t.Fatal("runtime snapshot lost its environment or identity")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("startup modified existing profile fields")
	}
	if _, err := os.Stat(restored.Desktop.WorkspaceDir); !os.IsNotExist(err) {
		t.Fatal("startup created the host workspace")
	}
}

func TestCancelledExecutionDoesNotStartCommandsOrInitializeStorage(t *testing.T) {
	home := filepath.Join(t.TempDir(), "uninitialized-profile")
	t.Setenv("AXV_HOME", home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, args := range [][]string{
		{}, {"init"}, {"serve"}, {"setup", "--yes"},
		{"daemon", "start"}, {"daemon", "restart"}, {"daemon", "stop"},
		{"daemon", "status"}, {"daemon", "logs"},
		{"_desktop", "--snapshot", filepath.Join(home, "snapshot.json")},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := Execute(ctx, args, strings.NewReader(""), io.Discard, io.Discard, "dev")
			if !errors.Is(err, context.Canceled) || ExitCode(err) != 130 {
				t.Fatalf("cancelled command %v = %v, exit = %d", args, err, ExitCode(err))
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("cancelled command initialized the profile: %v", err)
			}
		})
	}
}
