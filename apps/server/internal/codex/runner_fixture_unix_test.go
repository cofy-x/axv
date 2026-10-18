//go:build darwin || linux

package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
)

// The protocol adapter has its own fake app-server tests. Lifecycle tests use
// this small owned runner to publish identity before executing the terminal fixture.
func TestCodexRunnerFixtureProcess(t *testing.T) {
	if os.Getenv("AXV_TEST_RUNNER") != "1" {
		return
	}
	path := os.Args[len(os.Args)-1]
	data, err := os.ReadFile(path)
	if err != nil {
		os.Exit(11)
	}
	var manifest runner.Manifest
	if json.Unmarshal(data, &manifest) != nil {
		os.Exit(12)
	}
	thread := manifest.ThreadID
	if thread == "" {
		id := manifest.RunID
		thread = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	}
	state := runner.State{RunID: manifest.RunID, ThreadID: thread, HistoryHome: manifest.HistoryHome, Status: runner.StatusReady}
	data, _ = json.Marshal(state)
	if os.WriteFile(filepath.Join(filepath.Dir(path), "state.json"), data, 0600) != nil {
		os.Exit(13)
	}
	if syscall.Exec(manifest.CodexPath, []string{manifest.CodexPath, "--cd", manifest.ProjectPath, "--no-alt-screen", "--no-daemon"}, os.Environ()) != nil {
		os.Exit(14)
	}
}
