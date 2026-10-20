//go:build darwin || linux

package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAttachmentStartFailurePreservesStageAndNativeCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-tmux")
	p, err := (Backend{}).Attach(context.Background(), path, Target{Session: "exact-run"}, 80, 24)
	if p != nil || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "start attachment PTY") {
		t.Fatalf("attachment lost its startup stage or native cause: process=%v error=%v", p, err)
	}
}

func TestAttachmentWaitsForClientRegistrationBeforeResizing(t *testing.T) {
	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("requires tmux")
	}
	target := Target{Socket: "axv-ready-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36), Session: "exact-run"}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	backend := Backend{}
	projectPath := t.TempDir()
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 4*time.Second)
		defer done()
		if output, err := backend.Command(cleanup, path, target, "kill-session", "-t", target.SessionTarget()); err != nil {
			t.Errorf("clean up owned tmux session: %s %v", output, err)
		}
	})
	if err := backend.Start(ctx, path, Launch{Target: target, RunID: "ready-run", ProjectPath: projectPath, Command: []string{"/bin/sleep", "30"}, Environment: Environment(), Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	existing, err := backend.Attach(ctx, path, target, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(existing.Close)
	directory := t.TempDir()
	release := filepath.Join(directory, "release")
	wrapper := filepath.Join(directory, "tmux")
	script := "#!/bin/sh\nfor arg do\n  if [ \"$arg\" = attach-session ]; then\n    while [ ! -e " + shellQuote(release) + " ]; do /bin/sleep 0.01; done\n  fi\ndone\nexec " + shellQuote(path) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	inspected := make(chan struct{})
	var inspections atomic.Int32
	backend.Run = func(ctx context.Context, path string, args ...string) (string, error) {
		output, err := (Backend{}).commandInput(ctx, path, Environment(filepath.Dir(path)), "", args...)
		if slices.Contains(args, "list-clients") && inspections.Add(1) == 2 {
			// A second query proves the existing client did not satisfy readiness.
			close(inspected)
		}
		return output, err
	}
	type result struct {
		process Process
		err     error
	}
	started := make(chan result, 1)
	received := false
	go func() {
		process, err := backend.Attach(ctx, wrapper, target, 80, 24)
		started <- result{process, err}
	}()
	t.Cleanup(func() {
		cancel()
		if !received {
			result := <-started
			if result.process != nil {
				result.process.Close()
			}
		}
	})
	select {
	case result := <-started:
		received = true
		if result.process != nil {
			result.process.Close()
		}
		t.Fatalf("attachment returned before its client registered: %v", result.err)
	case <-inspected:
	case <-ctx.Done():
		t.Fatal("attachment did not inspect its registered client")
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	var attached result
	select {
	case attached = <-started:
		received = true
	case <-ctx.Done():
		t.Fatal("attachment did not become ready after registration")
	}
	if attached.err != nil {
		t.Fatal(attached.err)
	}
	t.Cleanup(attached.process.Close)
	if err := attached.process.Resize(111, 37); err != nil {
		t.Fatal(err)
	}
	if err := backend.WaitSize(ctx, wrapper, target, 111, 37); err != nil {
		t.Fatalf("first resize after readiness was lost: %v", err)
	}
}

func TestAttachmentReadinessCancelsAndRejectsExitedClients(t *testing.T) {
	for _, exit := range []bool{false, true} {
		t.Run(strconv.FormatBool(exit), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tmux")
			command := "exec /bin/sleep 30\n"
			if exit {
				command = "exit 17\n"
			}
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+command), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := Backend{Run: func(_ context.Context, _ string, _ ...string) (string, error) {
				if !exit {
					cancel()
				}
				return "-1", nil
			}}
			started := time.Now()
			process, err := backend.Attach(ctx, path, Target{Socket: "recorded-socket", Session: "exact-run"}, 80, 24)
			if process != nil {
				process.Close()
				t.Fatal("an unregistered client was returned")
			}
			if err == nil || (!exit && !errors.Is(err, context.Canceled)) || (exit && !strings.Contains(err.Error(), "exit status 17")) {
				t.Fatalf("attachment lost readiness failure: %v", err)
			}
			if time.Since(started) > time.Second {
				t.Fatal("attachment did not stop promptly after cancellation or exit")
			}
		})
	}
}

func TestNativeCommandBoundsOutputAndHonorsCallerDeadline(t *testing.T) {
	backend := Backend{}
	output, err := backend.commandInput(context.Background(), "/bin/sh", Environment(), "", "-c", `i=0; while [ "$i" -lt 20000 ]; do printf 'abcdefgh'; i=$((i+1)); done`)
	if err != nil || len(output) != 64<<10 || !strings.HasPrefix(output, "abcdefgh") {
		t.Fatalf("command output was not bounded: len=%d error=%v", len(output), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := backend.commandInput(ctx, "/bin/sleep", Environment(), "", "30"); err == nil {
		t.Fatal("command ignored caller deadline")
	}
	if time.Since(started) > time.Second {
		t.Fatal("command did not return promptly after deadline")
	}
}
