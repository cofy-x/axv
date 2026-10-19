package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type ContextBatch = runner.ContextBatch

type ContextInput struct {
	RequestID   string
	SnapshotIDs []string
}

func (m *Manager) contextQueue(session storage.CodexSession) runner.ContextQueue {
	return runner.ContextQueue{Directory: m.runDirectory(session.RunID), SessionID: session.ID, RunID: session.RunID, ThreadID: session.ConversationID}
}

func contextError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, runner.ErrContextConflict):
		return failure(Conflict, "context_request_conflict", "This request identifies different document snapshots or a different Codex run.")
	case errors.Is(err, runner.ErrContextState):
		return failure(Conflict, "context_state_changed", "This document context can no longer be queued or cancelled. Refresh this exact run.")
	case errors.Is(err, runner.ErrContextLimit):
		return failure(Conflict, "context_limit", "Attach up to 8 snapshots with at most 1 MiB combined content. A run accepts 4 pending batches and retains up to 256 delivery receipts; continue the conversation in a new run when that limit is reached.")
	case errors.Is(err, runner.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return failure(Unavailable, "context_unavailable", "The document context queue could not be verified. Check the AXV data directory and refresh before retrying.")
	default:
		return err
	}
}

// ContextBatches observes receipts without creating or replaying provider turns.
func (m *Manager) ContextBatches(ctx context.Context, id, runID string) ([]ContextBatch, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	session, err := m.store.CodexRun(ctx, id, runID)
	if err == nil {
		session, err = m.visibleSession(ctx, session)
	}
	if err != nil {
		return nil, err
	}
	if session.ConversationID == "" {
		return []ContextBatch{}, nil
	}
	if !m.contextQueueInitialized(session) {
		return []ContextBatch{}, nil
	}
	queue := m.contextQueue(session)
	if final(session.Status) {
		if err := queue.Close(ctx); err != nil {
			return nil, contextError(err)
		}
	}
	batches, err := queue.List(ctx)
	return batches, contextError(err)
}

// QueueContext resolves only storage-owned immutable snapshot IDs. Client text,
// paths and thread IDs never select the source or destination of an attachment.
func (m *Manager) QueueContext(ctx context.Context, id, runID string, input ContextInput) (ContextBatch, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return ContextBatch{}, err
	}
	defer done()
	if _, err := launchInput(input.RequestID, nil, nil); err != nil {
		return ContextBatch{}, failure(Invalid, "invalid_context", "Provide a request identifier of 1–128 bytes.")
	}
	if len(input.SnapshotIDs) < 1 || len(input.SnapshotIDs) > runner.MaxContextSnapshots {
		return ContextBatch{}, failure(Invalid, "invalid_context", "Choose 1–8 saved document snapshots.")
	}
	seen := make(map[string]bool)
	for _, snapshotID := range input.SnapshotIDs {
		if len(snapshotID) < 1 || len(snapshotID) > 128 || seen[snapshotID] {
			return ContextBatch{}, failure(Invalid, "invalid_context", "Choose distinct saved document snapshots.")
		}
		seen[snapshotID] = true
	}
	session, err := m.store.CodexRun(ctx, id, runID)
	if err != nil {
		return ContextBatch{}, err
	}
	if _, statErr := os.Stat(filepath.Join(m.runDirectory(runID), "context.json")); !os.IsNotExist(statErr) {
		batches, listErr := m.contextQueue(session).List(ctx)
		if listErr != nil {
			return ContextBatch{}, contextError(listErr)
		}
		if batch, found, replayErr := previousContext(batches, input); found {
			return batch, replayErr
		}
	}
	session, err = m.reconcile(ctx, session)
	if err != nil {
		return ContextBatch{}, err
	}
	release, err := m.runGates.acquire(ctx, runID)
	if err != nil {
		return ContextBatch{}, err
	}
	defer release()
	session, err = m.store.CodexRun(ctx, id, runID)
	if err != nil {
		return ContextBatch{}, err
	}
	queue := m.contextQueue(session)
	if _, err := os.Stat(queue.Directory); os.IsNotExist(err) {
		return ContextBatch{}, failure(Conflict, "context_not_ready", "Wait for this Codex run to become ready before attaching documents.")
	}
	if session.Status != "running" && !m.contextQueueInitialized(session) {
		return ContextBatch{}, failure(Conflict, "context_not_ready", "Only a ready, running Codex run can receive document context.")
	}
	if session.Status == "running" && !m.runnerSupportsContext(session) {
		return ContextBatch{}, failure(Conflict, "context_unsupported", "This run was started by an older AXV runner. End it and explicitly continue the saved conversation to attach documents.")
	}
	// Recover the original operation before run readiness or source lookup. A
	// lost HTTP response must not convert an accepted batch into another send.
	batches, err := queue.List(ctx)
	if err != nil {
		return ContextBatch{}, contextError(err)
	}
	if batch, found, replayErr := previousContext(batches, input); found {
		return batch, replayErr
	}
	if session.Status != "running" || session.ConversationID == "" {
		return ContextBatch{}, failure(Conflict, "context_not_ready", "Only a ready, running Codex run can receive document context.")
	}
	databaseID, err := m.store.DatabaseID(ctx)
	if err != nil {
		return ContextBatch{}, err
	}
	documents := make([]runner.ContextDocument, 0, len(input.SnapshotIDs))
	bytes := 0
	for _, snapshotID := range input.SnapshotIDs {
		snapshot, err := m.store.Snapshot(ctx, snapshotID)
		if err != nil {
			return ContextBatch{}, err
		}
		digest := sha256.Sum256([]byte(snapshot.Content))
		if snapshot.DatabaseID != databaseID || snapshot.SHA256 != hex.EncodeToString(digest[:]) {
			return ContextBatch{}, failure(Conflict, "context_source_changed", "The immutable document snapshot could not be verified against this database.")
		}
		bytes += len(snapshot.Content)
		if bytes > runner.MaxContextBytes {
			return ContextBatch{}, contextError(runner.ErrContextLimit)
		}
		documents = append(documents, runner.ContextDocument{ContextSnapshot: runner.ContextSnapshot{ID: snapshot.ID, DatabaseID: snapshot.DatabaseID, EntryID: snapshot.EntryID, Revision: snapshot.Revision, Name: snapshot.Name, SHA256: snapshot.SHA256}, Content: snapshot.Content})
	}
	batch, err := queue.Enqueue(ctx, input.RequestID, documents, true)
	return batch, contextError(err)
}

func previousContext(batches []ContextBatch, input ContextInput) (ContextBatch, bool, error) {
	for _, batch := range batches {
		if batch.RequestID != input.RequestID {
			continue
		}
		ids := make([]string, 0, len(batch.Snapshots))
		for _, snapshot := range batch.Snapshots {
			ids = append(ids, snapshot.ID)
		}
		if !slices.Equal(ids, input.SnapshotIDs) {
			return ContextBatch{}, true, contextError(runner.ErrContextConflict)
		}
		return batch, true, nil
	}
	return ContextBatch{}, false, nil
}

func (m *Manager) CancelContext(ctx context.Context, id, runID, batchID string) (ContextBatch, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return ContextBatch{}, err
	}
	defer done()
	release, err := m.runGates.acquire(ctx, runID)
	if err != nil {
		return ContextBatch{}, err
	}
	defer release()
	session, err := m.store.CodexRun(ctx, id, runID)
	if err != nil {
		return ContextBatch{}, err
	}
	batch, err := m.contextQueue(session).Cancel(ctx, batchID)
	return batch, contextError(err)
}

func (m *Manager) runnerSupportsContext(session storage.CodexSession) bool {
	state, valid := m.runnerContextState(session)
	return valid && state.Status == runner.StatusReady && state.ContextVersion == 1
}

// A ready run is durable evidence its queue was initialized. Failure after
// readiness preserves that proof so missing receipts cannot become a new send.
func (m *Manager) contextQueueInitialized(session storage.CodexSession) bool {
	if _, err := os.Stat(filepath.Join(m.runDirectory(session.RunID), "context.json")); !os.IsNotExist(err) {
		return true
	}
	state, valid := m.runnerContextState(session)
	return valid && state.ContextVersion == 1
}

func (m *Manager) closeContext(ctx context.Context, session storage.CodexSession) error {
	if session.ConversationID == "" || !m.contextQueueInitialized(session) {
		return nil
	}
	return m.contextQueue(session).Close(ctx)
}

func (m *Manager) runnerContextState(session storage.CodexSession) (runner.State, bool) {
	file, err := os.Open(filepath.Join(m.runDirectory(session.RunID), "state.json"))
	if err != nil {
		return runner.State{}, false
	}
	defer file.Close()
	var state runner.State
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	valid := decoder.Decode(&state) == nil && decoder.Decode(new(any)) == io.EOF && state.RunID == session.RunID && state.ThreadID == session.ConversationID && state.HistoryHome == session.HistoryHome
	return state, valid
}
