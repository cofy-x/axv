package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/codex"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type bridgeTestAttachment struct {
	output  *io.PipeReader
	done    <-chan struct{}
	exitErr error
}

func (p bridgeTestAttachment) Read(b []byte) (int, error)           { return p.output.Read(b) }
func (bridgeTestAttachment) Write(context.Context, []byte) error    { return nil }
func (bridgeTestAttachment) Resize(context.Context, int, int) error { return nil }
func (bridgeTestAttachment) Scroll(context.Context, int) error      { return nil }
func (p bridgeTestAttachment) Done() <-chan struct{}                { return p.done }
func (p bridgeTestAttachment) ExitError() error                     { return p.exitErr }
func (p bridgeTestAttachment) Detach()                              { _ = p.output.Close() }

func TestBridgeReportsAttachmentFailureWithoutEndingActiveRun(t *testing.T) {
	for _, event := range []string{"output error", "attachment exit", "ended run"} {
		t.Run(event, func(t *testing.T) {
			run := codex.Session{CodexSession: storage.CodexSession{RunID: "bridge-run", Status: "running"}}
			if event == "ended run" {
				run.Status, run.Reason = "ended", "The fixture run ended."
			}
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			lease := bridgeTestAttachment{output: reader}
			if event == "attachment exit" {
				done := make(chan struct{})
				close(done)
				lease.done = done
				lease.exitErr = errors.New("fixture exit status 9")
			} else {
				_ = writer.CloseWithError(errors.New("fixture PTY read failure"))
			}
			var logs bytes.Buffer
			previousLogger := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previousLogger)
			finished := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				bridge(r.Context(), conn, lease, run.RunID, func(context.Context) (codex.Session, error) { return run, nil })
			}))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.CloseNow() })
			_, data, err := conn.Read(ctx)
			if err != nil || string(data) != `{"ready":true}` {
				t.Fatalf("missing ready control: %s %v", data, err)
			}
			kind, data, err := conn.Read(ctx)
			var control struct {
				Code    string `json:"code"`
				Error   string `json:"error"`
				Ended   bool   `json:"ended"`
				Message string `json:"message"`
			}
			if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &control) != nil {
				t.Fatalf("attachment closed without a readable control: %s %v", data, err)
			}
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("bridge did not drain after reporting the attachment result")
			}
			if event == "ended run" {
				if !control.Ended || control.Message != run.Reason || control.Code != "" || logs.Len() != 0 {
					t.Fatalf("normal run ending was reported as an attachment failure: %s logs=%s", data, &logs)
				}
			} else {
				if control.Code != "connection_failed" || control.Ended || !strings.Contains(control.Error, "still active") || !strings.Contains(control.Error, "Reconnect") {
					t.Fatalf("missing reconnect guidance: %s", data)
				}
				cause := "fixture PTY read failure"
				if event == "attachment exit" {
					cause = "fixture exit status 9"
				}
				if !strings.Contains(logs.String(), run.RunID) || !strings.Contains(logs.String(), cause) {
					t.Fatalf("missing attachment error diagnostic: %s", &logs)
				}
			}
		})
	}
}

func TestBridgeReportsRuntimeInspectionFailure(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		bridge(r.Context(), conn, bridgeTestAttachment{output: reader}, "uncertain-run", func(context.Context) (codex.Session, error) {
			return codex.Session{}, errors.New("The exact runtime could not be inspected.")
		})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != `{"ready":true}` {
		t.Fatalf("missing ready control: %s %v", data, err)
	}
	kind, data, err := conn.Read(ctx)
	var control struct {
		Code  string `json:"code"`
		Error string `json:"error"`
		Ended bool   `json:"ended"`
	}
	if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &control) != nil {
		t.Fatalf("inspection failure closed the connection without a control: %s %v", data, err)
	}
	if control.Code != "connection_failed" || control.Error != "The exact runtime could not be inspected." || control.Ended {
		t.Fatalf("runtime uncertainty was not reported as a connection error: %s", data)
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("bridge did not drain after an inspection failure")
	}
}

type drainingTestAttachment struct {
	bridgeTestAttachment
	readStarted chan struct{}
	detached    chan struct{}
	finishRead  chan struct{}
	released    chan struct{}
	once        sync.Once
}

func (p *drainingTestAttachment) Read([]byte) (int, error) {
	close(p.readStarted)
	<-p.detached
	<-p.finishRead
	return 0, io.EOF
}

func (p *drainingTestAttachment) Detach() { p.once.Do(func() { close(p.detached) }) }
func (p *drainingTestAttachment) Close()  { close(p.released) }

func TestBridgeDetachesAndDrainsBeforeCallerReleasesLease(t *testing.T) {
	lease := &drainingTestAttachment{
		readStarted: make(chan struct{}), detached: make(chan struct{}),
		finishRead: make(chan struct{}), released: make(chan struct{}),
	}
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(func() { close(lease.finishRead) }) }
	defer finish()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer lease.Close()
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		bridge(r.Context(), conn, lease, "draining-run", func(context.Context) (codex.Session, error) {
			return codex.Session{CodexSession: storage.CodexSession{Status: "running"}}, nil
		})
	}))
	defer func() { finish(); server.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lease.readStarted:
	case <-ctx.Done():
		t.Fatal("bridge did not start output forwarding")
	}
	_ = conn.CloseNow()
	select {
	case <-lease.detached:
	case <-ctx.Done():
		t.Fatal("bridge did not detach the terminal to interrupt output")
	}
	select {
	case <-lease.released:
		t.Fatal("lease capacity was released before output forwarding drained")
	default:
	}
	finish()
	select {
	case <-lease.released:
	case <-ctx.Done():
		t.Fatal("caller did not release the lease after the bridge drained")
	}
}

func TestStrictDimensions(t *testing.T) {
	for _, query := range []string{"cols=1", "cols=501", "rows=0", "rows=201", "rows=2&rows=3", "command=sh", "cols=%xx"} {
		if _, _, ok := dimensions(query); ok {
			t.Fatalf("accepted %q", query)
		}
	}
	if cols, rows, ok := dimensions(""); !ok || cols != 80 || rows != 24 {
		t.Fatal("invalid default terminal dimensions")
	}
	if cols, rows, ok := dimensions("cols=500&rows=200"); !ok || cols != 500 || rows != 200 {
		t.Fatal("rejected valid terminal dimensions")
	}
}

func TestReadInputRejectsMalformedControls(t *testing.T) {
	for _, control := range []string{
		`{"resize":[80]}`, `{"resize":[1,24]}`, `{"resize":[80,24,1]}`,
		`{"scroll":201}`, `{"scroll":-201}`, `{"scroll":1.5}`,
		`{"scroll":2,"resize":[80,24]}`, `{"command":"sh"}`, `{} {}`,
	} {
		t.Run(control, func(t *testing.T) {
			result := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					result <- err
					return
				}
				defer conn.CloseNow()
				queue := make(chan inputFrame, 1)
				result <- readInput(r.Context(), conn, queue)
			}))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			if err := conn.Write(ctx, websocket.MessageText, []byte(control)); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err == nil || websocket.CloseStatus(err) != -1 {
					t.Fatalf("malformed control was accepted: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("malformed control was not rejected")
			}
		})
	}
}
