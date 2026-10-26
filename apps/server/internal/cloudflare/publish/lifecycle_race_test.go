package publish

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type retirementFixture struct {
	fixture
	publication Publication
	original    *runtimeEntry
	invalid     atomic.Bool
	resolves    atomic.Int32
	closes      atomic.Int32
	closing     chan struct{}
	release     func()
	httpStarted chan struct{}
}

func publicationRetirementFixture(t *testing.T) *retirementFixture {
	t.Helper()
	f := &retirementFixture{fixture: publishingFixture(t), closing: make(chan struct{}), httpStarted: make(chan struct{}, 1)}
	release := make(chan struct{})
	var once sync.Once
	f.release = func() { once.Do(func() { close(release) }) }
	t.Cleanup(f.release)
	resolve := f.options.Resolve
	f.manager.options.Resolve = func(ctx context.Context, route CapturedRoute, binding compute.ProjectBinding, port int) (Target, error) {
		target, err := resolve(ctx, route, binding, port)
		if err != nil {
			return target, err
		}
		attempt := f.resolves.Add(1)
		validate, closeTarget, handler := target.Validate, target.Close, target.Handler
		target.Validate = func(ctx context.Context) error {
			if attempt == 1 && f.invalid.Load() {
				return errors.New("the original binding changed")
			}
			return validate(ctx)
		}
		target.Close = func() {
			if attempt == 1 {
				close(f.closing)
				<-release
			}
			f.closes.Add(1)
			closeTarget()
		}
		target.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/pending-http" {
				f.httpStarted <- struct{}{}
				<-r.Context().Done()
				return
			}
			handler.ServeHTTP(w, r)
		})
		return target, nil
	}
	f.publication = publish(t, f.fixture, "public", "runtime-race")
	waitState(t, f.manager, f.publication.ID, "published")
	f.manager.mu.Lock()
	f.original = f.manager.live[f.publication.Hostname]
	f.manager.mu.Unlock()
	return f
}

func awaitLifecycle(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

// The actual forwarding requests remain open while the real monitor check
// revokes their captured runtime. Close then pauses at its resource boundary so
// Retry/Unpublish/shutdown can run deterministically during retirement.
func (f *retirementFixture) activeStreams(t *testing.T) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	httpDone := make(chan error, 1)
	go func() {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+f.publication.Hostname+"/pending-http", nil)
		response, err := f.client.Do(request)
		if response != nil {
			response.Body.Close()
		}
		httpDone <- err
	}()
	awaitLifecycle(t, f.httpStarted, "the pending HTTP request did not reach the original target")
	response := applicationRequest(t, f.fixture, f.publication.Hostname, http.MethodGet, "/stream", "", "")
	t.Cleanup(func() { response.Body.Close() })
	reader := bufio.NewReader(response.Body)
	if ready, err := reader.ReadString('\n'); err != nil || ready != "ready\n" {
		t.Fatalf("original SSE did not start: %q %v", ready, err)
	}
	sseDone := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, reader); sseDone <- err }()
	connection, _, err := websocket.Dial(ctx, "ws://"+f.publication.Hostname+"/ws", &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: http.Header{"Origin": {"https://" + f.publication.Hostname}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.CloseNow() })
	if err := connection.Write(ctx, websocket.MessageText, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := connection.Read(ctx); err != nil || string(data) != "original" {
		t.Fatalf("original WebSocket did not forward: %q %v", data, err)
	}
	wsDone := make(chan error, 1)
	go func() { _, _, err := connection.Read(ctx); wsDone <- err }()
	return func() {
		t.Helper()
		for _, stream := range []struct {
			name string
			done <-chan error
		}{{"HTTP", httpDone}, {"SSE", sseDone}, {"WebSocket", wsDone}} {
			select {
			case err := <-stream.done:
				if err == nil || ctx.Err() != nil {
					t.Fatalf("original %s survived retirement or needed the client timeout: %v", stream.name, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("original %s remained open during retirement", stream.name)
			}
		}
	}
}

func (f *retirementFixture) beginMonitorFailure(t *testing.T) <-chan struct{} {
	t.Helper()
	f.invalid.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); f.manager.checkBindings() }()
	awaitLifecycle(t, f.closing, "the monitor did not begin closing the original runtime")
	return done
}

func TestMonitorRetryWaitsForOriginalRuntimeAndIgnoresItsStaleFailure(t *testing.T) {
	f := publicationRetirementFixture(t)
	assertStreamsClosed := f.activeStreams(t)
	monitorDone := f.beginMonitorFailure(t)
	assertStreamsClosed()
	view := waitState(t, f.manager, f.publication.ID, "unavailable")
	if view.URL != "" || f.original.ctx.Err() == nil {
		t.Fatalf("failed runtime retained an active URL or lifetime: %+v", view)
	}
	view, err := f.manager.Retry(context.Background(), f.publication.ID)
	if err != nil || view.State != "provisioning" || view.URL != "" {
		t.Fatalf("retry did not retain the original request while closing: %+v %v", view, err)
	}
	f.manager.mu.Lock()
	live, retiring := f.manager.live[view.Hostname], f.manager.retiring[view.ID]
	f.manager.mu.Unlock()
	if live != nil || retiring != f.original || f.resolves.Load() != 1 {
		t.Fatal("Retry installed a replacement before the original runtime finished closing")
	}
	response := applicationRequest(t, f.fixture, view.Hostname, http.MethodGet, "/", "", "")
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("a retiring publication still forwarded requests: %d", response.StatusCode)
	}
	f.release()
	awaitLifecycle(t, monitorDone, "monitor retirement deadlocked")
	view = waitState(t, f.manager, view.ID, "published")
	f.manager.mu.Lock()
	replacement := f.manager.live[view.Hostname]
	retiring = f.manager.retiring[view.ID]
	f.manager.mu.Unlock()
	if replacement == nil || replacement == f.original || retiring != nil || f.resolves.Load() != 2 || f.closes.Load() != 1 || view.Binding != f.publication.Binding || view.Generation != f.publication.Generation || f.captures.Load() != 1 {
		t.Fatalf("retry lost its exact original identity or resources: %+v", view)
	}
	// A health result can be delayed after releasing its validator lease. Its
	// captured runtime must remain harmless after another runtime is active.
	f.manager.failRuntime(f.original, view.ID, "unavailable", "binding", "late original health failure")
	if current := f.manager.List()[0]; current.State != "published" || current.URL == "" || replacement.ctx.Err() != nil {
		t.Fatalf("an old health result retired the replacement: %+v", current)
	}
	response = applicationRequest(t, f.fixture, view.Hostname, http.MethodGet, "/", "", "")
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the replacement route is inactive despite its published snapshot: %d", response.StatusCode)
	}
}

func TestMonitorRetirementJoinsUnpublishAndShutdown(t *testing.T) {
	for _, operation := range []string{"unpublish", "shutdown"} {
		t.Run(operation, func(t *testing.T) {
			f := publicationRetirementFixture(t)
			assertStreamsClosed := f.activeStreams(t)
			monitorDone := f.beginMonitorFailure(t)
			assertStreamsClosed()
			if _, err := f.manager.Retry(context.Background(), f.publication.ID); err != nil {
				t.Fatal(err)
			}
			operationDone := make(chan struct{})
			operationError := make(chan error, 1)
			go func() {
				defer close(operationDone)
				if operation == "shutdown" {
					operationError <- f.manager.Close()
				} else {
					_, err := f.manager.Unpublish(context.Background(), f.publication.ID)
					operationError <- err
				}
			}()
			deadline := time.Now().Add(2 * time.Second)
			for {
				f.manager.mu.Lock()
				fenced := f.manager.closed || f.manager.withdrawn[f.publication.ID]
				f.manager.mu.Unlock()
				if fenced {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the lifecycle operation did not fence the retiring publication")
				}
				time.Sleep(time.Millisecond)
			}
			select {
			case <-operationDone:
				t.Fatal("the lifecycle operation abandoned the original runtime before it finished closing")
			default:
			}
			f.release()
			awaitLifecycle(t, monitorDone, "the monitor deadlocked with the lifecycle operation")
			awaitLifecycle(t, operationDone, "the lifecycle operation deadlocked with the monitor")
			if err := <-operationError; err != nil {
				t.Fatal(err)
			}
			if operation == "unpublish" {
				waitState(t, f.manager, f.publication.ID, "removed")
				removed := f.manager.List()[0]
				f.manager.failRuntime(f.original, removed.ID, "unavailable", "binding", "late original health failure")
				if current := f.manager.List()[0]; current != removed {
					t.Fatalf("an old health result changed the removed receipt: %+v", current)
				}
			}
			f.manager.mu.Lock()
			live := f.manager.live[f.publication.Hostname]
			f.manager.mu.Unlock()
			if live != nil || f.original.ctx.Err() == nil || f.resolves.Load() != 1 || f.closes.Load() != 1 {
				t.Fatal("the lifecycle operation reopened or orphaned the original publication")
			}
			if view := f.manager.List()[0]; view.State == "published" || view.URL != "" {
				t.Fatalf("a closed route still advertised published: %+v", view)
			}
		})
	}
}
