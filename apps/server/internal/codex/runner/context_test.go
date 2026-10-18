//go:build darwin || linux

package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/filelock"
)

func testContextDocument() ContextDocument {
	content := "# Immutable brief\nReference content."
	digest := sha256.Sum256([]byte(content))
	return ContextDocument{ContextSnapshot: ContextSnapshot{ID: "snapshot", DatabaseID: "database", EntryID: "document", Revision: 3, Name: "brief.md", SHA256: hex.EncodeToString(digest[:])}, Content: content}
}

func TestContextQueueRejectsInvalidStateAndCancelsLockWait(t *testing.T) {
	ctx := context.Background()
	queue := initializedContextQueue(t)
	lock, err := filelock.TryLock(filepath.Join(queue.Directory, "context.lock"))
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err := queue.List(blocked); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock contention ignored caller deadline: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(queue.Directory, "context.json")
	if err := os.WriteFile(path, []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, "invalid-state", []ContextDocument{testContextDocument()}, true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid queue was reset: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "not-json" {
		t.Fatalf("invalid queue was overwritten: %q %v", data, err)
	}
}

func TestContextQueueRejectsSemanticCorruptionWithoutChangingPersistedState(t *testing.T) {
	ctx := context.Background()
	queue := initializedContextQueue(t)
	batch, err := queue.Enqueue(ctx, "request", []ContextDocument{testContextDocument()}, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(queue.Directory, "context.json")
	baseline, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		raw    string
		change func(*contextFile)
	}{
		{name: "null", raw: "null"},
		{name: "empty object", raw: "{}"},
		{name: "missing scope", raw: `{"batches":[]}`},
		{name: "missing batches", raw: `{"sessionId":"session","runId":"run","threadId":"` + testThreadID + `"}`},
		{name: "record session", change: func(state *contextFile) { state.Batches[0].SessionID = "other-session" }},
		{name: "record run", change: func(state *contextFile) { state.Batches[0].RunID = "other-run" }},
		{name: "record provider thread", change: func(state *contextFile) { state.Batches[0].ProviderThreadID = otherThreadID }},
		{name: "duplicate batch ID", change: func(state *contextFile) {
			duplicate := state.Batches[0]
			duplicate.RequestID = "other-request"
			state.Batches = append(state.Batches, duplicate)
		}},
		{name: "duplicate request ID", change: func(state *contextFile) {
			duplicate := state.Batches[0]
			duplicate.ID = "other-batch"
			state.Batches = append(state.Batches, duplicate)
		}},
		{name: "unknown status", change: func(state *contextFile) { state.Batches[0].Status = "invalid-status" }},
		{name: "missing pending content", change: func(state *contextFile) { state.Batches[0].Documents = nil }},
		{name: "changed pending metadata", change: func(state *contextFile) { state.Batches[0].Documents[0].Name = "different.md" }},
		{name: "changed pending content", change: func(state *contextFile) { state.Batches[0].Documents[0].Content = "altered snapshot body" }},
		{name: "invalid snapshot digest", change: func(state *contextFile) { state.Batches[0].Snapshots[0].SHA256 = "invalid-digest" }},
		{name: "terminal content retained", change: func(state *contextFile) { state.Batches[0].Status = "unknown" }},
		{name: "accepted without provider proof", change: func(state *contextFile) { state.Batches[0].Status = "accepted"; state.Batches[0].Documents = nil }},
		{name: "closed with pending", change: func(state *contextFile) { state.Closed = true }},
		{name: "pending bound exceeded", change: func(state *contextFile) {
			for i := 0; i < maxPendingBatches; i++ {
				duplicate := state.Batches[0]
				duplicate.ID = string(rune('a' + i))
				duplicate.RequestID = duplicate.ID
				state.Batches = append(state.Batches, duplicate)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.raw)
			if tc.change != nil {
				var state contextFile
				if err := json.Unmarshal(baseline, &state); err != nil {
					t.Fatal(err)
				}
				tc.change(&state)
				data, err = json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := queue.List(ctx); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("invalid receipt was exposed: %v", err)
			}
			if _, err := queue.Cancel(ctx, batch.ID); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("invalid receipt was cancelled as if valid: %v", err)
			}
			if _, _, err := queue.claim(ctx); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("invalid context was claimed for delivery: %v", err)
			}
			if persisted, err := os.ReadFile(path); err != nil || string(persisted) != string(data) {
				t.Fatalf("invalid state was silently replaced: %q %v", persisted, err)
			}
		})
	}
}

func TestContextQueueMissingUnboundStateCannotBeReadOrPublished(t *testing.T) {
	queue := ContextQueue{Directory: privateContextDirectory(t), SessionID: "session", RunID: "run"}
	if batches, err := queue.List(context.Background()); !errors.Is(err, ErrUnavailable) || len(batches) != 0 {
		t.Fatalf("uninitialized queue was treated as empty: %+v %v", batches, err)
	}
	if err := queue.Close(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unbound queue was published: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queue.Directory, "context.json")); !os.IsNotExist(err) {
		t.Fatalf("unbound state was persisted: %v", err)
	}
}

func TestBridgeInvalidContextFailsExplicitlyWithoutForwardingUserTurn(t *testing.T) {
	harness, queue := readyContextHarness(t)
	path := filepath.Join(queue.Directory, "context.json")
	if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	harness.send(t, map[string]any{"id": "user-turn", "method": "turn/start", "params": map[string]any{"threadId": testThreadID, "input": []any{map[string]string{"type": "text", "text": "Run tests"}}}})
	select {
	case err := <-harness.done:
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("corrupt context did not fail the bridge: %v", err)
		}
	case <-harness.ctx.Done():
		t.Fatal("corrupt context stalled the bridge")
	}
	select {
	case data := <-harness.upstream:
		t.Fatalf("corrupt context silently forwarded the user turn: %s", data)
	default:
	}
	if state := stateAt(t, harness.path); state.Status != StatusFailed || state.ErrorCode != "codex_context_state_failed" {
		t.Fatalf("context failure was not visible in runner state: %+v", state)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "null" {
		t.Fatalf("corrupt context was reset: %q %v", data, err)
	}
}

func privateContextDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func initializedContextQueue(t *testing.T) ContextQueue {
	t.Helper()
	queue := ContextQueue{Directory: privateContextDirectory(t), SessionID: "session", RunID: "run", ThreadID: testThreadID}
	if err := queue.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return queue
}

func TestContextQueueMissingReceiptsCannotBecomeANewDelivery(t *testing.T) {
	ctx := context.Background()
	queue := initializedContextQueue(t)
	document := testContextDocument()
	batch, err := queue.Enqueue(ctx, "already-delivered", []ContextDocument{document}, true)
	if err != nil {
		t.Fatal(err)
	}
	_, ids, err := queue.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.acknowledge(ctx, ids, "accepted-turn", "accepted"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(queue.Directory, "context.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.List(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("lost receipts appeared as an empty queue: %v", err)
	}
	if _, err := queue.Enqueue(ctx, batch.RequestID, []ContextDocument{document}, true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("lost receipt allowed a duplicate delivery: %v", err)
	}
	if _, _, err := queue.claim(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing receipt state was consumed: %v", err)
	}
	if err := queue.Close(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing receipt state was silently closed as new: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ordinary operations recreated lost receipts: %v", err)
	}
}

func TestContextQueueInitializationNeverReplacesExistingReceipts(t *testing.T) {
	queue := initializedContextQueue(t)
	path := filepath.Join(queue.Directory, "context.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Initialize(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("existing queue was initialized twice: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("initialization changed existing receipts: %q %v", after, err)
	}
}

func TestBridgeCannotPublishReadyOverExistingQueueState(t *testing.T) {
	harness := newBridgeHarness(t, ModeResume)
	path := filepath.Join(filepath.Dir(harness.path), "context.json")
	const original = "unverifiable existing receipts"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	harness.send(t, map[string]any{"id": "resume", "method": "thread/resume", "params": map[string]any{"threadId": testThreadID}})
	harness.upstreamMessage(t)
	harness.reply(t, map[string]any{"id": "resume", "result": map[string]any{"thread": map[string]string{"id": testThreadID}}})
	select {
	case err := <-harness.done:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("queue initialization failure did not prevent readiness: %v", err)
		}
	case <-harness.ctx.Done():
		t.Fatal("queue initialization failure stalled readiness")
	}
	if state := stateAt(t, harness.path); state.Status != StatusFailed || state.ThreadID != testThreadID || state.ContextVersion != 0 || state.ErrorCode != "codex_context_state_failed" {
		t.Fatalf("failed initialization published readiness or lost provider identity: %+v", state)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != original {
		t.Fatalf("readiness replaced unverifiable receipts: %q %v", data, err)
	}
}

func TestBridgeReadinessPublishesQueueBeforeCapabilityAndKeepsMissingReceiptsUnavailable(t *testing.T) {
	harness, queue := readyContextHarness(t)
	state := stateAt(t, harness.path)
	if state.Status != StatusReady || state.ContextVersion != 1 {
		t.Fatalf("queue readiness proof was not published: %+v", state)
	}
	if batches, err := queue.List(harness.ctx); err != nil || len(batches) != 0 {
		t.Fatalf("ready runner did not initialize its queue: %+v %v", batches, err)
	}
	if err := os.Remove(filepath.Join(queue.Directory, "context.json")); err != nil {
		t.Fatal(err)
	}
	harness.send(t, map[string]any{"id": "missing-receipts", "method": "turn/start", "params": map[string]any{"threadId": testThreadID, "input": []any{map[string]string{"type": "text", "text": "Review documents"}}}})
	select {
	case err := <-harness.done:
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("missing queue did not stop dispatch: %v", err)
		}
	case <-harness.ctx.Done():
		t.Fatal("missing receipts stalled dispatch")
	}
	if state := stateAt(t, harness.path); state.Status != StatusFailed || state.ThreadID != testThreadID || state.ContextVersion != 1 || state.ErrorCode != "codex_context_state_failed" {
		t.Fatalf("failure lost queue readiness or provider identity: %+v", state)
	}
	select {
	case raw := <-harness.upstream:
		t.Fatalf("user turn was forwarded after receipt loss: %s", raw)
	default:
	}
}

func TestContextQueueSerializesReplaysCancelAndExactIdentity(t *testing.T) {
	ctx := context.Background()
	queue := initializedContextQueue(t)
	document := testContextDocument()
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for range 12 {
		wg.Go(func() {
			batch, err := queue.Enqueue(ctx, "same-request", []ContextDocument{document}, true)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- batch.ID
		})
	}
	wg.Wait()
	close(ids)
	var id string
	for candidate := range ids {
		if id != "" && candidate != id {
			t.Fatal("one request created multiple batches")
		}
		id = candidate
	}
	if id == "" {
		t.Fatal("no batch created")
	}
	other := document
	other.ID = "other-snapshot"
	if _, err := queue.Enqueue(ctx, "same-request", []ContextDocument{other}, true); !errors.Is(err, ErrContextConflict) {
		t.Fatalf("changed payload reused request ID: %v", err)
	}
	otherQueue := queue
	otherQueue.ThreadID = otherThreadID
	if _, err := otherQueue.List(ctx); !errors.Is(err, ErrContextConflict) {
		t.Fatalf("queue escaped its provider identity: %v", err)
	}
	batch, err := queue.Cancel(ctx, id)
	if err != nil || batch.Status != "cancelled" {
		t.Fatalf("cancel: %+v %v", batch, err)
	}
	if documents, ids, err := queue.claim(ctx); err != nil || len(documents) != 0 || len(ids) != 0 {
		t.Fatalf("cancelled content was consumed: %+v %v %v", documents, ids, err)
	}
	if err := queue.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, "new-after-close", []ContextDocument{document}, true); !errors.Is(err, ErrContextState) {
		t.Fatalf("closed queue accepted more context: %v", err)
	}
	if replay, err := queue.Enqueue(ctx, "same-request", []ContextDocument{document}, false); err != nil || replay.ID != id || replay.Status != "cancelled" {
		t.Fatalf("ended run lost idempotency: %+v %v", replay, err)
	}
}

func TestContextQueuePersistsAttemptBeforeDispatchAndDoesNotReplayUnknown(t *testing.T) {
	ctx := context.Background()
	queue := initializedContextQueue(t)
	document := testContextDocument()
	first, err := queue.Enqueue(ctx, "first", []ContextDocument{document}, true)
	if err != nil {
		t.Fatal(err)
	}
	documents, ids, err := queue.claim(ctx)
	if err != nil || len(documents) != 1 || len(ids) != 1 || ids[0] != first.ID {
		t.Fatalf("claim: %v %v %v", documents, ids, err)
	}
	// A new queue handle models a service/process restart after dispatch.
	restarted := queue
	batches, err := restarted.List(ctx)
	if err != nil || len(batches) != 1 || batches[0].Status != "unknown" {
		t.Fatalf("attempt was not durable: %+v %v", batches, err)
	}
	if _, _, err := restarted.claim(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := restarted.claim(ctx); len(got) != 0 {
		t.Fatal("unknown delivery was replayed")
	}
	if _, err := restarted.Cancel(ctx, first.ID); !errors.Is(err, ErrContextState) {
		t.Fatalf("uncertain dispatch was cancelled: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(queue.Directory, "context.json"))
	if err != nil || strings.Contains(string(data), "Immutable brief") {
		t.Fatalf("attempt retained duplicate document content: %v", err)
	}
	second, err := restarted.Enqueue(ctx, "second", []ContextDocument{document}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Close(ctx); err != nil {
		t.Fatal(err)
	}
	batches, err = restarted.List(ctx)
	if err != nil || batches[0].Status != "unknown" || batches[1].ID != second.ID || batches[1].Status != "failed" {
		t.Fatalf("run close lost delivery certainty: %+v %v", batches, err)
	}
}

func readyContextHarness(t *testing.T) (*bridgeHarness, ContextQueue) {
	t.Helper()
	harness := newBridgeHarness(t, ModeResume)
	if err := os.Chmod(filepath.Dir(harness.path), 0700); err != nil {
		t.Fatal(err)
	}
	harness.send(t, map[string]any{"id": 1, "method": "thread/resume", "params": map[string]string{"threadId": testThreadID}})
	_ = harness.upstreamMessage(t)
	harness.reply(t, threadResponse(1, testThreadID))
	_ = harness.receive(t)
	return harness, ContextQueue{Directory: filepath.Dir(harness.path), SessionID: "session", RunID: "run", ThreadID: testThreadID}
}

func TestBridgeAppendsSnapshotsOnlyToNextUserTurnAndRequiresProviderAck(t *testing.T) {
	harness, queue := readyContextHarness(t)
	document := testContextDocument()
	batch, err := queue.Enqueue(harness.ctx, "request", []ContextDocument{document}, true)
	if err != nil {
		t.Fatal(err)
	}
	// Tool-only turns and requests for other conversations cannot consume it.
	harness.send(t, map[string]any{"id": 2, "method": "turn/start", "params": map[string]any{"threadId": testThreadID, "input": []any{}, "toolOutput": map[string]string{"name": "test", "output": "done"}}})
	if got := harness.upstreamMessage(t); !strings.Contains(string(got.Params), "toolOutput") {
		t.Fatalf("tool output was changed: %+v", got)
	}
	harness.send(t, map[string]any{"id": 3, "method": "turn/start", "params": map[string]any{"threadId": otherThreadID, "input": []any{map[string]string{"type": "text", "text": "other"}}}})
	if got := harness.receive(t); len(got.Error) == 0 {
		t.Fatal("wrong thread was allowed")
	}
	if batches, _ := queue.List(harness.ctx); batches[0].Status != "pending" {
		t.Fatalf("context consumed before user turn: %+v", batches)
	}
	originalInput := []any{map[string]any{"type": "text", "text": "Run tests", "text_elements": []any{}}, map[string]string{"type": "image", "url": "https://example.test/image.png"}}
	params := map[string]any{"threadId": testThreadID, "input": originalInput, "model": "configured-model", "effort": "high", "approvalPolicy": "on-request", "sandboxPolicy": map[string]any{"type": "workspaceWrite", "networkAccess": false}, "collaborationMode": map[string]any{"mode": "default"}}
	harness.send(t, map[string]any{"id": 4, "method": "turn/start", "params": params})
	forwarded := harness.upstreamMessage(t)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(forwarded.Params, &got); err != nil {
		t.Fatal(err)
	}
	var input []json.RawMessage
	if err := json.Unmarshal(got["input"], &input); err != nil || len(input) != 3 {
		t.Fatalf("context was not appended: %s %v", got["input"], err)
	}
	var attached struct{ Type, Text string }
	if err := json.Unmarshal(input[2], &attached); err != nil || attached.Type != "text" || !strings.Contains(attached.Text, document.Content) || !strings.Contains(attached.Text, `"revision":3`) {
		t.Fatalf("snapshot content or source missing: %s %v", input[2], err)
	}
	for key, original := range params {
		if key == "input" {
			continue
		}
		want, _ := json.Marshal(original)
		var left, right any
		_ = json.Unmarshal(want, &left)
		_ = json.Unmarshal(got[key], &right)
		if !reflect.DeepEqual(left, right) {
			t.Fatalf("TUI override %s changed: %s -> %s", key, want, got[key])
		}
	}
	if batches, _ := queue.List(harness.ctx); batches[0].Status != "unknown" {
		t.Fatalf("dispatch counted as accepted: %+v", batches)
	}
	harness.reply(t, map[string]any{"id": 4, "method": "item/commandExecution/requestApproval", "params": map[string]any{}})
	_ = harness.receive(t)
	if batches, _ := queue.List(harness.ctx); batches[0].Status != "unknown" {
		t.Fatalf("approval request counted as acknowledgment: %+v", batches)
	}
	harness.reply(t, map[string]any{"id": 4, "result": map[string]any{"turn": map[string]string{"id": "turn-confirmed"}}})
	_ = harness.receive(t)
	if batches, _ := queue.List(harness.ctx); batches[0].ID != batch.ID || batches[0].Status != "accepted" || batches[0].ProviderTurnID != "turn-confirmed" {
		t.Fatalf("provider acknowledgment was lost: %+v", batches)
	}
	harness.send(t, map[string]any{"id": 5, "method": "turn/start", "params": map[string]any{"threadId": testThreadID, "input": []any{map[string]string{"type": "text", "text": "next"}}}})
	forwarded = harness.upstreamMessage(t)
	_ = json.Unmarshal(forwarded.Params, &got)
	_ = json.Unmarshal(got["input"], &input)
	if len(input) != 1 {
		t.Fatalf("accepted context was sent twice: %s", got["input"])
	}
}

func TestBridgeContextRejectAndDisconnectPreserveCertainty(t *testing.T) {
	for _, response := range []string{"rejected", "missing turn", "disconnect"} {
		t.Run(response, func(t *testing.T) {
			harness, queue := readyContextHarness(t)
			if _, err := queue.Enqueue(harness.ctx, "request", []ContextDocument{testContextDocument()}, true); err != nil {
				t.Fatal(err)
			}
			harness.send(t, map[string]any{"id": "turn", "method": "turn/start", "params": map[string]any{"threadId": testThreadID, "input": []any{map[string]string{"type": "text", "text": "run"}}}})
			_ = harness.upstreamMessage(t)
			if response == "rejected" {
				harness.reply(t, map[string]any{"id": "turn", "error": map[string]any{"code": -1, "message": "Rejected"}})
				_ = harness.receive(t)
			} else if response == "missing turn" {
				harness.reply(t, map[string]any{"id": "turn", "result": map[string]any{}})
				_ = harness.receive(t)
			} else {
				_ = harness.client.CloseNow()
				<-harness.done
			}
			batches, err := queue.List(context.Background())
			want := "unknown"
			if response == "rejected" {
				want = "failed"
			}
			if err != nil || len(batches) != 1 || batches[0].Status != want {
				t.Fatalf("delivery certainty lost: %+v %v", batches, err)
			}
		})
	}
}

func TestBridgeAppendsContextToExplicitSteerWithExactTurnPrecondition(t *testing.T) {
	harness, queue := readyContextHarness(t)
	document := testContextDocument()
	if _, err := queue.Enqueue(harness.ctx, "steer-context", []ContextDocument{document}, true); err != nil {
		t.Fatal(err)
	}
	input := []any{map[string]any{"type": "text", "text": "Focus on tests", "text_elements": []any{}}, map[string]string{"type": "localImage", "path": "/project/reference.png"}}
	// Missing required steer identity must reach the provider unchanged,
	// leaving documents available for the next valid user submission.
	harness.send(t, map[string]any{"id": 2, "method": "turn/steer", "params": map[string]any{"threadId": testThreadID, "input": input}})
	_ = harness.upstreamMessage(t)
	if batches, _ := queue.List(harness.ctx); batches[0].Status != "pending" {
		t.Fatalf("invalid steer consumed context: %+v", batches)
	}
	harness.send(t, map[string]any{"id": 3, "method": "turn/steer", "params": map[string]any{"threadId": otherThreadID, "expectedTurnId": "active-turn", "input": input}})
	if reply := harness.receive(t); len(reply.Error) == 0 {
		t.Fatal("steer escaped its managed conversation")
	}
	params := map[string]any{"threadId": testThreadID, "expectedTurnId": "active-turn", "clientUserMessageId": "user-message", "input": input}
	harness.send(t, map[string]any{"id": 4, "method": "turn/steer", "params": params})
	forwarded := harness.upstreamMessage(t)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(forwarded.Params, &got); err != nil {
		t.Fatal(err)
	}
	var augmented []json.RawMessage
	if forwarded.Method != "turn/steer" || json.Unmarshal(got["input"], &augmented) != nil || len(augmented) != 3 || !strings.Contains(string(augmented[2]), "Immutable brief") {
		t.Fatalf("steer input was not augmented: %+v", forwarded)
	}
	for key, original := range params {
		if key == "input" {
			continue
		}
		want, _ := json.Marshal(original)
		if string(got[key]) != string(want) {
			t.Fatalf("steer parameter %s changed: %s -> %s", key, want, got[key])
		}
	}
	for i, original := range input {
		want, _ := json.Marshal(original)
		if string(augmented[i]) != string(want) {
			t.Fatalf("original steer input changed: %s -> %s", want, augmented[i])
		}
	}
	if batches, _ := queue.List(harness.ctx); batches[0].Status != "unknown" {
		t.Fatalf("steer dispatch was counted as provider acceptance: %+v", batches)
	}
	harness.reply(t, map[string]any{"id": 4, "method": "item/commandExecution/requestApproval", "params": map[string]any{}})
	_ = harness.receive(t)
	if batches, _ := queue.List(harness.ctx); batches[0].Status != "unknown" {
		t.Fatalf("steer approval request counted as an acknowledgment: %+v", batches)
	}
	harness.reply(t, map[string]any{"id": 4, "result": map[string]string{"turnId": "active-turn"}})
	_ = harness.receive(t)
	if batches, _ := queue.List(harness.ctx); batches[0].Status != "accepted" || batches[0].ProviderTurnID != "active-turn" {
		t.Fatalf("matching steer acknowledgment was not recorded: %+v", batches)
	}
	harness.send(t, map[string]any{"id": 5, "method": "turn/start", "params": map[string]any{"threadId": testThreadID, "input": input}})
	forwarded = harness.upstreamMessage(t)
	_ = json.Unmarshal(forwarded.Params, &got)
	_ = json.Unmarshal(got["input"], &augmented)
	if len(augmented) != len(input) {
		t.Fatal("steered context was replayed into a later turn")
	}
}

func TestBridgeSteerAcknowledgmentPreservesFailedAndUnknownReceipts(t *testing.T) {
	for _, scenario := range []string{"stale expected turn", "missing turn ID", "different turn ID", "start response shape", "disconnect"} {
		t.Run(scenario, func(t *testing.T) {
			harness, queue := readyContextHarness(t)
			document := testContextDocument()
			if _, err := queue.Enqueue(harness.ctx, "steer-request", []ContextDocument{document}, true); err != nil {
				t.Fatal(err)
			}
			params := map[string]any{"threadId": testThreadID, "expectedTurnId": "expected-turn", "input": []any{map[string]string{"type": "text", "text": "steer"}}}
			harness.send(t, map[string]any{"id": "steer", "method": "turn/steer", "params": params})
			_ = harness.upstreamMessage(t)
			switch scenario {
			case "stale expected turn":
				harness.reply(t, map[string]any{"id": "steer", "error": map[string]any{"code": -32600, "message": "expectedTurnId does not match the active turn"}})
			case "missing turn ID":
				harness.reply(t, map[string]any{"id": "steer", "result": map[string]any{}})
			case "different turn ID":
				harness.reply(t, map[string]any{"id": "steer", "result": map[string]string{"turnId": "another-turn"}})
			case "start response shape":
				harness.reply(t, map[string]any{"id": "steer", "result": map[string]any{"turn": map[string]string{"id": "expected-turn"}}})
			case "disconnect":
				_ = harness.client.CloseNow()
				<-harness.done
			}
			if scenario != "disconnect" {
				_ = harness.receive(t)
			}
			want := "unknown"
			if scenario == "stale expected turn" {
				want = "failed"
			}
			batches, err := queue.List(context.Background())
			if err != nil || len(batches) != 1 || batches[0].Status != want || batches[0].ProviderTurnID != "" {
				t.Fatalf("steer response lost delivery certainty: %+v %v", batches, err)
			}
			if replay, err := queue.Enqueue(context.Background(), "steer-request", []ContextDocument{document}, true); err != nil || replay.Status != want {
				t.Fatalf("steer retry changed terminal receipt: %+v %v", replay, err)
			}
			if scenario != "disconnect" {
				harness.send(t, map[string]any{"id": "next-steer", "method": "turn/steer", "params": params})
				forwarded := harness.upstreamMessage(t)
				var got struct {
					Input []json.RawMessage `json:"input"`
				}
				if json.Unmarshal(forwarded.Params, &got) != nil || len(got.Input) != 1 {
					t.Fatalf("failed or unknown steer was automatically replayed: %s", forwarded.Params)
				}
			}
		})
	}
}
