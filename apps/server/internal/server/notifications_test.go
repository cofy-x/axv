package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	codexhttp "github.com/cofy-x/axv/apps/server/internal/codex/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
	"github.com/cofy-x/axv/apps/server/internal/notifications"
	"github.com/cofy-x/axv/apps/server/internal/storage"
	"github.com/cofy-x/axv/apps/server/internal/terminal"
)

type workstationNotificationFixture struct {
	home      string
	agents    *codex.Manager
	compute   *computegateway.Gateway
	terminals *terminal.Manager
	agentAPI  *codexhttp.Handler
}

func newWorkstationNotificationFixture(t *testing.T) *workstationNotificationFixture {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	agents, err := codex.New(store, codex.Options{DataDir: t.TempDir(), WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(agents.Close)
	gateway := computegateway.NewGateway("", computegateway.GatewayOptions{})
	t.Cleanup(gateway.Close)
	terminals := terminal.New()
	t.Cleanup(terminals.Close)
	agentAPI := codexhttp.New(agents)
	t.Cleanup(agentAPI.Close)
	return &workstationNotificationFixture{home: t.TempDir(), agents: agents, compute: gateway, terminals: terminals, agentAPI: agentAPI}
}

func (f *workstationNotificationFixture) open(t *testing.T) (*notifications.Manager, http.Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	manager, err := workstationNotifications(ctx, f.home, f.agents, f.compute)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	application := http.NewServeMux()
	manager.Register(application)
	return manager, newHandler(application, f.terminals, f.agentAPI)
}

func awaitWorkstationNotificationCheckpoint(t *testing.T, home, source string) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		data, err := os.ReadFile(filepath.Join(home, "notifications", "state.json"))
		if err == nil {
			var state struct {
				Checkpoints map[string]json.RawMessage `json:"checkpoints"`
			}
			if json.Unmarshal(data, &state) == nil && state.Checkpoints[source] != nil {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("workstation notification collection did not checkpoint %s", source)
		case <-tick.C:
		}
	}
}

func TestWorkstationNotificationsDistinguishUnconfiguredComputeFromUnavailableHost(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfigured", true: "unavailable"}[configured], func(t *testing.T) {
			fixture := newWorkstationNotificationFixture(t)
			if configured {
				fixture.compute.Bind(filepath.Join(t.TempDir(), "absent.sock"), "configured-host", "configured-fingerprint")
			}
			manager, _ := fixture.open(t)
			awaitWorkstationNotificationCheckpoint(t, fixture.home, "codex-store")
			snapshot := manager.Snapshot()
			if (snapshot.Intake.Error != "") != configured || snapshot.Intake.Gap || len(snapshot.Events) != 0 {
				t.Fatalf("workstation intake confused route configuration and availability: configured=%v state=%+v", configured, snapshot)
			}
		})
	}
}

func TestWorkstationNotificationLinksResolveExactAttemptThroughSharedHandler(t *testing.T) {
	fixture := newWorkstationNotificationFixture(t)
	const hostID = "original-host"
	target := notificationevent.Target{Kind: "task", StoreID: hostID, HostID: hostID, ProjectID: "original-project", TaskID: "original-task", AttemptID: "original-attempt", EventID: "original-event"}
	socketDirectory, err := os.MkdirTemp("", "axv-n-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDirectory) })
	socket := filepath.Join(socketDirectory, "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var statusReads atomic.Int32
	host := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("opening a notification attempted to control work: %s %s", request.Method, request.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.URL.Path == "/v1/runtime" {
			_ = json.NewEncoder(w).Encode(compute.Runtime{Ready: true, HostID: hostID, ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: "original-config"})
			return
		}
		if request.Header.Get("X-AXV-Host-ID") != hostID {
			t.Error("workstation notification read lost its original host identity")
		}
		switch request.URL.Path {
		case "/v1/task-events":
			_ = json.NewEncoder(w).Encode(notificationevent.Batch{SourceID: "compute:" + hostID, StoreID: hostID, Events: []notificationevent.Event{}})
		case "/v1/projects/original-project/tasks/original-task/attempts/original-attempt/status":
			statusReads.Add(1)
			_ = json.NewEncoder(w).Encode(notificationevent.Resolution{Available: true, Current: false, State: "failed", Target: target})
		default:
			t.Errorf("notification selected another resource: %s", request.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})}
	go host.Serve(listener)
	t.Cleanup(func() { host.Close() })
	fixture.compute.Bind(socket, hostID, "original-config")
	storeID, eventID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	event := notifications.Notification{ID: eventID, CreatedAt: time.Now().UTC(), Kind: "task_failed", State: "failed", Status: "accepted", Attempts: 1, Target: target}
	state := map[string]any{
		"version": 1, "storeId": storeID, "revision": 1, "token": "", "enabledAt": time.Time{},
		"config":      notifications.Config{EventTypes: []string{"task_failed"}},
		"checkpoints": map[string]any{}, "events": []notifications.Notification{event}, "intake": notifications.Intake{},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(fixture.home, "notifications")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, handler := fixture.open(t)
	awaitWorkstationNotificationCheckpoint(t, fixture.home, "compute:"+hostID)
	path := "/api/notifications/" + eventID + "?storeId=" + storeID
	request := func(change func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
		if change != nil {
			change(r)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	response := request(nil)
	var detail struct {
		Event      notifications.Notification   `json:"event"`
		Resolution notificationevent.Resolution `json:"resolution"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.Event.Target != target || detail.Resolution.Target != target || !detail.Resolution.Available || detail.Resolution.Current || detail.Resolution.State != "failed" || statusReads.Load() != 1 {
		t.Fatalf("workstation link did not resolve the historical attempt: HTTP %d %s reads=%d", response.Code, response.Body, statusReads.Load())
	}
	if response.Header().Get("X-Frame-Options") != "DENY" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("notification link bypassed shared response protection: %v", response.Header())
	}
	for _, change := range []func(*http.Request){
		func(request *http.Request) { request.Header.Set("Origin", "null") },
		func(request *http.Request) { request.Header.Set("Sec-Fetch-Site", "cross-site") },
		func(request *http.Request) { request.Header.Set("Sec-Fetch-Site", "same-site") },
		func(request *http.Request) { request.Header.Set("Origin", "https://another.example") },
	} {
		response = request(change)
		if response.Code != http.StatusForbidden || statusReads.Load() != 1 {
			t.Fatalf("untrusted frame reached original work: HTTP %d reads=%d", response.Code, statusReads.Load())
		}
	}
	response = request(func(request *http.Request) { request.URL.RawQuery = "storeId=" + strings.Repeat("f", 32) })
	if response.Code != http.StatusConflict || statusReads.Load() != 1 {
		t.Fatalf("old notification store reached work resolution: HTTP %d reads=%d", response.Code, statusReads.Load())
	}
	fixture.compute.Bind(filepath.Join(socketDirectory, "replacement.sock"), "replacement-host", "replacement-config")
	response = request(nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.Event.Target != target || detail.Resolution.Target != target || detail.Resolution.Available || detail.Resolution.State != "unavailable" || statusReads.Load() != 1 {
		t.Fatalf("old link was redirected after host replacement: HTTP %d %s reads=%d", response.Code, response.Body, statusReads.Load())
	}
	if snapshot := manager.Snapshot(); snapshot.Revision != 1 || len(snapshot.Events) != 1 || snapshot.Events[0].ID != eventID || snapshot.Events[0].Status != "accepted" {
		t.Fatalf("opening a link changed delivery or work state: %+v", snapshot)
	}
}

func TestWorkstationNotificationHostRebindCannotApplyAnotherHostsCursor(t *testing.T) {
	fixture := newWorkstationNotificationFixture(t)
	directory, err := os.MkdirTemp("", "axv-nr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var newHostReadFromStart atomic.Bool
	startHost := func(id string, handle http.HandlerFunc) string {
		t.Helper()
		socket := filepath.Join(directory, id+".sock")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/runtime" {
				_ = json.NewEncoder(w).Encode(compute.Runtime{Ready: true, HostID: id, ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: id + "-config"})
				return
			}
			if r.Method != http.MethodGet || r.URL.Path != "/v1/task-events" || r.Header.Get("X-AXV-Host-ID") != id {
				t.Errorf("unexpected notification source request: %s %s host=%s", r.Method, r.URL.Path, r.Header.Get("X-AXV-Host-ID"))
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			handle(w, r)
		})}
		go server.Serve(listener)
		t.Cleanup(func() { server.Close() })
		return socket
	}
	firstSocket := startHost("first", func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("after") == "0" {
			enterOnce.Do(func() { close(entered) })
			<-release
		}
		_ = json.NewEncoder(w).Encode(notificationevent.Batch{SourceID: "compute:first", StoreID: "first", First: 1, Next: 100, Events: []notificationevent.Event{}})
	})
	secondSocket := startHost("second", func(w http.ResponseWriter, request *http.Request) {
		batch := notificationevent.Batch{SourceID: "compute:second", StoreID: "second", First: 1, Next: 1, Events: []notificationevent.Event{}}
		if request.URL.Query().Get("after") == "0" {
			newHostReadFromStart.Store(true)
			batch.Events = []notificationevent.Event{{ID: "second-event", Sequence: 1, ObservedAt: time.Now().UTC(), State: "failed",
				Target: notificationevent.Target{Kind: "task", StoreID: "second", HostID: "second", ProjectID: "project", TaskID: "task", AttemptID: "attempt", EventID: "second-event"}}}
		}
		_ = json.NewEncoder(w).Encode(batch)
	})
	fixture.compute.Bind(firstSocket, "first", "first-config")
	state := map[string]any{
		"version": 1, "storeId": strings.Repeat("a", 32), "revision": 1, "token": "", "enabledAt": time.Time{},
		"config": notifications.Config{EventTypes: []string{}}, "events": []notifications.Notification{}, "intake": notifications.Intake{},
		"checkpoints": map[string]any{"compute:first": map[string]any{"storeId": "first", "sequence": 100}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	stateDirectory := filepath.Join(fixture.home, "notifications")
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDirectory, "state.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.open(t)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first host discovery was not requested")
	}
	fixture.compute.Bind(secondSocket, "second", "second-config")
	releaseOnce.Do(func() { close(release) })
	awaitWorkstationNotificationCheckpoint(t, fixture.home, "codex-store")
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Checkpoints map[string]struct {
			Sequence uint64 `json:"sequence"`
		} `json:"checkpoints"`
	}
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	if second, exists := after.Checkpoints["compute:second"]; exists && second.Sequence > 0 && !newHostReadFromStart.Load() {
		t.Fatal("host replacement checkpointed unseen events using the previous host's cursor")
	}
}
