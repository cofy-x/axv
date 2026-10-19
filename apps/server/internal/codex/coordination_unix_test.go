//go:build darwin || linux

package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func TestRunGatesCancelWaitersAndReleaseHistory(t *testing.T) {
	var gates runGates
	release, err := gates.acquire(context.Background(), "held")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() {
		unlock, err := gates.acquire(ctx, "held")
		if unlock != nil {
			unlock()
		}
		waiting <- err
	}()
	eventually(t, func() bool {
		gates.mu.Lock()
		defer gates.mu.Unlock()
		return gates.gates["held"].users == 2
	})
	cancel()
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting operation did not cancel: %v", err)
	}
	other, err := gates.acquire(context.Background(), "independent")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	var wg sync.WaitGroup
	value := 0
	for range 32 {
		wg.Go(func() {
			unlock, err := gates.acquire(context.Background(), "shared")
			if err != nil {
				t.Error(err)
				return
			}
			value++
			unlock()
		})
	}
	wg.Wait()
	if value != 32 || len(gates.gates) != 0 {
		t.Fatalf("lost serialization or retained completed runs: value=%d gates=%d", value, len(gates.gates))
	}
}

func TestShutdownCancelsHostHandlersAndRejectsNewWork(t *testing.T) {
	m := testManager(t)
	entered := make(chan struct{})
	m.probe = func(ctx context.Context) Info {
		close(entered)
		<-ctx.Done()
		return Info{Reason: "cancelled"}
	}
	mux := http.NewServeMux()
	m.Register(mux)
	r := httptest.NewRequest("GET", "/api/host/codex/info", nil)
	r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:44"
	handled := make(chan struct{})
	go func() {
		mux.ServeHTTP(httptest.NewRecorder(), r)
		close(handled)
	}()
	<-entered
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown waited behind a host operation instead of cancelling it")
	}
	<-handled
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("new handler entered after shutdown: %d %s", w.Code, w.Body.String())
	}
}

type observedBody struct {
	io.ReadCloser
	entered chan struct{}
	once    sync.Once
}

func (b *observedBody) Read(data []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	return b.ReadCloser.Read(data)
}

func TestShutdownInterruptsIncompleteRequestBody(t *testing.T) {
	m := testManager(t)
	mux := http.NewServeMux()
	m.Register(mux)
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = &observedBody{ReadCloser: r.Body, entered: entered}
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "POST /api/host/codex/sessions HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{", server.Listener.Addr(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not interrupt a partial request body")
	}
}

func reservedRunning(t *testing.T, m *Manager, request string) storage.CodexSession {
	t.Helper()
	ctx := context.Background()
	run, _, err := m.store.ReserveCodexRun(ctx, storage.CodexRunInput{Mode: "new", RequestID: request, ProjectPath: "/project", HistoryHome: "/history", NamePrefix: m.namePrefix, Socket: m.socket, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.store.BindCodexConversation(ctx, run.RunID, request, run.HistoryHome); err != nil {
		t.Fatal(err)
	}
	run, err = m.store.StartCodexRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestSlowRunInspectionDoesNotBlockMetadataOrAnotherRun(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery")
	}
	m := testManager(t)
	first, second := reservedRunning(t, m, "first"), reservedRunning(t, m, "second")
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	m.run = func(ctx context.Context, _ string, args ...string) (string, error) {
		for _, arg := range args {
			if arg == target(first)+":0" {
				once.Do(func() { close(entered) })
				select {
				case <-unblock:
				case <-ctx.Done():
					return "", ctx.Err()
				}
				return first.TmuxSession + "\t0\t0:", nil
			}
		}
		return "", nil
	}
	defer release()
	server := serveManager(t, m)
	inspected := make(chan error, 1)
	go func() {
		_, err := m.reconcile(m.ctx, first)
		inspected <- err
	}()
	<-entered
	client := &http.Client{Timeout: time.Second}
	request, _ := http.NewRequest("POST", server.URL+"/api/host/codex/sessions/"+second.ID+"/runs/"+second.RunID+"/end", nil)
	request.Header.Set("Origin", server.URL)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("another run was blocked by inspection: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("another run could not end: %d", response.StatusCode)
	}
	request, _ = http.NewRequest("PATCH", server.URL+"/api/host/codex/sessions/"+first.ID, strings.NewReader(`{"revision":1,"title":"metadata stays available"}`))
	request.Header.Set("Origin", server.URL)
	request.Header.Set("Content-Type", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("metadata request was blocked by inspection: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metadata request failed: %d", response.StatusCode)
	}
	// Cancellation must also release a handler queued on the blocked run.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.reconcile(ctx, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued inspection did not cancel: %v", err)
	}
	release()
	if err := <-inspected; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSignalExitKeepsAReadableFailure(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery")
	}
	m := testManager(t)
	run := reservedRunning(t, m, "signal-exit")
	m.run = func(_ context.Context, _ string, args ...string) (string, error) {
		for _, arg := range args {
			if arg == "list-panes" {
				return run.TmuxSession + "\t0\t1:", nil
			}
			if arg == "display-message" {
				return ":TERM", nil
			}
		}
		return "", nil
	}
	current, err := m.reconcile(context.Background(), run)
	if err != nil || current.Status != "failed" || !strings.HasPrefix(current.Reason, "Codex exited unexpectedly.") {
		t.Fatalf("signal exit did not keep a readable failure: %+v %v", current, err)
	}
}

func TestRuntimeWaitsForConfirmedExitOutcome(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery")
	}
	largestSignal := "64"
	if runtime.GOOS == "darwin" {
		largestSignal = "31"
	}
	for _, tc := range []struct {
		name, outcome, want string
	}{
		{"clean exit", "0:", "ended"},
		{"failed exit", "7:", "failed"},
		{"numeric signal", ":15", "failed"},
		{"largest platform signal", ":" + largestSignal, "failed"},
		{"status unavailable", ":", "running"},
		{"contradictory outcome", "7:TERM", "running"},
		{"invalid status", "unknown:", "running"},
		{"out of range status", "256:", "running"},
		{"invalid signal", ":TERM:extra", "running"},
		{"unknown signal name", ":unknown", "running"},
		{"invalid extended signal name", ":TERMextra", "running"},
		{"out of range signal", ":200", "running"},
		{"zero signal", ":0", "running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testManager(t)
			run := reservedRunning(t, m, "exit-outcome")
			killed := false
			inspections := 0
			m.run = func(_ context.Context, _ string, args ...string) (string, error) {
				for _, arg := range args {
					switch arg {
					case "list-panes":
						return run.TmuxSession + "\t0\t1:", nil
					case "display-message":
						inspections++
						if inspections == 1 {
							return ":", nil
						}
						return tc.outcome, nil
					case "kill-session":
						killed = true
					}
				}
				return "", nil
			}
			current, err := m.reconcile(context.Background(), run)
			if tc.want == "running" {
				var unavailable *Error
				if !errors.As(err, &unavailable) || unavailable.Code != "session_exit_unconfirmed" || killed {
					t.Fatalf("unknown exit outcome was finalized: %+v %v killed=%t", current, err, killed)
				}
			} else if err != nil || current.Status != tc.want || !killed {
				t.Fatalf("confirmed outcome was lost: %+v %v killed=%t", current, err, killed)
			}
			saved, readErr := m.store.CodexRunByID(context.Background(), run.RunID)
			if readErr != nil || saved.Status != tc.want {
				t.Fatalf("exit outcome persisted incorrectly: %+v %v", saved, readErr)
			}
			if tc.want == "running" {
				ended, endErr := m.End(context.Background(), run.ID, run.RunID)
				if endErr != nil || ended.Status != "ended" || !killed {
					t.Fatalf("uncertain exit blocked exact-run End: %+v %v", ended, endErr)
				}
			}
		})
	}
}
