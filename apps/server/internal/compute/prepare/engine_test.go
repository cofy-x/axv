package prepare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func shortTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "axvp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func testEngine(t *testing.T, activate Activator) (*Engine, string) {
	t.Helper()
	root := os.Getenv("AXV_TEST_TMPDIR")
	if root == "" {
		t.Fatal("Run preparation tests with node scripts/test-go.mjs so profile fixtures stay in .data/test")
	}
	dir, err := os.MkdirTemp(root, "axvp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	home := filepath.Join(dir, "profile")
	// Lima's long per-machine SSH socket suffix needs a short state directory;
	// executable fixtures and profile data still follow the configured test root.
	engine, err := New(Options{CacheHome: filepath.Join(t.TempDir(), "cache"), Home: home, DefaultLimaHome: filepath.Join(shortTestDir(t), "lima"), Activator: activate})
	if err != nil {
		t.Fatal(err)
	}
	engine.platform = "linux"
	t.Cleanup(func() { _ = engine.Close() })
	return engine, home
}

func testHost(t *testing.T, handle func(http.ResponseWriter, *http.Request)) string {
	t.Helper()
	socket := filepath.Join(shortTestDir(t), "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(handle)}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = listener.Close() })
	return socket
}

func hostResponse(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{"hostId": "test-host", "protocolVersion": compute.ProtocolVersion, "configFingerprint": "test-fingerprint", "providers": []compute.Capability{{Provider: "firecracker", Available: true}}})
}
func testBinding() Binding {
	return Binding{HostID: "test-host", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: "test-fingerprint"}
}

func planFor(t *testing.T, e *Engine, socket string) Plan {
	t.Helper()
	p, err := e.Plan(context.Background(), Request{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	if !p.CanStart {
		t.Fatalf("blocked plan: %+v", p.Checks)
	}
	return p
}

func startPlan(t *testing.T, e *Engine, p Plan, requestID string) Job {
	t.Helper()
	j, err := e.Start(context.Background(), StartRequest{RequestID: requestID, PlanID: p.ID, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func awaitJob(t *testing.T, e *Engine, id string, status string) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := e.Job(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == status {
			return job
		}
		if job.Status == "failed" && status != "failed" {
			t.Fatalf("task failed: %+v", job.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := e.Job(context.Background(), id)
	t.Fatalf("wanted %s, got %+v", status, job)
	return Job{}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("wanted %s, got %v", code, err)
	}
}

func TestRequestIdentityAndOneActiveJob(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	e, _ := testEngine(t, func(ctx context.Context, _ Activation) (Binding, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return Binding{}, ctx.Err()
		case <-release:
			return testBinding(), nil
		}
	})
	socket := testHost(t, func(w http.ResponseWriter, _ *http.Request) { hostResponse(w) })
	p := planFor(t, e, socket)
	other := planFor(t, e, socket)
	request := StartRequest{RequestID: "same-start-request", PlanID: p.ID, Confirmed: true}
	job, err := e.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("activation did not start")
	}
	again, err := e.Start(context.Background(), request)
	if err != nil || again.ID != job.ID {
		t.Fatalf("duplicate start: %+v %v", again, err)
	}
	_, err = e.Start(context.Background(), StartRequest{RequestID: request.RequestID, PlanID: other.ID, Confirmed: true})
	assertCode(t, err, "request_conflict")
	_, err = e.Start(context.Background(), StartRequest{RequestID: "different-start-request", PlanID: other.ID, Confirmed: true})
	assertCode(t, err, "configuration_changed")
	current, _ := e.Job(context.Background(), job.ID)
	_, err = e.Cancel(context.Background(), job.ID, ActionRequest{RequestID: "cancel-after-commit", ExpectedRevision: current.Revision})
	assertCode(t, err, "cancel_unavailable")
	close(release)
	final := awaitJob(t, e, job.ID, "succeeded")
	if final.Result != "ready" || !final.Committed || final.Binding == nil {
		t.Fatalf("incomplete result: %+v", final)
	}
}

func TestActivationFailureRetryPreservesCommitAndRequest(t *testing.T) {
	var calls atomic.Int32
	e, home := testEngine(t, func(context.Context, Activation) (Binding, error) {
		if calls.Add(1) == 1 {
			return Binding{}, errors.New("test startup failure")
		}
		return testBinding(), nil
	})
	if err := jsonfile.Write(filepath.Join(home, "config.json"), map[string]any{"workspace_dir": filepath.Join(home, "projects"), "future_setting": map[string]bool{"keep": true}}, 0600); err != nil {
		t.Fatal(err)
	}
	socket := testHost(t, func(w http.ResponseWriter, _ *http.Request) { hostResponse(w) })
	job := startPlan(t, e, planFor(t, e, socket), "activation-retry-start")
	failed := awaitJob(t, e, job.ID, "failed")
	if !failed.Committed || !failed.CanRetry || failed.CanCancel {
		t.Fatalf("invalid failed state: %+v", failed)
	}
	before, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	request := ActionRequest{RequestID: "activation-retry-action", ExpectedRevision: failed.Revision}
	if _, err = e.Retry(context.Background(), job.ID, request); err != nil {
		t.Fatal(err)
	}
	final := awaitJob(t, e, job.ID, "succeeded")
	if final.Attempt != 2 {
		t.Fatalf("attempts=%d", final.Attempt)
	}
	again, err := e.Retry(context.Background(), job.ID, request)
	if err != nil || again.ID != job.ID || again.Attempt != 2 || calls.Load() != 2 {
		t.Fatalf("retry duplicated: %+v %v", again, err)
	}
	after, _ := os.ReadFile(filepath.Join(home, "config.json"))
	if string(before) != string(after) || !strings.Contains(string(after), "future_setting") {
		t.Fatal("retry changed committed profile")
	}
}

func TestPreparationOutlivesStartingRequestContext(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	e, _ := testEngine(t, func(ctx context.Context, _ Activation) (Binding, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return Binding{}, ctx.Err()
		case <-release:
			return testBinding(), nil
		}
	})
	t.Cleanup(func() { close(release) })
	socket := testHost(t, func(w http.ResponseWriter, _ *http.Request) { hostResponse(w) })
	plan := planFor(t, e, socket)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job, err := e.Start(ctx, StartRequest{RequestID: "detached-start-request", PlanID: plan.ID, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request cancellation stopped the durable task")
	}
	select {
	case release <- struct{}{}:
	case <-time.After(5 * time.Second):
		t.Fatal("request cancellation interrupted activation")
	}
	final := awaitJob(t, e, job.ID, "succeeded")
	if final.Result != "ready" || final.Binding == nil {
		t.Fatalf("detached task did not activate: %+v", final)
	}
}

func TestInterruptedTaskSurvivesEngineRestart(t *testing.T) {
	entered := make(chan struct{})
	e, home := testEngine(t, func(ctx context.Context, _ Activation) (Binding, error) {
		close(entered)
		<-ctx.Done()
		return Binding{}, ctx.Err()
	})
	socket := testHost(t, func(w http.ResponseWriter, _ *http.Request) { hostResponse(w) })
	job := startPlan(t, e, planFor(t, e, socket), "restart-start-request")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("activation did not start")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := New(Options{CacheHome: e.cache.Home, Home: home, Activator: func(context.Context, Activation) (Binding, error) { return testBinding(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	next.platform = "linux"
	check, err := next.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if check.LatestJob == nil || check.LatestJob.ID != job.ID || check.LatestJob.Status != "interrupted" || !check.LatestJob.Committed {
		t.Fatalf("lost task: %+v", check.LatestJob)
	}
	if _, err = next.Retry(context.Background(), job.ID, ActionRequest{RequestID: "restart-retry-request", ExpectedRevision: check.LatestJob.Revision}); err != nil {
		t.Fatal(err)
	}
	awaitJob(t, next, job.ID, "succeeded")
}

func TestCancelBeforeCommitDoesNotSaveProfile(t *testing.T) {
	var probes atomic.Int32
	entered := make(chan struct{})
	socket := testHost(t, func(w http.ResponseWriter, r *http.Request) {
		if probes.Add(1) > 1 {
			close(entered)
			<-r.Context().Done()
			return
		}
		hostResponse(w)
	})
	e, home := testEngine(t, nil)
	job := startPlan(t, e, planFor(t, e, socket), "cancel-start-request")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not probe")
	}
	current, _ := e.Job(context.Background(), job.ID)
	cancelled, err := e.Cancel(context.Background(), job.ID, ActionRequest{RequestID: "cancel-task-request", ExpectedRevision: current.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatal(cancelled.Status)
	}
	if _, err = os.Stat(filepath.Join(home, "config.json")); !os.IsNotExist(err) {
		t.Fatal("cancelled task committed profile")
	}
	_, err = e.Retry(context.Background(), job.ID, ActionRequest{RequestID: "retry-cancelled-task", ExpectedRevision: cancelled.Revision})
	assertCode(t, err, "retry_unavailable")
}

func TestReviewAndReadOnlyChecksDoNotConfigureProfile(t *testing.T) {
	e, home := testEngine(t, nil)
	var probes atomic.Int32
	socket := testHost(t, func(w http.ResponseWriter, _ *http.Request) { probes.Add(1); hostResponse(w) })
	for range 3 {
		if _, err := e.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if probes.Load() != 0 {
		t.Fatal("polling probed a service")
	}
	plan := planFor(t, e, socket)
	_, err := e.Start(context.Background(), StartRequest{RequestID: "missing-confirmation", PlanID: plan.ID})
	assertCode(t, err, "confirmation_required")
	if _, err = os.Stat(filepath.Join(home, "config.json")); !os.IsNotExist(err) {
		t.Fatal("review configured the profile")
	}
}

func TestCommitCASPreservesConcurrentUnknownSettings(t *testing.T) {
	e, home := testEngine(t, nil)
	_, fingerprint, err := readCompute(home)
	if err != nil {
		t.Fatal(err)
	}
	desired, _ := json.Marshal(profile.Compute{Mode: "external", Socket: "/tmp/axv-test.sock"})
	spec := specification{ComputeFingerprint: fingerprint}
	intent := commitIntent{ComputeJSON: desired}
	if err = jsonfile.Write(filepath.Join(home, "config.json"), map[string]any{"workspace_dir": "/tmp/new-workspace", "future_setting": 42}, 0600); err != nil {
		t.Fatal(err)
	}
	if err = e.commit(context.Background(), spec, intent); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "config.json"))
	if !strings.Contains(string(b), "future_setting") || !strings.Contains(string(b), "new-workspace") {
		t.Fatal("lost unrelated changes")
	}
	other, _ := json.Marshal(profile.Compute{Mode: "external", Socket: "/tmp/different.sock"})
	err = e.commit(context.Background(), spec, commitIntent{ComputeJSON: other})
	assertCode(t, err, "configuration_changed")
	after, _ := os.ReadFile(filepath.Join(home, "config.json"))
	if string(after) != string(b) {
		t.Fatal("CAS overwrote changed profile")
	}
}

func TestCommitIntentRecoversPublishedHostConfiguration(t *testing.T) {
	e, home := testEngine(t, nil)
	_, fingerprint, _ := readCompute(home)
	host := compute.Config{DataDir: filepath.Join(home, "compute"), Socket: filepath.Join(home, "host", "control.sock"), Capacity: compute.Capacity{Resources: compute.Resources{CPUs: 2, MemoryMiB: 4096, DiskGiB: 128}, Machines: 4, Workers: 2}, Lima: &compute.LimaConfig{Binary: "/tmp/test-limactl", Version: compute.SupportedLimaVersion, Home: filepath.Join(home, "lima")}}
	hostJSON, _ := json.Marshal(host)
	hostPath := filepath.Join(home, "host.json")
	desired, _ := json.Marshal(profile.Compute{Mode: "managed", HostConfig: hostPath})
	intent := commitIntent{HostJSON: hostJSON, HostPath: hostPath, ComputeJSON: desired}
	if err := atomicCreate(hostPath, hostJSON); err != nil {
		t.Fatal(err)
	}
	if err := e.commit(context.Background(), specification{ComputeFingerprint: fingerprint}, intent); err != nil {
		t.Fatal(err)
	}
	profile, err := profile.Read(home)
	if err != nil || profile.Compute.HostConfig != hostPath {
		t.Fatalf("not recovered: %+v %v", profile, err)
	}
	if err = e.commit(context.Background(), specification{ComputeFingerprint: fingerprint}, intent); err != nil {
		t.Fatal("idempotent commit failed", err)
	}
}

func TestStoreRejectsUnknownFormatWithoutReset(t *testing.T) {
	e, home := testEngine(t, nil)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "preparation", "preparation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if next, err := New(Options{CacheHome: filepath.Join(t.TempDir(), "cache"), Home: home}); err == nil {
		_ = next.Close()
		t.Fatal("accepted unknown format")
	}
	db, err = sql.Open("sqlite", filepath.Join(home, "preparation", "preparation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 99 {
		t.Fatalf("database reset: %d %v", version, err)
	}
}

func TestPreparationHasNoApplicationGuestBuildDependency(t *testing.T) {
	e, _ := testEngine(t, nil)
	check, err := e.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, condition := range check.Checks {
		if condition.ID == "guest" {
			t.Fatal("preparation depends on the app guest binary")
		}
	}
}

func TestStorageOwnershipAndProfileScopedDefault(t *testing.T) {
	e, home := testEngine(t, nil)
	r := e.defaults()
	if _, err := e.validateStorage(r); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "compute"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "compute", "foreign"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.validateStorage(r); err == nil {
		t.Fatal("adopted foreign compute storage")
	}
	e.options.DefaultLimaHome = ""
	e.options.Home = filepath.Join(home, strings.Repeat("long", 20))
	request := e.defaults()
	if request.LimaHome != filepath.Join(e.options.Home, "lima") {
		t.Fatal("moved Lima storage outside the profile")
	}
	if _, err := e.validateStorage(request); err == nil || !strings.Contains(err.Error(), "AXV_HOME (AXV_DEV_HOME") {
		t.Fatalf("missing profile socket path diagnostic: %v", err)
	}
}
