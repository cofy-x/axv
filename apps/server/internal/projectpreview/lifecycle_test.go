package projectpreview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func controlRequest(m *Manager, method, path string, body any) *http.Request {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://127.0.0.1:3000/api/project-previews/project-one"+path, strings.NewReader(string(data)))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "http://127.0.0.1:3000")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-AXV-Host-ID", "host-one")
	return r
}

func registerForTest(t *testing.T, m *Manager, handler http.Handler, id string) View {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, controlRequest(m, "POST", "", Registration{InstanceID: m.instanceID, RequestID: id, Binding: testBinding(), Port: 5173}))
	var view View
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &view) != nil {
		t.Fatalf("registration: %d %s", w.Code, w.Body)
	}
	return view
}

func TestRegistrationRecoversOriginalRequestAfterBrowserDisconnect(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var resolutions atomic.Int32
	m := New(context.Background(), func(ctx context.Context, _ compute.ProjectBinding, _ int) (Target, error) {
		resolutions.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return Target{}, ctx.Err()
		}
		return Target{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), Validate: func(context.Context) error { return nil }, Close: func() {}}, nil
	})
	defer m.Close()
	mux := http.NewServeMux()
	m.Register(mux)
	input := Registration{InstanceID: m.instanceID, RequestID: "original-registration", Binding: testBinding(), Port: 5173}
	ctx, cancel := context.WithCancel(context.Background())
	first := controlRequest(m, "POST", "", input).WithContext(ctx)
	done := make(chan struct{})
	go func() { mux.ServeHTTP(httptest.NewRecorder(), first); close(done) }()
	<-entered
	cancel()
	<-done
	close(release)
	a := registerForTest(t, m, mux, input.RequestID)
	b := registerForTest(t, m, mux, input.RequestID)
	if a.ID != b.ID || resolutions.Load() != 1 {
		t.Fatalf("request duplicated preview: %+v %+v %d", a, b, resolutions.Load())
	}
	input.Port = 8080
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, controlRequest(m, "POST", "", input))
	if w.Code != 409 {
		t.Fatalf("changed request reused: %d", w.Code)
	}
	other := New(context.Background(), nil)
	defer other.Close()
	next := http.NewServeMux()
	other.Register(next)
	w = httptest.NewRecorder()
	next.ServeHTTP(w, controlRequest(other, "POST", "", input))
	if w.Code != 409 || len(other.requests) != 0 {
		t.Fatalf("old manager request created a new preview: %d", w.Code)
	}
}

func TestStoppingMoreThan64PreviewsReleasesCapacityAndResources(t *testing.T) {
	var closed atomic.Int32
	m := New(context.Background(), func(context.Context, compute.ProjectBinding, int) (Target, error) {
		return Target{Handler: http.NotFoundHandler(), Validate: func(context.Context) error { return nil }, Close: func() { closed.Add(1) }}, nil
	})
	defer m.Close()
	mux := http.NewServeMux()
	m.Register(mux)
	origins := map[string]bool{}
	for range 80 {
		v := registerForTest(t, m, mux, compute.NewID("preview"))
		u, _ := url.Parse(v.URL)
		if origins[u.Hostname()] {
			t.Fatal("a retired hostname was recycled")
		}
		origins[u.Hostname()] = true
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, controlRequest(m, "POST", "/"+v.ID+"/stop", struct{}{}))
		if w.Code != 200 {
			t.Fatalf("stop: %d %s", w.Code, w.Body)
		}
		if response, err := http.Get("http://127.0.0.1:" + u.Port() + "/"); err == nil {
			response.Body.Close()
			t.Fatal("retired listener retained its port")
		}
	}
	if m.active != 0 || closed.Load() != 80 {
		t.Fatalf("retired resources retained: %d %d", m.active, closed.Load())
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, controlRequest(m, "GET", "", nil))
	var result struct {
		Previews []View `json:"previews"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Previews) != retainedPreviewHistory {
		t.Fatalf("history was not bounded: %d", len(result.Previews))
	}
	// Old request receipts remain recoverable after leaving the recent list.
	first := m.entries[0]
	var original string
	for key, r := range m.requests {
		if r.entry == first {
			original = key
			break
		}
	}
	replayed := registerForTest(t, m, mux, original)
	if replayed.ID != first.view.ID || replayed.State != "stopped" || closed.Load() != 80 {
		t.Fatal("old receipt resurrected a stopped preview")
	}
}

func TestStopCancelsValidatedRequestBeforeUpstreamAndClosesTargetOnce(t *testing.T) {
	m, control, client, _ := fixture(t)
	v := createPreview(t, control, client)
	bootstrap, err := client.Get(v.URL)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap.Body.Close()
	entered := make(chan struct{})
	var forwarded, closed atomic.Int32
	m.mu.Lock()
	e := m.entries[0]
	e.target.Validate = func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }
	e.target.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) })
	e.target.Close = func() { closed.Add(1) }
	m.mu.Unlock()
	u, _ := url.Parse(v.URL)
	done := make(chan struct{})
	go func() {
		response, err := client.Get("http://" + u.Host + "/")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach validation")
	}
	mux := http.NewServeMux()
	m.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, controlRequest(m, "POST", "/"+v.ID+"/stop", struct{}{}))
	if w.Code != 200 || forwarded.Load() != 0 || closed.Load() != 1 {
		t.Fatalf("stop did not fence upstream: %d %d %d", w.Code, forwarded.Load(), closed.Load())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retired request remained connected")
	}
	m.Close()
	if closed.Load() != 1 {
		t.Fatal("target closed more than once")
	}
}

func TestRegistrationReceiptCapacityPreservesRecovery(t *testing.T) {
	m, control, client, _ := fixture(t)
	v := createPreview(t, control, client)
	mux := http.NewServeMux()
	m.Register(mux)
	m.mu.Lock()
	var original string
	for key := range m.requests {
		original = key
	}
	for len(m.requests) < maxRegistrationReceipts {
		m.requests[fmt.Sprintf("retained-request-%d", len(m.requests))] = &registration{}
	}
	m.mu.Unlock()
	input := Registration{InstanceID: m.instanceID, RequestID: "capacity-new-request", Binding: testBinding(), Port: 5173}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, controlRequest(m, "POST", "", input))
	if w.Code != 429 || !strings.Contains(w.Body.String(), "preview_receipt_capacity") {
		t.Fatalf("receipt capacity did not fail visibly: %d %s", w.Code, w.Body)
	}
	if recovered := registerForTest(t, m, mux, original); recovered.ID != v.ID {
		t.Fatal("receipt capacity discarded an existing identity")
	}
}

func TestSlowValidationDoesNotDelayOtherPreviews(t *testing.T) {
	m, control, client, _ := fixture(t)
	createPreview(t, control, client)
	createPreview(t, control, client)
	release := make(chan struct{})
	entered := make(chan struct{})
	other := make(chan struct{})
	m.mu.Lock()
	m.entries[0].target.Validate = func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.entries[1].target.Validate = func(context.Context) error { close(other); return fmt.Errorf("changed project root") }
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.check(); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("slow check did not start")
	}
	select {
	case <-other:
	case <-time.After(time.Second):
		t.Fatal("one slow preview serialized other validation")
	}
	close(release)
	<-done
	m.mu.Lock()
	state := m.entries[1].view.State
	m.mu.Unlock()
	if state != "unavailable" {
		t.Fatal("other preview was not invalidated independently")
	}
}
