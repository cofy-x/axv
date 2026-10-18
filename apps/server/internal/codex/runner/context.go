package runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
)

const (
	MaxContextSnapshots = 8
	MaxContextBytes     = 1 << 20
	maxContextBatches   = 256
	maxPendingBatches   = 4
	maxContextFileBytes = 32 << 20
)

var (
	ErrContextConflict = errors.New("the document context request conflicts with a recorded request")
	ErrContextLimit    = errors.New("the document context queue is full")
	ErrContextState    = errors.New("the document context batch cannot be cancelled")
)

// ContextSnapshot is an immutable document reference. Content is supplied only
// by the desktop storage owner and is never returned by the context HTTP API.
type ContextSnapshot struct {
	ID         string `json:"id"`
	DatabaseID string `json:"databaseId"`
	EntryID    string `json:"entryId"`
	Revision   int    `json:"revision"`
	Name       string `json:"name"`
	SHA256     string `json:"sha256"`
}

type ContextDocument struct {
	ContextSnapshot
	Content string `json:"content"`
}

// ContextBatch records delivery to one exact managed run and provider thread.
// Unknown means dispatch may have happened: it is never automatically retried.
type ContextBatch struct {
	ID               string            `json:"id"`
	RequestID        string            `json:"requestId"`
	SessionID        string            `json:"sessionId"`
	RunID            string            `json:"runId"`
	ProviderThreadID string            `json:"providerThreadId"`
	Status           string            `json:"status"`
	Snapshots        []ContextSnapshot `json:"snapshots"`
	CreatedAt        string            `json:"createdAt"`
	UpdatedAt        string            `json:"updatedAt"`
	ProviderTurnID   string            `json:"providerTurnId,omitempty"`
	Error            string            `json:"error,omitempty"`
}

type contextRecord struct {
	ContextBatch
	Documents []ContextDocument `json:"documents,omitempty"`
}

type contextFile struct {
	SessionID string          `json:"sessionId"`
	RunID     string          `json:"runId"`
	ThreadID  string          `json:"threadId"`
	Closed    bool            `json:"closed,omitempty"`
	Batches   []contextRecord `json:"batches"`
}

// ContextQueue is a private run control file, independent of the desktop DB and
// provider transcripts. The service and runner serialize publication with the
// same interprocess lock; a service restart leaves the independent runner live.
type ContextQueue struct {
	Directory, SessionID, RunID, ThreadID string
}

// Initialize belongs to runner readiness. Ordinary queue operations never
// recreate missing receipts, because a lost file could hide a prior delivery.
func (queue ContextQueue) Initialize(ctx context.Context) error {
	return queue.access(ctx, true, func(*contextFile) (bool, error) { return true, nil })
}

func (queue ContextQueue) update(ctx context.Context, change func(*contextFile) (bool, error)) error {
	return queue.access(ctx, false, change)
}

func (queue ContextQueue) access(ctx context.Context, initialize bool, change func(*contextFile) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := os.Lstat(queue.Directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrUnavailable
	}
	lockPath := filepath.Join(queue.Directory, "context.lock")
	if info, err := os.Lstat(lockPath); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return ErrUnavailable
		}
	} else if !os.IsNotExist(err) {
		return ErrUnavailable
	}
	var lock *os.File
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lock, err = filelock.TryLock(lockPath)
		if err == nil {
			break
		}
		// Only contention is retried. Permission and invalid path errors must
		// remain visible rather than becoming an unbounded polling loop.
		if !contextLockContended(err) {
			return ErrUnavailable
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer lock.Close()
	path := filepath.Join(queue.Directory, "context.json")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return ErrUnavailable
		}
	} else if !os.IsNotExist(err) {
		return ErrUnavailable
	}
	state := contextFile{SessionID: queue.SessionID, RunID: queue.RunID, ThreadID: queue.ThreadID, Batches: []contextRecord{}}
	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		if initialize {
			return ErrUnavailable
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxContextFileBytes {
			return ErrUnavailable
		}
		decoder := json.NewDecoder(io.LimitReader(file, maxContextFileBytes))
		decoder.DisallowUnknownFields()
		// Existing state must carry its own complete identity. Decoding into
		// the missing-file defaults would treat null or {} as an empty queue.
		var persisted contextFile
		if decoder.Decode(&persisted) != nil || decoder.Decode(new(any)) != io.EOF ||
			persisted.SessionID == "" || persisted.RunID == "" || persisted.ThreadID == "" || persisted.Batches == nil {
			return ErrUnavailable
		}
		state = persisted
	} else if !os.IsNotExist(err) || !initialize {
		return ErrUnavailable
	}
	if state.SessionID != queue.SessionID || state.RunID != queue.RunID || state.ThreadID != queue.ThreadID || len(state.Batches) > maxContextBatches {
		return ErrContextConflict
	}
	if !validContextRecords(state) {
		return ErrUnavailable
	}
	changed, err := change(&state)
	if err != nil || !changed {
		return err
	}
	if state.SessionID == "" || state.RunID == "" || state.ThreadID == "" || state.Batches == nil || !validContextRecords(state) {
		return ErrUnavailable
	}
	data, err := json.Marshal(state)
	if err != nil || len(data) > maxContextFileBytes || atomicfile.Write(path, append(data, '\n'), 0600) != nil {
		return ErrUnavailable
	}
	return nil
}

func validContextRecords(state contextFile) bool {
	ids, requests := make(map[string]bool), make(map[string]bool)
	pending := 0
	for _, record := range state.Batches {
		if !contextIdentifier(record.ID) || ids[record.ID] || !contextIdentifier(record.RequestID) ||
			strings.TrimSpace(record.RequestID) != record.RequestID || requests[record.RequestID] ||
			record.SessionID != state.SessionID || record.RunID != state.RunID || record.ProviderThreadID != state.ThreadID ||
			len(record.Snapshots) < 1 || len(record.Snapshots) > MaxContextSnapshots {
			return false
		}
		ids[record.ID], requests[record.RequestID] = true, true
		switch record.Status {
		case "pending":
			pending++
			if state.Closed || pending > maxPendingBatches || len(record.Documents) != len(record.Snapshots) || record.ProviderTurnID != "" {
				return false
			}
		case "accepted":
			if record.ProviderTurnID == "" || len(record.Documents) != 0 {
				return false
			}
		case "unknown", "failed", "cancelled":
			if record.ProviderTurnID != "" || len(record.Documents) != 0 {
				return false
			}
		default:
			return false
		}
		snapshots := make(map[string]bool)
		bytes := 0
		for i, snapshot := range record.Snapshots {
			digest, err := hex.DecodeString(snapshot.SHA256)
			if !contextIdentifier(snapshot.ID) || snapshots[snapshot.ID] || !contextIdentifier(snapshot.DatabaseID) ||
				!contextIdentifier(snapshot.EntryID) || snapshot.Revision < 1 || snapshot.Name == "" || !utf8.ValidString(snapshot.Name) ||
				err != nil || len(digest) != sha256.Size {
				return false
			}
			snapshots[snapshot.ID] = true
			if record.Status == "pending" {
				document := record.Documents[i]
				bytes += len(document.Content)
				digest := sha256.Sum256([]byte(document.Content))
				if document.ContextSnapshot != snapshot || !utf8.ValidString(document.Content) ||
					bytes > MaxContextBytes || hex.EncodeToString(digest[:]) != snapshot.SHA256 {
					return false
				}
			}
		}
	}
	return true
}

func contextIdentifier(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func (queue ContextQueue) List(ctx context.Context) ([]ContextBatch, error) {
	batches := []ContextBatch{}
	err := queue.update(ctx, func(state *contextFile) (bool, error) {
		for _, record := range state.Batches {
			batches = append(batches, record.ContextBatch)
		}
		return false, nil
	})
	return batches, err
}

// Enqueue makes a replay recover its original receipt before considering queue
// capacity or run state. Request IDs cannot be reused for different snapshots.
func (queue ContextQueue) Enqueue(ctx context.Context, requestID string, documents []ContextDocument, active bool) (ContextBatch, error) {
	var batch ContextBatch
	err := queue.update(ctx, func(state *contextFile) (bool, error) {
		snapshots := make([]ContextSnapshot, 0, len(documents))
		for _, document := range documents {
			snapshots = append(snapshots, document.ContextSnapshot)
		}
		pending := 0
		for _, record := range state.Batches {
			if record.RequestID == requestID {
				if !reflect.DeepEqual(record.Snapshots, snapshots) {
					return false, ErrContextConflict
				}
				batch = record.ContextBatch
				return false, nil
			}
			if record.Status == "pending" {
				pending++
			}
		}
		if !active || state.Closed {
			return false, ErrContextState
		}
		if len(state.Batches) >= maxContextBatches || pending >= maxPendingBatches {
			return false, ErrContextLimit
		}
		if len(documents) < 1 || len(documents) > MaxContextSnapshots {
			return false, ErrContextLimit
		}
		bytes := 0
		for _, document := range documents {
			bytes += len(document.Content)
		}
		if bytes > MaxContextBytes {
			return false, ErrContextLimit
		}
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return false, ErrUnavailable
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		batch = ContextBatch{ID: hex.EncodeToString(nonce), RequestID: requestID, SessionID: queue.SessionID, RunID: queue.RunID, ProviderThreadID: queue.ThreadID, Status: "pending", Snapshots: snapshots, CreatedAt: now, UpdatedAt: now}
		state.Batches = append(state.Batches, contextRecord{ContextBatch: batch, Documents: documents})
		return true, nil
	})
	return batch, err
}

func (queue ContextQueue) Cancel(ctx context.Context, id string) (ContextBatch, error) {
	var batch ContextBatch
	err := queue.update(ctx, func(state *contextFile) (bool, error) {
		for i := range state.Batches {
			record := &state.Batches[i]
			if record.ID != id {
				continue
			}
			if record.Status == "cancelled" {
				batch = record.ContextBatch
				return false, nil
			}
			if record.Status != "pending" {
				return false, ErrContextState
			}
			record.Status, record.UpdatedAt, record.Documents = "cancelled", time.Now().UTC().Format(time.RFC3339Nano), nil
			batch = record.ContextBatch
			return true, nil
		}
		return false, ErrContextState
	})
	return batch, err
}

// Close prevents further consumption before the service ends this exact run.
// An attempted delivery stays unknown unless its provider acknowledgment arrives.
func (queue ContextQueue) Close(ctx context.Context) error {
	return queue.update(ctx, func(state *contextFile) (bool, error) {
		changed := !state.Closed
		state.Closed = true
		for i := range state.Batches {
			record := &state.Batches[i]
			if record.Status == "pending" {
				record.Status, record.Error = "failed", "The addressed Codex run ended before delivery."
				record.UpdatedAt, record.Documents = time.Now().UTC().Format(time.RFC3339Nano), nil
				changed = true
			}
		}
		return changed, nil
	})
}

// claim durably marks attempts unknown before any provider write. If the
// process dies between publication and the write, no successor replays them.
func (queue ContextQueue) claim(ctx context.Context) ([]ContextDocument, []string, error) {
	documents, ids := []ContextDocument{}, []string{}
	err := queue.update(ctx, func(state *contextFile) (bool, error) {
		if state.Closed {
			return false, nil
		}
		for i := range state.Batches {
			record := &state.Batches[i]
			if record.Status != "pending" {
				continue
			}
			documents = append(documents, record.Documents...)
			ids = append(ids, record.ID)
			record.Status, record.Error = "unknown", "Codex delivery has not been confirmed. Do not automatically resend this snapshot."
			record.UpdatedAt, record.Documents = time.Now().UTC().Format(time.RFC3339Nano), nil
		}
		return len(ids) > 0, nil
	})
	return documents, ids, err
}

func (queue ContextQueue) acknowledge(ctx context.Context, ids []string, turnID, status string, reason ...string) error {
	return queue.update(ctx, func(state *contextFile) (bool, error) {
		changed := false
		for i := range state.Batches {
			record := &state.Batches[i]
			for _, id := range ids {
				if record.ID != id || record.Status != "unknown" {
					continue
				}
				record.Status, record.ProviderTurnID = status, turnID
				if status != "unknown" {
					record.Error = ""
				}
				if status == "failed" {
					record.Error = "Codex rejected the message containing this document snapshot."
					if len(reason) > 0 {
						record.Error = reason[0]
					}
				}
				record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
				changed = true
			}
		}
		return changed, nil
	})
}

func contextText(document ContextDocument) string {
	metadata, _ := json.Marshal(document.ContextSnapshot)
	return fmt.Sprintf("AXV document snapshot (user-provided reference material). Source metadata: %s\n\n%s", metadata, document.Content)
}
