package codex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func TestRunnerUsesFixedExecutableAndExplicitProfileSocketRoot(t *testing.T) {
	base := testManager(t)
	pinned := filepath.Join(base.settings.ProfileHome, "bin", "axv-pinned")
	options := base.settings
	options.RunnerExecutable = pinned
	manager, err := New(base.store, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	options.RunnerExecutable = filepath.Join(base.settings.ProfileHome, "bin", "axv-reinstalled")
	t.Setenv("AXV_HOME", filepath.Join(base.settings.ProfileHome, "unrelated"))
	t.Setenv("CODEX_HOME", filepath.Join(base.settings.ProfileHome, "other-provider"))
	session := storage.CodexSession{ID: "session", RunID: "run", ProjectPath: base.settings.WorkspaceDir, HistoryHome: filepath.Join(base.settings.ProfileHome, "provider")}
	command, err := manager.prepareRunner(Info{CodexPath: "/official/codex"}, session)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(manager.runDirectory(session.RunID), "manifest.json")
	if !reflect.DeepEqual(command, []string{pinned, "internal-codex-run", manifestPath}) {
		t.Fatalf("runner command changed after construction: %v", command)
	}
	data, err := os.ReadFile(manifestPath)
	var manifest runner.Manifest
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.SocketRoot != filepath.Join(base.settings.ProfileHome, "run", "codex") || manifest.HistoryHome != session.HistoryHome {
		t.Fatalf("manifest changed profile or provider ownership: %+v", manifest)
	}
	if manifest.OwnershipPath != manager.ownershipPath(session.ID) || filepath.Dir(manifest.OwnershipPath) != filepath.Join(base.settings.DataDir, "codex-owners") {
		t.Fatalf("manifest lacks stable conversation ownership: %+v", manifest)
	}
}

func TestRunnerRequiresFixedExecutableWithoutDynamicFallback(t *testing.T) {
	manager := testManager(t)
	command, err := manager.runnerCommand("/manifest.json")
	var unavailable *Error
	if command != nil || !errors.As(err, &unavailable) || unavailable.Code != "codex_runtime_unavailable" {
		t.Fatalf("missing executable did not report runtime unavailable: %v, %v", command, err)
	}
	options := manager.settings
	options.RunnerExecutable = "./axv"
	if _, err := New(manager.store, options); err == nil {
		t.Fatal("relative runner executable accepted")
	}
}

func TestRunnerRejectsLongSocketRootBeforeWritingManifest(t *testing.T) {
	base := testManager(t)
	base.settings.ProfileHome = filepath.Join(base.settings.ProfileHome, strings.Repeat("x", 120))
	options := base.settings
	options.RunnerExecutable = "/fixed/axv"
	manager, err := New(base.store, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	_, err = manager.prepareRunner(Info{}, storage.CodexSession{ID: "session", RunID: "run"})
	var unavailable *Error
	if !errors.As(err, &unavailable) || !strings.Contains(err.Error(), "AXV_DEV_HOME") {
		t.Fatalf("long socket path did not produce actionable error: %v", err)
	}
	if _, err := os.Stat(manager.runDirectory("run")); !os.IsNotExist(err) {
		t.Fatalf("invalid socket path created runtime data: %v", err)
	}
}
