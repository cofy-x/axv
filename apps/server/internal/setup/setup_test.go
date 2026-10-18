package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
)

func TestNormalizeRequiresValidOptions(t *testing.T) {
	for _, o := range []Options{
		{Request: prepare.Request{MemoryMiB: 255}},
		{Request: prepare.Request{StorageRoot: " "}},
		{Request: prepare.Request{Socket: "/tmp/a", HostConfig: "/tmp/b"}},
		{Retry: "prepare-a", Explicit: true},
	} {
		if err := Normalize(&o); err == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
	o := Options{Yes: true, Explicit: true, Request: prepare.Request{CPUs: 2, StorageRoot: "/tmp/owned"}}
	if err := Normalize(&o); err != nil {
		t.Fatal(err)
	}
}

func TestOfflinePreparationCannotRaceDesktop(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Unix profile locks")
	}
	home := t.TempDir()
	lock, err := lockOffline(home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if second, err := lockOffline(home); err == nil {
		second.Close()
		t.Fatal("acquired an occupied desktop lock")
	}
}

func TestClientVerifiesInstanceAndPinsMutationHeaders(t *testing.T) {
	mutations := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/runtime" {
			_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": "desktop-exact"})
			return
		}
		if r.Method == "POST" {
			mutations++
			if r.Header.Get("Origin") != server.URL || r.Header.Get("X-AXV-Instance-ID") != "desktop-exact" {
				t.Error("mutation lost verified desktop identity")
			}
		}
		_ = json.NewEncoder(w).Encode(prepare.Plan{ID: "plan-exact"})
	}))
	defer server.Close()
	client, err := connect(context.Background(), launch.State{URL: server.URL, InstanceID: "desktop-exact"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	plan, err := client.Plan(context.Background(), prepare.Request{})
	if err != nil || plan.ID != "plan-exact" || mutations != 1 {
		t.Fatalf("plan=%+v mutations=%d err=%v", plan, mutations, err)
	}
	if other, err := connect(context.Background(), launch.State{URL: server.URL, InstanceID: "desktop-wrong"}); err == nil {
		other.Close()
		t.Fatal("accepted foreign instance")
	}
}

func TestClientRejectsRemoteStateAndRedirects(t *testing.T) {
	for _, value := range []string{"http://example.com:1234", "http://localhost:1234", "https://127.0.0.1:1234", "http://user@127.0.0.1:1234", "http://127.0.0.1:1234/other", "http://127.0.0.1:1234?x=1"} {
		if c, err := connect(context.Background(), launch.State{URL: value, InstanceID: "desktop"}); err == nil {
			c.Close()
			t.Fatalf("accepted %s", value)
		}
	}
	followed := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed = true
		_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": "desktop"})
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if c, err := connect(context.Background(), launch.State{URL: redirect.URL, InstanceID: "desktop"}); err == nil {
		c.Close()
		t.Fatal("accepted redirect")
	}
	if followed {
		t.Fatal("followed desktop redirect")
	}
}

type fakePreparation struct {
	check            prepare.Check
	plan             prepare.Plan
	job              prepare.Job
	started, retried int
	request          prepare.StartRequest
	action           prepare.ActionRequest
	startErr         error
}

func (f *fakePreparation) Check(context.Context) (prepare.Check, error) { return f.check, nil }
func (f *fakePreparation) Plan(context.Context, prepare.Request) (prepare.Plan, error) {
	return f.plan, nil
}
func (f *fakePreparation) Start(_ context.Context, request prepare.StartRequest) (prepare.Job, error) {
	f.started++
	f.request = request
	return f.job, f.startErr
}
func (f *fakePreparation) Job(context.Context, string) (prepare.Job, error) { return f.job, nil }
func (f *fakePreparation) Retry(_ context.Context, _ string, action prepare.ActionRequest) (prepare.Job, error) {
	f.retried++
	f.action = action
	job := f.job
	job.Status, job.Result = "succeeded", "ready"
	return job, nil
}

func TestActiveTaskIsFollowedWithoutNewConsentOrMutation(t *testing.T) {
	job := prepare.Job{ID: "prepare-existing", Status: "succeeded", Result: "ready"}
	service := &fakePreparation{check: prepare.Check{ActiveJob: &job}}
	if err := execute(context.Background(), service, Options{}, &bytes.Buffer{}, true); err != nil {
		t.Fatal(err)
	}
	if service.started != 0 || service.retried != 0 {
		t.Fatal("following active task mutated it")
	}
	if err := execute(context.Background(), service, Options{Explicit: true}, &bytes.Buffer{}, true); err == nil {
		t.Fatal("silently ignored conflicting settings")
	}
}

func TestCLIStartsReviewedPlanAndDistinguishesOfflineResult(t *testing.T) {
	service := &fakePreparation{plan: prepare.Plan{ID: "plan-reviewed", CanStart: true}, job: prepare.Job{ID: "prepare-one", Status: "succeeded", Result: "configured"}}
	var output bytes.Buffer
	if err := execute(context.Background(), service, Options{Yes: true}, &output, false); err != nil {
		t.Fatal(err)
	}
	if service.started != 1 || service.request.PlanID != "plan-reviewed" || !service.request.Confirmed || service.request.RequestID == "" {
		t.Fatalf("bad start: %+v", service.request)
	}
	if !strings.Contains(output.String(), "configuration is prepared") || strings.Contains(output.String(), "hosting is ready") {
		t.Fatal("claimed an offline service was ready")
	}
}

func TestCLIUnknownStartOutcomeDoesNotSubmitAnotherTask(t *testing.T) {
	service := &fakePreparation{plan: prepare.Plan{ID: "plan-reviewed", CanStart: true}, startErr: errors.New("connection lost")}
	err := execute(context.Background(), service, Options{Yes: true}, &bytes.Buffer{}, true)
	if err == nil || service.started != 1 || !strings.Contains(err.Error(), service.request.RequestID) {
		t.Fatalf("unknown outcome lost request identity: %v", err)
	}
}

func TestCLIRetryUsesOriginalJobAndRevision(t *testing.T) {
	service := &fakePreparation{job: prepare.Job{ID: "prepare-original", Revision: 7, CanRetry: true, Status: "interrupted", Plan: prepare.Plan{ID: "plan-original"}}}
	if err := execute(context.Background(), service, Options{Yes: true, Retry: "prepare-original"}, &bytes.Buffer{}, true); err != nil {
		t.Fatal(err)
	}
	if service.started != 0 || service.retried != 1 || service.action.ExpectedRevision != 7 || service.action.RequestID == "" {
		t.Fatal("retry replaced the task or lost its revision")
	}
}

func TestInterruptedTaskRequiresExplicitRetry(t *testing.T) {
	job := prepare.Job{ID: "prepare-interrupted", Status: "interrupted", CanRetry: true}
	service := &fakePreparation{check: prepare.Check{LatestJob: &job}}
	err := execute(context.Background(), service, Options{Yes: true}, &bytes.Buffer{}, true)
	if err == nil || !strings.Contains(err.Error(), "--retry prepare-interrupted") || service.started != 0 || service.retried != 0 {
		t.Fatalf("interrupted task was replaced: %v", err)
	}
}

func TestOfflineLockUsesStableIdentityThroughSymlinkedParent(t *testing.T) {
	parent := t.TempDir()
	real := filepath.Join(parent, "real")
	link := filepath.Join(parent, "link")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	home := filepath.Join(link, "new-profile")
	lock, err := lockOffline(home)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	if err = (app.Settings{Inputs: launch.Inputs{Home: home}}).Prepare(); err != nil {
		t.Fatalf("offline setup left an incompatible runtime owner: %v", err)
	}
}

func TestRunOptionsCancellationDoesNotInitializeAProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "uninitialized-profile")
	t.Setenv("AXV_HOME", home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RunOptions(ctx, Options{Yes: true}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled setup = %v", err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("cancelled setup initialized the profile: %v", err)
	}
}

func TestSetupRequiresExplicitApprovalAndUsesTheCallerConfirmation(t *testing.T) {
	service := &fakePreparation{
		plan: prepare.Plan{ID: "reviewed", CanStart: true},
		job:  prepare.Job{ID: "prepared", Status: "succeeded", Result: "configured"},
	}
	var output bytes.Buffer
	if err := execute(context.Background(), service, Options{}, &output, false); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("noninteractive setup = %v", err)
	}
	if service.started != 0 {
		t.Fatal("started preparation without approval")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	confirmed := 0
	o := Options{Confirm: func(actual context.Context) error {
		if actual != ctx || !strings.Contains(output.String(), "VM preparation plan:") {
			t.Fatal("confirmation did not receive the caller context and displayed plan")
		}
		confirmed++
		return nil
	}}
	if err := execute(ctx, service, o, &output, false); err != nil {
		t.Fatal(err)
	}
	if confirmed != 1 || service.started != 1 {
		t.Fatalf("confirmations = %d, starts = %d", confirmed, service.started)
	}
	o.Yes = true
	o.Confirm = func(context.Context) error { t.Fatal("--yes requested a prompt"); return nil }
	if err := execute(ctx, service, o, &output, false); err != nil {
		t.Fatal(err)
	}
}

func TestCancellationDuringConfirmationCannotStartOrRetryPreparation(t *testing.T) {
	for _, retry := range []string{"", "original"} {
		t.Run("retry="+retry, func(t *testing.T) {
			service := &fakePreparation{
				plan: prepare.Plan{ID: "reviewed", CanStart: true},
				job:  prepare.Job{ID: "original", Revision: 7, CanRetry: true, Status: "interrupted"},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{})
			o := Options{Retry: retry, Confirm: func(actual context.Context) error {
				close(entered)
				<-actual.Done()
				// Even a confirmer returning approval after cancellation must not
				// authorize a mutation.
				return nil
			}}
			done := make(chan error, 1)
			go func() { done <- execute(ctx, service, o, &bytes.Buffer{}, false) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("setup did not request confirmation")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled setup = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("setup did not stop with its caller")
			}
			if service.started != 0 || service.retried != 0 {
				t.Fatal("confirmation cancellation submitted preparation")
			}
		})
	}
}

type cancelTaskOutput struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (o *cancelTaskOutput) Write(p []byte) (int, error) {
	n, err := o.Buffer.Write(p)
	if bytes.Contains(p, []byte("prepare-active: running")) {
		o.cancel()
	}
	return n, err
}

func TestFollowingCancellationRetainsTheExactTaskAndOwnershipMessage(t *testing.T) {
	for _, live := range []bool{false, true} {
		name, message := "offline", "retry task prepare-active"
		if live {
			name, message = "live", "Detached. Preparation continues in AXV"
		}
		t.Run(name, func(t *testing.T) {
			job := prepare.Job{ID: "prepare-active", Status: "running", Phase: "downloading"}
			service := &fakePreparation{check: prepare.Check{ActiveJob: &job}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			output := &cancelTaskOutput{cancel: cancel}
			err := execute(ctx, service, Options{}, output, live)
			if !errors.Is(err, context.Canceled) || !strings.Contains(output.String(), message) {
				t.Fatalf("following cancellation = %v, output = %q", err, output.String())
			}
			if service.started != 0 || service.retried != 0 {
				t.Fatal("detaching from a task changed its execution")
			}
		})
	}
}
