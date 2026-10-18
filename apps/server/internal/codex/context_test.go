package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func contextServiceRun(t *testing.T, manager *Manager) storage.CodexSession {
	t.Helper()
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery; runtime commands are injected")
	}
	session := serviceRunningRun(t, manager, "context-run")
	dir := manager.runDirectory(session.RunID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := manager.contextQueue(session).Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(runner.State{RunID: session.RunID, ThreadID: session.ConversationID, HistoryHome: session.HistoryHome, Status: runner.StatusReady, ContextVersion: 1})
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if !slices.Contains(args, "list-panes") || !slices.Contains(args, "="+session.TmuxSession+":0") {
			t.Fatalf("context unexpectedly controlled runtime: %q", args)
		}
		return session.TmuxSession + "\t0\t0:", nil
	}
	return session
}

func contextServiceSnapshot(t *testing.T, manager *Manager) storage.DocumentSnapshot {
	t.Helper()
	ctx := context.Background()
	entry, err := manager.store.Create(ctx, storage.CreateInput{ParentID: "documents", Name: "context.md", Kind: "file", Content: "immutable-original-token"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.store.CreateSnapshot(ctx, entry.ID, entry.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestContextUsesSavedImmutableSnapshotAndRecoversReplayWithoutHostWork(t *testing.T) {
	manager := testManager(t)
	session := contextServiceRun(t, manager)
	snapshot := contextServiceSnapshot(t, manager)
	ctx := context.Background()
	newContent := "newer-source-token"
	if _, err := manager.store.Update(ctx, snapshot.EntryID, storage.UpdateInput{Content: &newContent, Revision: &snapshot.Revision}); err != nil {
		t.Fatal(err)
	}
	input := ContextInput{RequestID: "queue-document", SnapshotIDs: []string{snapshot.ID}}
	batch, err := manager.QueueContext(ctx, session.ID, session.RunID, input)
	if err != nil || batch.Status != "pending" || batch.ProviderThreadID != session.ConversationID || batch.Snapshots[0].Revision != snapshot.Revision || batch.Snapshots[0].DatabaseID != snapshot.DatabaseID {
		t.Fatalf("queue result: %+v %v", batch, err)
	}
	data, err := os.ReadFile(filepath.Join(manager.runDirectory(session.RunID), "context.json"))
	if err != nil || !strings.Contains(string(data), "immutable-original-token") || strings.Contains(string(data), newContent) {
		t.Fatalf("queued mutable source instead of snapshot: %s %v", data, err)
	}
	manager.run = func(context.Context, string, ...string) (string, error) {
		t.Fatal("idempotent receipt inspected runtime")
		return "", nil
	}
	if replay, err := manager.QueueContext(ctx, session.ID, session.RunID, input); err != nil || !reflect.DeepEqual(replay, batch) {
		t.Fatalf("request replay changed original batch: %+v %v", replay, err)
	}
	_, err = manager.QueueContext(ctx, session.ID, session.RunID, ContextInput{RequestID: input.RequestID, SnapshotIDs: []string{"different"}})
	requireServiceError(t, err, Conflict, "context_request_conflict")
	cancelled, err := manager.CancelContext(ctx, session.ID, session.RunID, batch.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel pending batch: %+v %v", cancelled, err)
	}
	if replay, err := manager.QueueContext(ctx, session.ID, session.RunID, input); err != nil || replay.Status != "cancelled" || replay.ID != batch.ID {
		t.Fatalf("cancelled request replay created a send: %+v %v", replay, err)
	}
	_, err = manager.CancelContext(ctx, "other-session", session.RunID, batch.ID)
	var stored *storage.Error
	if !errors.As(err, &stored) || stored.Code != "not_found" {
		t.Fatalf("cancel escaped exact conversation: %v", err)
	}
}

func TestContextEndsExactRunBeforeNewRunAndNeverTransfersPendingBatch(t *testing.T) {
	manager := testManager(t)
	first := contextServiceRun(t, manager)
	snapshot := contextServiceSnapshot(t, manager)
	ctx := context.Background()
	input := ContextInput{RequestID: "first-context", SnapshotIDs: []string{snapshot.ID}}
	batch, err := manager.QueueContext(ctx, first.ID, first.RunID, input)
	if err != nil {
		t.Fatal(err)
	}
	manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if !slices.Contains(args, "kill-session") || !slices.Contains(args, "="+first.TmuxSession) {
			t.Fatalf("End changed runtime ownership: %q", args)
		}
		batches, err := manager.contextQueue(first).List(ctx)
		if err != nil || len(batches) != 1 || batches[0].Status != "failed" {
			t.Fatalf("End did not close document queue before terminating process: %+v %v", batches, err)
		}
		return "", nil
	}
	ended, err := manager.End(ctx, first.ID, first.RunID)
	if err != nil || ended.Status != "ended" {
		t.Fatalf("End: %+v %v", ended, err)
	}
	second, _, err := manager.store.ReserveCodexRun(ctx, storage.CodexRunInput{Mode: "resume", RequestID: "context-next-run", SessionID: first.ID, ExpectedRunID: first.RunID, NamePrefix: manager.namePrefix, Socket: manager.socket, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.store.StartCodexRun(ctx, second.RunID); err != nil {
		t.Fatal(err)
	}
	if replay, err := manager.QueueContext(ctx, first.ID, first.RunID, input); err != nil || replay.ID != batch.ID || replay.Status != "failed" {
		t.Fatalf("old run context adopted a newer run: %+v %v", replay, err)
	}
	_, err = manager.QueueContext(ctx, first.ID, first.RunID, ContextInput{RequestID: "late-old-context", SnapshotIDs: input.SnapshotIDs})
	requireServiceError(t, err, Conflict, "context_not_ready")
	if _, err := os.Stat(manager.runDirectory(second.RunID)); !os.IsNotExist(err) {
		t.Fatalf("old request created newer run context: %v", err)
	}
}

func TestContextRejectsRunnerWithoutProtocolCapability(t *testing.T) {
	manager := testManager(t)
	session := contextServiceRun(t, manager)
	snapshot := contextServiceSnapshot(t, manager)
	data, _ := json.Marshal(runner.State{RunID: session.RunID, ThreadID: session.ConversationID, HistoryHome: session.HistoryHome, Status: runner.StatusReady})
	if err := os.WriteFile(filepath.Join(manager.runDirectory(session.RunID), "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(manager.runDirectory(session.RunID), "context.json")); err != nil {
		t.Fatal(err)
	}
	_, err := manager.QueueContext(context.Background(), session.ID, session.RunID, ContextInput{RequestID: "old-runner", SnapshotIDs: []string{snapshot.ID}})
	requireServiceError(t, err, Conflict, "context_unsupported")
	if _, err := os.Stat(filepath.Join(manager.runDirectory(session.RunID), "context.json")); !os.IsNotExist(err) {
		t.Fatalf("unsupported runner accepted pending state: %v", err)
	}
}

func TestContextReadsUnboundFailedRunWithoutCreatingQueueState(t *testing.T) {
	manager := testManager(t)
	ctx := context.Background()
	session, _, err := manager.store.ReserveCodexRun(ctx, storage.CodexRunInput{
		Mode: "new", RequestID: "failed-before-provider-bind", ProjectPath: "/service/project", HistoryHome: "/service/history",
		NamePrefix: manager.namePrefix, Socket: manager.socket, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.store.UpdateCodexRun(ctx, session.RunID, "failed", "Startup failed before conversation initialization."); err != nil {
		t.Fatal(err)
	}
	dir := manager.runDirectory(session.RunID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	state, _ := json.Marshal(runner.State{RunID: session.RunID, HistoryHome: session.HistoryHome, Status: runner.StatusFailed})
	if err := os.WriteFile(filepath.Join(dir, "state.json"), state, 0600); err != nil {
		t.Fatal(err)
	}
	rejectServiceHostWork(t, manager)
	for range 2 {
		batches, err := manager.ContextBatches(ctx, session.ID, session.RunID)
		if err != nil || len(batches) != 0 {
			t.Fatalf("unbound failed run exposed invalid context state: %+v %v", batches, err)
		}
	}
	for _, name := range []string{"context.json", "context.lock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("context read created %s for an unbound run: %v", name, err)
		}
	}
}

func TestContextMissingReadyReceiptsNeverRecreateTheOriginalRequest(t *testing.T) {
	manager := testManager(t)
	session := contextServiceRun(t, manager)
	snapshot := contextServiceSnapshot(t, manager)
	ctx := context.Background()
	input := ContextInput{RequestID: "lost-context-receipt", SnapshotIDs: []string{snapshot.ID}}
	if _, err := manager.QueueContext(ctx, session.ID, session.RunID, input); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(manager.runDirectory(session.RunID), "context.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err := manager.QueueContext(ctx, session.ID, session.RunID, input)
	requireServiceError(t, err, Unavailable, "context_unavailable")
	_, err = manager.ContextBatches(ctx, session.ID, session.RunID)
	requireServiceError(t, err, Unavailable, "context_unavailable")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("service recreated lost receipts: %v", err)
	}
}

func TestContextFailedEndIntentPreservesPendingDeliveryUntilEndCanCommit(t *testing.T) {
	manager := testManager(t)
	session := contextServiceRun(t, manager)
	snapshot := contextServiceSnapshot(t, manager)
	ctx := context.Background()
	batch, err := manager.QueueContext(ctx, session.ID, session.RunID, ContextInput{RequestID: "pending-before-end", SnapshotIDs: []string{snapshot.ID}})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(manager.settings.DataDir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TRIGGER reject_end_intent BEFORE UPDATE OF status ON codex_runs
		WHEN NEW.status='stopping' BEGIN SELECT RAISE(ABORT, 'injected end intent failure'); END`); err != nil {
		t.Fatal(err)
	}
	manager.run = func(context.Context, string, ...string) (string, error) {
		t.Fatal("failed End intent controlled the runtime")
		return "", nil
	}
	if _, err := manager.End(ctx, session.ID, session.RunID); err == nil {
		t.Fatal("failed End transaction reported success")
	}
	stored, err := manager.store.CodexRun(ctx, session.ID, session.RunID)
	if err != nil || stored.Status != "running" {
		t.Fatalf("failed End changed run state: %+v %v", stored, err)
	}
	batches, err := manager.contextQueue(session).List(ctx)
	if err != nil || len(batches) != 1 || batches[0].ID != batch.ID || batches[0].Status != "pending" {
		t.Fatalf("failed End discarded the active run's pending document: %+v %v", batches, err)
	}
	if _, err := database.Exec("DROP TRIGGER reject_end_intent"); err != nil {
		t.Fatal(err)
	}
	manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if !slices.Contains(args, "kill-session") {
			t.Fatalf("retry did not address the exact run: %q", args)
		}
		batches, err := manager.contextQueue(session).List(ctx)
		if err != nil || batches[0].Status != "failed" {
			t.Fatalf("committed End did not close pending context before kill: %+v %v", batches, err)
		}
		return "", nil
	}
	if ended, err := manager.End(ctx, session.ID, session.RunID); err != nil || ended.Status != "ended" {
		t.Fatalf("End could not recover after its transaction failure: %+v %v", ended, err)
	}
}

func TestContextEndFinalRunClosesPendingReceiptsWithoutHostWork(t *testing.T) {
	manager := testManager(t)
	session := contextServiceRun(t, manager)
	snapshot := contextServiceSnapshot(t, manager)
	ctx := context.Background()
	if _, err := manager.QueueContext(ctx, session.ID, session.RunID, ContextInput{RequestID: "pending-before-crash", SnapshotIDs: []string{snapshot.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.store.UpdateCodexRun(ctx, session.RunID, "lost", "Runtime disappeared."); err != nil {
		t.Fatal(err)
	}
	rejectServiceHostWork(t, manager)
	if ended, err := manager.End(ctx, session.ID, session.RunID); err != nil || ended.Status != "lost" {
		t.Fatalf("End changed final run identity: %+v %v", ended, err)
	}
	batches, err := manager.contextQueue(session).List(ctx)
	if err != nil || len(batches) != 1 || batches[0].Status != "failed" {
		t.Fatalf("final run retained pending delivery after End: %+v %v", batches, err)
	}
}

func TestContextFailedContinuationBeforeReadinessHasNoQueue(t *testing.T) {
	manager := testManager(t)
	first := contextServiceRun(t, manager)
	ctx := context.Background()
	if _, err := manager.store.UpdateCodexRun(ctx, first.RunID, "ended", "Exited."); err != nil {
		t.Fatal(err)
	}
	next, _, err := manager.store.ReserveCodexRun(ctx, storage.CodexRunInput{Mode: "resume", RequestID: "failed-context-continuation", SessionID: first.ID, ExpectedRunID: first.RunID, NamePrefix: manager.namePrefix, Socket: manager.socket, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.store.UpdateCodexRun(ctx, next.RunID, "failed", "Failed before readiness."); err != nil {
		t.Fatal(err)
	}
	directory := manager.runDirectory(next.RunID)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	state, _ := json.Marshal(runner.State{RunID: next.RunID, ThreadID: next.ConversationID, HistoryHome: next.HistoryHome, Status: runner.StatusFailed})
	if err := os.WriteFile(filepath.Join(directory, "state.json"), state, 0600); err != nil {
		t.Fatal(err)
	}
	rejectServiceHostWork(t, manager)
	if batches, err := manager.ContextBatches(ctx, next.ID, next.RunID); err != nil || len(batches) != 0 {
		t.Fatalf("uninitialized failed continuation exposed queue errors: %+v %v", batches, err)
	}
	for _, name := range []string{"context.json", "context.lock"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("read created %s for failed continuation: %v", name, err)
		}
	}
}
