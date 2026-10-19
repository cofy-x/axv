//go:build darwin || linux

package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func holdConversationOwnership(t *testing.T, manager *Manager, id string) *os.File {
	t.Helper()
	path := manager.ownershipPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := filelock.TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	return lock
}

func TestEndWaitsForOwnedProviderCleanupAfterTerminalDisappears(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery; native commands are injected")
	}
	manager := testManager(t)
	run := serviceRunningRun(t, manager, "wait-for-provider-cleanup")
	lock := holdConversationOwnership(t, manager, run.ID)
	killed := make(chan struct{})
	manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if !slices.Contains(args, "kill-session") || !slices.Contains(args, target(run)) {
			t.Errorf("End addressed unexpected runtime: %q", args)
		}
		close(killed)
		return "", nil
	}
	type outcome struct {
		session Session
		err     error
	}
	finished := make(chan outcome, 1)
	go func() {
		session, err := manager.End(context.Background(), run.ID, run.RunID)
		finished <- outcome{session, err}
	}()
	select {
	case <-killed:
	case <-time.After(2 * time.Second):
		t.Fatal("End did not stop its exact terminal")
	}
	select {
	case result := <-finished:
		t.Fatalf("End completed before its managed provider exited: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	stored, err := manager.store.CodexRun(context.Background(), run.ID, run.RunID)
	if err != nil || stored.Status != "stopping" {
		t.Fatalf("End published completion during cleanup: %+v %v", stored, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-finished:
		if result.err != nil || result.session.Status != "ended" {
			t.Fatalf("End did not finish after managed cleanup: %+v %v", result.session, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("End did not observe released conversation ownership")
	}
}

func TestResumeCanceledDuringCleanupNeverVerifiesOrReservesRun(t *testing.T) {
	manager := testManager(t)
	run := serviceRunningRun(t, manager, "predecessor-cleaning-up")
	if _, err := manager.store.UpdateCodexRun(context.Background(), run.RunID, "ended", "Terminal exited."); err != nil {
		t.Fatal(err)
	}
	holdConversationOwnership(t, manager, run.ID)
	var hostCalls atomic.Int32
	manager.probe = func(context.Context) Info { hostCalls.Add(1); return Info{} }
	manager.verifyHistory = func(context.Context, string, string, string, []string, string) error {
		hostCalls.Add(1)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := manager.Resume(ctx, run.ID, ResumeInput{RequestID: "canceled-resume", ExpectedRunID: run.RunID})
	if !errors.Is(err, context.DeadlineExceeded) || hostCalls.Load() != 0 {
		t.Fatalf("canceled cleanup inspected provider: error=%v hostCalls=%d", err, hostCalls.Load())
	}
	latest, err := manager.store.CodexSession(context.Background(), run.ID)
	if err != nil || latest.RunID != run.RunID || latest.Status != "ended" {
		t.Fatalf("canceled continuation changed predecessor: %+v %v", latest, err)
	}
	_, err = manager.store.CodexRequest(context.Background(), "canceled-resume")
	var absent *storage.Error
	if !errors.As(err, &absent) || absent.Code != "not_found" {
		t.Fatalf("canceled continuation reserved a new run: %v", err)
	}
}

func TestEndOwnershipCorruptionRemainsVisibleAndStopping(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery; native commands are injected")
	}
	for _, kind := range []string{"public-file", "directory"} {
		t.Run(kind, func(t *testing.T) {
			manager := testManager(t)
			run := serviceRunningRun(t, manager, "corrupt-ownership")
			path := manager.ownershipPath(run.ID)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "public-file" {
				if err := os.WriteFile(path, nil, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			manager.run = func(context.Context, string, ...string) (string, error) { return "", nil }
			_, err := manager.End(context.Background(), run.ID, run.RunID)
			requireServiceError(t, err, Unavailable, "codex_ownership_unavailable")
			stored, err := manager.store.CodexRun(context.Background(), run.ID, run.RunID)
			if err != nil || stored.Status != "stopping" {
				t.Fatalf("invalid ownership published successful End: %+v %v", stored, err)
			}
		})
	}
}

func TestStoppingReconciliationCannotBypassManagedCleanup(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery; native commands are injected")
	}
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-terminal-%t", missing), func(t *testing.T) {
			manager := testManager(t)
			run := serviceRunningRun(t, manager, "reconcile-cleanup")
			run, err := manager.store.UpdateCodexRun(context.Background(), run.RunID, "stopping", "")
			if err != nil {
				t.Fatal(err)
			}
			lock := holdConversationOwnership(t, manager, run.ID)
			manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
				if slices.Contains(args, "list-panes") {
					if missing {
						return "can't find session", errors.New("terminal absent")
					}
					return run.TmuxSession + "\t0\t0:", nil
				}
				if !slices.Contains(args, "kill-session") || !slices.Contains(args, target(run)) {
					t.Errorf("reconciliation addressed unexpected runtime: %q", args)
				}
				return "", nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, err = manager.reconcile(ctx, run)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("reconciliation ignored pending cleanup: %v", err)
			}
			stored, err := manager.store.CodexRun(context.Background(), run.ID, run.RunID)
			if err != nil || stored.Status != "stopping" {
				t.Fatalf("inspection finalized an uncleaned run: %+v %v", stored, err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			finished, err := manager.reconcile(context.Background(), stored)
			if err != nil || finished.Status != "ended" {
				t.Fatalf("inspection did not finish after cleanup: %+v %v", finished, err)
			}
		})
	}
}
