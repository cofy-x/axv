//go:build darwin || linux

package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func writeNotificationJournal(t *testing.T, manager *Manager, run storage.CodexSession, events ...runner.Activity) {
	t.Helper()
	directory := manager.runDirectory(run.RunID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.Write(filepath.Join(directory, "activity-events.json"), map[string]any{
		"version": 1, "runId": run.RunID, "threadId": run.ConversationID,
		"first": events[0].Sequence, "last": events[len(events)-1].Sequence, "events": events,
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.Write(filepath.Join(directory, "activity.json"), events[len(events)-1], 0o600); err != nil {
		t.Fatal(err)
	}
}

func notificationFixture(run storage.CodexSession, state string, sequence uint64, id string) runner.Activity {
	return runner.Activity{RunID: run.RunID, ThreadID: run.ConversationID, TurnID: "turn-fixture", State: state,
		Sequence: sequence, EventID: strings.Repeat(id, 32), UpdatedAt: time.Now().UTC()}
}

func notificationBatch(t *testing.T, batches []notificationevent.Batch, source string) notificationevent.Batch {
	t.Helper()
	for _, batch := range batches {
		if batch.SourceID == source {
			return batch
		}
	}
	t.Fatalf("missing source %s: %+v", source, batches)
	return notificationevent.Batch{}
}

func notificationRuntime(t *testing.T, manager *Manager, run storage.CodexSession, state *string) {
	t.Helper()
	if executable("tmux") == "" {
		// Discovery is deterministic even on a host without tmux; all
		// commands are captured below and this file is never executed.
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "tmux"), []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	manager.probe = func(context.Context) Info {
		t.Fatal("notification inspection probed provider capabilities")
		return Info{}
	}
	manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if !slices.Contains(args, run.TmuxSocket) {
			t.Fatalf("notification inspection changed the recorded socket: %v", args)
		}
		if slices.Contains(args, "list-panes") && slices.Contains(args, target(run)+":0") {
			switch *state {
			case "live":
				return run.TmuxSession + "\t0\t0:", nil
			case "missing":
				return "no server running", errors.New("fixture runtime absent")
			case "unconfirmed":
				return "", errors.New("fixture inspection unavailable")
			case "dead":
				return run.TmuxSession + "\t0\t1:1", nil
			}
		}
		if *state == "dead" && slices.Contains(args, "kill-session") && slices.Contains(args, target(run)) {
			return "", nil
		}
		t.Fatalf("notification inspection addressed another run or changed work: %v", args)
		return "", errors.New("unexpected fixture command")
	}
}

func TestNotificationSourcesRetainHistoricalArchivedRunsAndConsumerCheckpoints(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	ctx := context.Background()
	empty, err := manager.NotificationSources(ctx, nil)
	if err != nil || len(empty) != 1 || empty[0].SourceID != "codex-store" || empty[0].StoreID != manager.DatabaseID() {
		t.Fatalf("empty-store identity missing: %+v, %v", empty, err)
	}
	first := serviceRunningRun(t, manager, "notification-first")
	writeNotificationJournal(t, manager, first,
		notificationFixture(first, runner.ActivityWorking, 1, "a"), notificationFixture(first, runner.ActivityCompleted, 2, "b"))
	if _, err := manager.store.UpdateCodexRun(ctx, first.RunID, "ended", "Fixture ended."); err != nil {
		t.Fatal(err)
	}
	second, _, err := manager.store.ReserveCodexRun(ctx, storage.CodexRunInput{
		Mode: "resume", RequestID: "notification-second", SessionID: first.ID, ExpectedRunID: first.RunID,
		NamePrefix: manager.namePrefix, Socket: manager.socket, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err = manager.store.StartCodexRun(ctx, second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	archived := true
	if _, err := manager.store.PatchCodexMetadata(ctx, second.ID, storage.CodexMetadataPatch{Revision: second.MetadataRevision, Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	writeNotificationJournal(t, manager, second, notificationFixture(second, runner.ActivityWaitingInput, 1, "c"))
	reopened, err := New(manager.store, manager.settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	live := "live"
	notificationRuntime(t, reopened, second, &live)
	batches, err := reopened.NotificationSources(ctx, map[string]uint64{"codex:" + first.RunID: 1})
	if err != nil || len(batches) != 3 {
		t.Fatalf("all-run discovery = %+v, %v", batches, err)
	}
	historical := notificationBatch(t, batches, "codex:"+first.RunID)
	if historical.Gap || historical.Unavailable || historical.Next != 2 || len(historical.Events) != 1 || historical.Events[0].State != runner.ActivityCompleted || historical.Events[0].Target.RunID != first.RunID {
		t.Fatalf("historical event was lost or redirected: %+v", historical)
	}
	current := notificationBatch(t, batches, "codex:"+second.RunID)
	if current.Gap || current.Unavailable || len(current.Events) != 1 || current.Events[0].Target.StoreID != manager.DatabaseID() || current.Events[0].Target.SessionID != first.ID {
		t.Fatalf("archived exact-run event = %+v", current)
	}
}

func TestResolveNotificationPreservesExactEventsAndSuppressesResolvedWaits(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	ctx := context.Background()
	run := serviceRunningRun(t, manager, "notification-resolve")
	live := "live"
	notificationRuntime(t, manager, run, &live)
	wait := notificationFixture(run, runner.ActivityWaitingApproval, 1, "a")
	writeNotificationJournal(t, manager, run, wait)
	target := manager.notificationTarget(run, wait)
	resolution, err := manager.ResolveNotification(ctx, target)
	if err != nil || !resolution.Available || !resolution.Current || resolution.Target != target {
		t.Fatalf("current exact wait = %+v, %v", resolution, err)
	}
	working := notificationFixture(run, runner.ActivityWorking, 2, "b")
	complete := notificationFixture(run, runner.ActivityCompleted, 3, "c")
	writeNotificationJournal(t, manager, run, wait, working, complete)
	resolution, err = manager.ResolveNotification(ctx, target)
	if err != nil || !resolution.Available || resolution.Current || resolution.Target != target {
		t.Fatalf("resolved wait remained actionable: %+v, %v", resolution, err)
	}
	terminal := manager.notificationTarget(run, complete)
	resolution, err = manager.ResolveNotification(ctx, terminal)
	if err != nil || !resolution.Available || !resolution.Current || resolution.State != runner.ActivityCompleted || resolution.Target != terminal {
		t.Fatalf("historical terminal event = %+v, %v", resolution, err)
	}
	for _, change := range []func(*notificationevent.Target){
		func(target *notificationevent.Target) { target.StoreID = "another-store" },
		func(target *notificationevent.Target) { target.RunID = "another-run" },
		func(target *notificationevent.Target) { target.ThreadID = "11234567-89ab-cdef-0123-456789abcdef" },
		func(target *notificationevent.Target) { target.TurnID = "another-turn" },
		func(target *notificationevent.Target) { target.EventID = strings.Repeat("d", 32) },
		func(target *notificationevent.Target) { target.ProjectID = "another-project" },
	} {
		foreign := terminal
		change(&foreign)
		resolution, err = manager.ResolveNotification(ctx, foreign)
		if err != nil || resolution.Available || resolution.Current || resolution.Target != foreign {
			t.Fatalf("foreign target was redirected: %+v, %v", resolution, err)
		}
	}
	// Retention makes the original completion unavailable; a fresh result
	// from a later turn cannot take its place.
	newer := notificationFixture(run, runner.ActivityFailed, 4, "d")
	newer.TurnID = "newer-turn"
	writeNotificationJournal(t, manager, run, newer)
	resolution, err = manager.ResolveNotification(ctx, terminal)
	if err != nil || resolution.Available || resolution.Current || resolution.Target != terminal {
		t.Fatalf("retained-away event was substituted: %+v, %v", resolution, err)
	}
}

func TestNotificationSourcesExposeJournalFailureEvenWhenMarkerCannotBeWritten(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	run := serviceRunningRun(t, manager, "notification-gap")
	writeNotificationJournal(t, manager, run, notificationFixture(run, runner.ActivityWorking, 1, "a"))
	writeServiceActivity(t, manager, run, runner.ActivityFailed, strings.Repeat("b", 32), 3)
	batches, err := manager.NotificationSources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	batch := notificationBatch(t, batches, "codex:"+run.RunID)
	if !batch.Gap || !batch.Unavailable || batch.Next != 1 || len(batch.Events) != 1 {
		t.Fatalf("missing journal tail was hidden: %+v", batch)
	}
}

func TestNotificationSourcesRecoverRunnerReadinessWithoutDesktopReads(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	ctx := context.Background()
	run, _, err := manager.store.ReserveCodexRun(ctx, storage.CodexRunInput{
		Mode: "new", RequestID: "notification-readiness", ProjectPath: "/service/project", HistoryHome: "/service/history",
		NamePrefix: manager.namePrefix, Socket: manager.socket, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	run.ConversationID = "01234567-89ab-cdef-0123-456789abcdef"
	writeNotificationJournal(t, manager, run, notificationFixture(run, runner.ActivityCompleted, 1, "a"))
	if err := jsonfile.Write(filepath.Join(manager.runDirectory(run.RunID), "state.json"), runner.State{
		RunID: run.RunID, ThreadID: run.ConversationID, HistoryHome: run.HistoryHome, Status: runner.StatusReady,
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	batches, err := manager.NotificationSources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	batch := notificationBatch(t, batches, "codex:"+run.RunID)
	if batch.Unavailable || len(batch.Events) != 1 || batch.Events[0].Target.ThreadID != run.ConversationID {
		t.Fatalf("independent ready runner was omitted: %+v", batch)
	}
	stored, err := manager.store.CodexRun(ctx, run.ID, run.RunID)
	if err != nil || stored.ConversationID != run.ConversationID || stored.Status != "running" {
		t.Fatalf("runner proof was not reconciled: %+v, %v", stored, err)
	}
}

func TestNotificationSourcesVerifyNonterminalTailAfterConsumerCaughtUp(t *testing.T) {
	for _, state := range []string{runner.ActivityWorking, runner.ActivityWaitingApproval, runner.ActivityWaitingInput, runner.ActivityUnknown, runner.ActivityIdle} {
		for _, runtimeState := range []string{"live", "missing", "dead", "unconfirmed", "recorded-final"} {
			t.Run(state+"/"+runtimeState, func(t *testing.T) {
				manager := testManager(t)
				run := serviceRunningRun(t, manager, "notification-tail")
				writeNotificationJournal(t, manager, run, notificationFixture(run, state, 1, "a"))
				if runtimeState == "recorded-final" {
					if _, err := manager.store.UpdateCodexRun(context.Background(), run.RunID, "ended", "Fixture ended."); err != nil {
						t.Fatal(err)
					}
					rejectServiceHostWork(t, manager)
				} else {
					notificationRuntime(t, manager, run, &runtimeState)
				}
				// The consumer already imported this event. An empty delta
				// must still expose an unconfirmed nonterminal tail.
				batches, err := manager.NotificationSources(context.Background(), map[string]uint64{"codex:" + run.RunID: 1})
				if err != nil {
					t.Fatal(err)
				}
				batch := notificationBatch(t, batches, "codex:"+run.RunID)
				uncertain := runtimeState != "live"
				if batch.Gap != uncertain || batch.Unavailable != uncertain || batch.Next != 1 || len(batch.Events) != 0 {
					t.Fatalf("nonterminal tail availability = %+v", batch)
				}
				journal, err := runner.ReadActivityEvents(filepath.Join(manager.runDirectory(run.RunID), "manifest.json"), run.RunID, run.ConversationID, 0)
				if err != nil || len(journal.Events) != 1 || journal.Latest.State != state {
					t.Fatalf("runtime inspection inferred a provider outcome: %+v, %v", journal, err)
				}
			})
		}
	}
}

func TestPendingNotificationVerifiesExactRuntimeAfterDesktopRestart(t *testing.T) {
	for _, runtimeState := range []string{"live", "missing", "dead", "unconfirmed"} {
		t.Run(runtimeState, func(t *testing.T) {
			manager := testManager(t)
			run := serviceRunningRun(t, manager, "notification-pending-runtime")
			wait := notificationFixture(run, runner.ActivityWaitingApproval, 1, "a")
			writeNotificationJournal(t, manager, run, wait)
			reopened, err := New(manager.store, manager.settings)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(reopened.Close)
			notificationRuntime(t, reopened, run, &runtimeState)
			before, err := reopened.store.CodexRun(context.Background(), run.ID, run.RunID)
			if err != nil || before.Status != "running" {
				t.Fatalf("fixture lacks stale running metadata: %+v, %v", before, err)
			}
			target := reopened.notificationTarget(run, wait)
			resolution, err := reopened.ResolveNotification(context.Background(), target)
			live := runtimeState == "live"
			if err != nil || resolution.Available != live || resolution.Current != live || resolution.Target != target || resolution.State != runner.ActivityWaitingApproval {
				t.Fatalf("pending request trusted stale lifecycle or lost original target: %+v, %v", resolution, err)
			}
		})
	}
}

func TestNotificationSourceConfirmsCompletionPublishedDuringExitInspection(t *testing.T) {
	manager := testManager(t)
	run := serviceRunningRun(t, manager, "notification-exit-race")
	working := notificationFixture(run, runner.ActivityWorking, 1, "a")
	completed := notificationFixture(run, runner.ActivityCompleted, 2, "b")
	writeNotificationJournal(t, manager, run, working)
	live := "live"
	notificationRuntime(t, manager, run, &live)
	manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if !slices.Contains(args, run.TmuxSocket) || !slices.Contains(args, "list-panes") || !slices.Contains(args, target(run)+":0") {
			t.Fatalf("notification exit inspection changed its exact target: %v", args)
		}
		writeNotificationJournal(t, manager, run, working, completed)
		return "no server running", errors.New("fixture runtime exited after recording completion")
	}
	batches, err := manager.NotificationSources(context.Background(), map[string]uint64{"codex:" + run.RunID: 1})
	if err != nil {
		t.Fatal(err)
	}
	batch := notificationBatch(t, batches, "codex:"+run.RunID)
	if batch.Gap || batch.Unavailable || batch.Next != 2 || len(batch.Events) != 1 || batch.Events[0].State != runner.ActivityCompleted || batch.Events[0].Target.EventID != completed.EventID {
		t.Fatalf("concurrent durable completion became an inferred gap: %+v", batch)
	}
}
