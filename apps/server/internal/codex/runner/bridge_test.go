package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testThreadID = "12345678-1234-1234-1234-123456789abc"
const otherThreadID = "12345678-1234-1234-1234-123456789def"

type messageWriter struct{ messages chan []byte }

func (writer messageWriter) Write(data []byte) (int, error) {
	writer.messages <- append([]byte(nil), data...)
	return len(data), nil
}

type bridgeHarness struct {
	ctx      context.Context
	client   *websocket.Conn
	upstream chan []byte
	server   *io.PipeWriter
	done     chan error
	finished chan struct{}
	path     string
}

func newBridgeHarness(t *testing.T, mode string, configure ...func(*Manifest, string)) *bridgeHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	reader, writer := io.Pipe()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	harness := &bridgeHarness{
		ctx: ctx, upstream: make(chan []byte, 20), server: writer,
		done: make(chan error, 1), finished: make(chan struct{}), path: filepath.Join(directory, "state.json"),
	}
	manifest := Manifest{SessionID: "session", RunID: "run", HistoryHome: "/private/history", Mode: mode}
	if mode == ModeResume {
		manifest.ThreadID = testThreadID
	}
	for _, apply := range configure {
		apply(&manifest, directory)
	}
	if err := writeState(harness.path, State{RunID: "run", HistoryHome: manifest.HistoryHome, ThreadID: manifest.ThreadID, Status: StatusStarting}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer close(harness.finished)
		client, err := websocket.Accept(response, request, nil)
		if err != nil {
			harness.done <- err
			return
		}
		harness.done <- newBridge(manifest, harness.path, messageWriter{harness.upstream}, client).run(ctx, reader)
	}))
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	harness.client = client
	t.Cleanup(func() {
		cancel()
		_ = client.CloseNow()
		_ = writer.Close()
		_ = reader.Close()
		server.Close()
		select {
		case <-harness.finished:
		case <-time.After(2 * time.Second):
			t.Error("bridge cleanup did not finish")
		}
	})
	return harness
}

func (harness *bridgeHarness) send(t *testing.T, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil || harness.client.Write(harness.ctx, websocket.MessageText, data) != nil {
		t.Fatalf("send client message: %v", err)
	}
}

func (harness *bridgeHarness) upstreamMessage(t *testing.T) wireMessage {
	t.Helper()
	select {
	case data := <-harness.upstream:
		var message wireMessage
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		return message
	case <-harness.ctx.Done():
		t.Fatal("no upstream message")
		return wireMessage{}
	}
}

func (harness *bridgeHarness) reply(t *testing.T, value any) {
	t.Helper()
	if err := writeMessage(harness.server, value); err != nil {
		t.Fatal(err)
	}
}

func (harness *bridgeHarness) receive(t *testing.T) wireMessage {
	t.Helper()
	_, data, err := harness.client.Read(harness.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var message wireMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func stateAt(t *testing.T, path string) State {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func threadResponse(id any, threadID string) map[string]any {
	return map[string]any{"id": id, "result": map[string]any{"thread": map[string]string{"id": threadID}}}
}

func TestNewConversationPersistsBeforeForwardingIdentity(t *testing.T) {
	harness := newBridgeHarness(t, ModeNew)
	harness.send(t, map[string]any{"id": 1, "method": "thread/start", "params": map[string]any{"ephemeral": false}})
	if got := harness.upstreamMessage(t).Method; got != "thread/start" {
		t.Fatalf("upstream method = %q", got)
	}
	// Neither global notifications nor server approval requests with a reused
	// numeric request ID are evidence of this terminal's conversation.
	harness.reply(t, map[string]any{"method": "thread/started", "params": map[string]any{"thread": map[string]string{"id": otherThreadID}}})
	if got := harness.receive(t).Method; got != "thread/started" {
		t.Fatalf("notification method = %q", got)
	}
	harness.reply(t, map[string]any{"id": 1, "method": "item/commandExecution/requestApproval", "params": map[string]any{}})
	if got := harness.receive(t).Method; got != "item/commandExecution/requestApproval" {
		t.Fatalf("approval method = %q", got)
	}
	harness.reply(t, threadResponse(1, testThreadID))
	persist := harness.upstreamMessage(t)
	if persist.Method != "thread/name/set" || !strings.Contains(string(persist.Params), testThreadID) {
		t.Fatalf("persist request = %+v", persist)
	}
	if got := stateAt(t, harness.path); got.Status != StatusStarting || got.ThreadID != "" {
		t.Fatalf("unconfirmed identity was published: %+v", got)
	}
	harness.reply(t, map[string]any{"method": "thread/status/changed", "params": map[string]string{"threadId": testThreadID}})
	harness.reply(t, map[string]any{"id": persist.ID, "result": map[string]any{}})
	if got := harness.receive(t); responseThreadID(got.Result) != testThreadID {
		t.Fatalf("start response = %+v", got)
	}
	if got := stateAt(t, harness.path); got.Status != StatusReady || got.ThreadID != testThreadID {
		t.Fatalf("identity not persisted before response: %+v", got)
	}
	if info, err := os.Stat(harness.path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file permissions: %v, %v", info, err)
	}
	if got := harness.receive(t).Method; got != "thread/status/changed" {
		t.Fatalf("buffered notification = %q", got)
	}
}

func TestManagedConversationRejectsIdentityChanges(t *testing.T) {
	harness := newBridgeHarness(t, ModeResume)
	requests := []map[string]any{
		{"method": "thread/start", "params": map[string]any{}},
		{"method": "thread/fork", "params": map[string]string{"threadId": testThreadID}},
		{"method": "thread/resume", "params": map[string]string{"threadId": otherThreadID}},
		{"method": "thread/resume", "params": map[string]string{"threadId": testThreadID, "path": "/other/history"}},
		{"method": "thread/resume", "params": map[string]any{"threadId": testThreadID, "history": []any{}}},
	}
	for index, request := range requests {
		request["id"] = index + 1
		harness.send(t, request)
		response := harness.receive(t)
		if !strings.Contains(string(response.Error), "Use New Session in AXV") {
			t.Fatalf("identity change was not rejected: %+v", response)
		}
		select {
		case unexpected := <-harness.upstream:
			t.Fatalf("rejected request reached server: %s", unexpected)
		default:
		}
	}
	harness.send(t, map[string]any{"id": 10, "method": "thread/resume", "params": map[string]any{"threadId": testThreadID, "path": nil, "history": nil}})
	if got := harness.upstreamMessage(t).Method; got != "thread/resume" {
		t.Fatalf("exact resume method = %q", got)
	}
	harness.reply(t, threadResponse(10, testThreadID))
	if got := harness.receive(t); responseThreadID(got.Result) != testThreadID {
		t.Fatalf("resume response = %+v", got)
	}
	if got := stateAt(t, harness.path); got.Status != StatusReady || got.ThreadID != testThreadID {
		t.Fatalf("resume state = %+v", got)
	}
}

func TestPersistenceFailureDoesNotPublishCandidateIdentity(t *testing.T) {
	harness := newBridgeHarness(t, ModeNew)
	harness.send(t, map[string]any{"id": "start", "method": "thread/start", "params": map[string]any{}})
	_ = harness.upstreamMessage(t)
	harness.reply(t, threadResponse("start", testThreadID))
	persist := harness.upstreamMessage(t)
	harness.reply(t, map[string]any{"id": persist.ID, "error": map[string]any{"code": -1, "message": "write failed"}})
	select {
	case err := <-harness.done:
		if err == nil {
			t.Fatal("persistence failure succeeded")
		}
	case <-harness.ctx.Done():
		t.Fatal("bridge did not stop")
	}
	if got := stateAt(t, harness.path); got.Status != StatusFailed || got.ThreadID != "" || got.ErrorCode != "codex_persist_failed" {
		t.Fatalf("failure state = %+v", got)
	}
}

func TestResumeRejectsWrongReturnedIdentityAndPreservesKnownIdentity(t *testing.T) {
	harness := newBridgeHarness(t, ModeResume)
	harness.send(t, map[string]any{"id": 1, "method": "thread/resume", "params": map[string]string{"threadId": testThreadID}})
	_ = harness.upstreamMessage(t)
	harness.reply(t, threadResponse(1, otherThreadID))
	select {
	case err := <-harness.done:
		if err == nil {
			t.Fatal("wrong conversation succeeded")
		}
	case <-harness.ctx.Done():
		t.Fatal("bridge did not stop")
	}
	if got := stateAt(t, harness.path); got.Status != StatusFailed || got.ThreadID != testThreadID || got.ErrorCode != "codex_protocol_error" {
		t.Fatalf("failure state = %+v", got)
	}
}

func TestWriteFailurePreservesOnlyMatchingDurableIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	ready := State{RunID: "run", HistoryHome: "/history", ThreadID: testThreadID, Status: StatusReady, ContextVersion: 1}
	if err := writeState(path, ready); err != nil {
		t.Fatal(err)
	}
	writeFailure(path, State{RunID: "run", HistoryHome: "/history"}, "codex_runtime_failed")
	if got := stateAt(t, path); got.ThreadID != testThreadID || got.Status != StatusFailed || got.ContextVersion != 1 {
		t.Fatalf("lost confirmed identity: %+v", got)
	}
	writeFailure(path, State{RunID: "next-run", HistoryHome: "/history"}, "codex_start_failed")
	if got := stateAt(t, path); got.ThreadID != "" {
		t.Fatalf("different run adopted identity: %+v", got)
	}
}
