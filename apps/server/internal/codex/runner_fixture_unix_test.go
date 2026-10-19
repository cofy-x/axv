//go:build darwin || linux

package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
)

// The protocol adapter has its own fake app-server tests. Lifecycle tests use
// this small owned runner to publish identity before executing the terminal fixture.
func TestCodexRunnerFixtureProcess(t *testing.T) {
	if os.Getenv("AXV_TEST_RUNNER") != "1" {
		return
	}
	os.Exit(runCodexRunnerFixture())
}

func runCodexRunnerFixture() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGTERM)
	defer cancel()
	gate := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--fixture-gate=") {
			if gate != "" {
				return 18
			}
			gate = strings.TrimPrefix(arg, "--fixture-gate=")
		}
	}
	if gate == "" && os.Getenv("AXV_TEST_RUNNER_GATE_REQUIRED") == "1" {
		return 18
	}
	if gate != "" {
		return runWithFixtureWriter(ctx, gate, func() int {
			return runOwnedCodexRunnerFixture(ctx, os.Args[len(os.Args)-1])
		})
	}
	return runOwnedCodexRunnerFixture(ctx, os.Args[len(os.Args)-1])
}

func runOwnedCodexRunnerFixture(ctx context.Context, path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 11
	}
	var manifest runner.Manifest
	if json.Unmarshal(data, &manifest) != nil {
		return 12
	}
	thread := manifest.ThreadID
	if thread == "" {
		id := manifest.RunID
		thread = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	}
	ownership, err := runner.AcquireOwnership(ctx, manifest.OwnershipPath)
	if err != nil {
		return 15
	}
	state := runner.State{RunID: manifest.RunID, ThreadID: thread, HistoryHome: manifest.HistoryHome, Status: runner.StatusReady}
	data, _ = json.Marshal(state)
	if os.WriteFile(filepath.Join(filepath.Dir(path), "state.json"), data, 0600) != nil {
		_ = ownership.Close()
		return 13
	}
	child := exec.CommandContext(ctx, manifest.CodexPath, "--cd", manifest.ProjectPath, "--no-alt-screen", "--no-daemon")
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
	child.WaitDelay = time.Second
	code := 0
	err = child.Start()
	if err == nil {
		err = child.Wait()
	}
	if err != nil {
		code = 14
		if child.ProcessState != nil {
			code = child.ProcessState.ExitCode()
			if code < 0 {
				if status, ok := child.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					code = 128 + int(status.Signal())
				}
			}
		}
	}
	if err := ownership.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 16
	}
	if err := runner.RecordExit(path, code); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 17
	}
	return code
}

func TestCodexRunnerFixtureRecordsActualChildExitAfterReleasingOwnership(t *testing.T) {
	for _, code := range []int{0, 7} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			profile, err := os.MkdirTemp("", "axv-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(profile) })
			path := filepath.Join(directory, "manifest.json")
			cli := filepath.Join(directory, "fixture-cli")
			if err := os.WriteFile(cli, []byte("#!/bin/sh\nexit "+strconv.Itoa(code)+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := runner.Manifest{
				SessionID: "session", RunID: "0123456789abcdef0123456789abcdef",
				ProjectPath: directory, CodexPath: cli, HistoryHome: directory,
				SocketRoot: filepath.Join(profile, "run", "codex"), OwnershipPath: filepath.Join(directory, "ownership", "conversation.lock"), Mode: runner.ModeNew,
			}
			if err := runner.ValidateSocketRoot(manifest.SocketRoot); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestCodexRunnerFixtureProcess$", "--", path)
			command.Env = append(os.Environ(), "AXV_TEST_RUNNER=1")
			output, err := command.CombinedOutput()
			if code == 0 && err != nil {
				t.Fatalf("clean fixture exit = %v: %s", err, output)
			}
			var exit *exec.ExitError
			if code != 0 && (!errors.As(err, &exit) || exit.ExitCode() != code) {
				t.Fatalf("nonzero fixture exit = %v: %s", err, output)
			}
			proof, err := runner.ReadExit(directory, manifest.RunID)
			if err != nil || proof == nil || proof.ExitCode != command.ProcessState.ExitCode() || proof.ExitCode != code {
				t.Fatalf("fixture proof = %+v, %v; want actual child exit %d", proof, err, code)
			}
			ownership, err := runner.AcquireOwnership(ctx, manifest.OwnershipPath)
			if err != nil {
				t.Fatalf("fixture retained ownership after its exit proof: %v", err)
			}
			if err := ownership.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
