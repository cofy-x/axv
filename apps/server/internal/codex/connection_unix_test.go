//go:build darwin || linux

package codex

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type inputTestProcess struct{ write func([]byte) (int, error) }

func (p inputTestProcess) Read([]byte) (int, error)    { return 0, io.EOF }
func (p inputTestProcess) Write(b []byte) (int, error) { return p.write(b) }
func (inputTestProcess) Resize(int, int) error         { return nil }
func (inputTestProcess) Done() <-chan struct{}         { return nil }
func (inputTestProcess) ExitError() error              { return nil }
func (inputTestProcess) Close()                        {}

func TestInputFramesSerializePerRunAndCancelWaitingAttachments(t *testing.T) {
	m := &Manager{}
	entered, unblock := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	defer release()
	var output bytes.Buffer
	var outputMu sync.Mutex
	writeByte := func(b []byte) (int, error) {
		outputMu.Lock()
		defer outputMu.Unlock()
		return output.Write(b[:1])
	}
	var firstWrite sync.Once
	first := inputTestProcess{write: func(b []byte) (int, error) {
		firstWrite.Do(func() { close(entered); <-unblock })
		return writeByte(b)
	}}
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		firstDone <- m.applyFrame(context.Background(), first, "", "shared-run", inputFrame{data: []byte("AAA")})
	}()
	<-entered
	second := inputTestProcess{write: writeByte}
	go func() {
		secondDone <- m.applyFrame(context.Background(), second, "", "shared-run", inputFrame{data: []byte("BBB")})
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		cancelled <- m.applyFrame(ctx, second, "", "shared-run", inputFrame{data: []byte("must not write")})
	}()
	cancel()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued input did not cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued input remained blocked after cancellation")
	}
	independent := make(chan error, 1)
	go func() {
		independent <- m.applyFrame(context.Background(), inputTestProcess{write: func(b []byte) (int, error) { return len(b), nil }}, "", "other-run", inputFrame{data: []byte("independent")})
	}()
	select {
	case err := <-independent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("input to another run was blocked")
	}
	release()
	for _, done := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("input did not complete after the previous frame")
		}
	}
	if got := output.String(); got != "AAABBB" {
		t.Fatalf("terminal input frames interleaved: %q", got)
	}
}

func TestAttachmentInspectionDoesNotHoldManagerLock(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery")
	}
	m := testManager(t)
	run := reservedRunning(t, m, "attachment-inspection")
	entered := make(chan struct{})
	m.run = func(ctx context.Context, _ string, _ ...string) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	server := serveManager(t, m)
	requested := make(chan error, 1)
	go func() {
		request, _ := http.NewRequest("GET", server.URL+runPath(run)+"/terminal", nil)
		request.Header.Set("Origin", server.URL)
		request.Header.Set("X-AXV-Database-ID", server.databaseID)
		response, err := server.Client().Do(request)
		if response != nil {
			response.Body.Close()
		}
		requested <- err
	}()
	<-entered
	client := &http.Client{Timeout: time.Second}
	request, _ := http.NewRequest("GET", server.URL+"/api/host/codex/projects", nil)
	request.Header.Set("X-AXV-Database-ID", server.databaseID)
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err != nil || response.StatusCode != http.StatusOK {
		m.cancel()
		t.Fatalf("an attaching view blocked other handlers: response=%v error=%v", response, err)
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel attachment inspection")
	}
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("cancelled attachment handler did not return")
	}
}

func TestPendingAttachmentsReserveCapacityAndDrainOnShutdown(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery")
	}
	m := testManager(t)
	run := reservedRunning(t, m, "pending-attachments")
	// These connections stop at the input gate before launching an attachment.
	// Native tmux survival is covered by the real attachment lifecycle tests.
	m.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if !slices.Contains(args, "list-panes") {
			t.Errorf("pending attachment unexpectedly changed the runtime: %v", args)
			return "", errors.New("unexpected runtime command")
		}
		return run.TmuxSession + "\t0\t0:", nil
	}
	server := serveManager(t, m)
	status, _ := jsonRequest(t, server, "GET", runPath(run)+"/terminal", nil)
	if status == http.StatusSwitchingProtocols {
		t.Fatal("accepted a request without WebSocket upgrade headers")
	}
	m.mu.Lock()
	remaining := m.attachments
	m.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("failed upgrade retained %d reservations", remaining)
	}
	unlock, err := m.inputGates.acquire(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var unlockOnce sync.Once
	release := func() { unlockOnce.Do(unlock) }
	t.Cleanup(release)
	m.copyModes.Store(run.RunID, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + runPath(run) + "/terminal"
	options := &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{server.URL}, "X-AXV-Database-ID": []string{server.databaseID}}}
	for range 8 {
		conn, response, err := websocket.Dial(ctx, url, options)
		if err != nil {
			t.Fatalf("reserve attachment: %v response=%v", err, response)
		}
		t.Cleanup(func() { conn.CloseNow() })
	}
	eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.attachments == 8 && len(m.connections[run.RunID]) == 8
	})
	conn, response, err := websocket.Dial(ctx, url, options)
	if conn != nil {
		conn.CloseNow()
	}
	if response != nil {
		response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("pending connections exceeded capacity: response=%v error=%v", response, err)
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("shutdown waited for a held input gate instead of cancelling pending attachments")
	}
	m.mu.Lock()
	remaining, connected := m.attachments, len(m.connections)
	m.mu.Unlock()
	if remaining != 0 || connected != 0 {
		t.Fatalf("shutdown retained %d reservations and %d run connections", remaining, connected)
	}
	if _, retained := m.copyModes.Load(run.RunID); retained {
		t.Fatal("last attachment retained scrollback cache state")
	}
	stored, err := m.store.CodexRunByID(context.Background(), run.RunID)
	if err != nil || stored.Status != "running" {
		t.Fatalf("attachment shutdown changed the persistent run: %+v, %v", stored, err)
	}
	release()
}
