package githubreview

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type evidenceReviewer struct{ rules []string }

func (r *evidenceReviewer) Review(_ context.Context, _ Target, _, rules string, _ func(string) error) ([]Finding, error) {
	r.rules = append(r.rules, rules)
	return []Finding{{Path: "main.go", Line: 1, Title: "Candidate"}}, nil
}

type evidenceVM struct {
	keys    []string
	head    string
	command []string
}

func (v *evidenceVM) Poll(_ context.Context, target Target, command []string, key string, previous VMTestAttempt) (VMTestAttempt, error) {
	v.keys = append(v.keys, key)
	v.head = target.HeadSHA
	v.command = slices.Clone(command)
	if previous.TaskID == "" {
		previous.TaskID = "original-" + key
		previous.Status = "running"
		return previous, nil
	}
	previous.Status = "passed"
	previous.Log = "example package tests passed"
	return previous, nil
}

func TestRepositoryEvidenceAndIsolatedTestRetrySurviveRestart(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "run.sqlite")
	db := testReviewStore(t, filename, true)
	target := workerTestTarget("owner/alpha", 7, sourceRepository)
	saved := workerSettings(t, db).Config
	saved.PublishRepositories = []string{}
	policy := RepositoryPolicy{Rules: "Trace cancellation.", TestCommand: []string{"go", "test", "./pkg/api"}}
	saved.RepositoryPolicies = map[string]RepositoryPolicy{"Owner/Alpha": policy}
	settings := configureWorker(t, db, saved)
	github := &workerGitHub{targets: []Target{target}}
	model, vm := &evidenceReviewer{}, &evidenceVM{}
	service := newService(ctx, func() {}, db, github, connectedCLI{}, model)
	service.vm = vm
	now := time.Now()
	service.scan(ctx, settings, now)
	if err := service.processNext(ctx, settings, now); err != nil {
		t.Fatal(err)
	}
	job := workerJob(t, db, target, "analyzed")
	if !slices.Equal(model.rules, []string{policy.Rules}) || job.Evidence.Review.Status != "completed" || len(job.Evidence.Tests) != 1 || job.Evidence.Tests[0].Status != "queued" {
		t.Fatalf("initial review and recipe: %+v", job)
	}
	for _, stage := range []string{"checks", "test"} {
		if err := service.advanceEvidence(ctx, stage, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testReviewStore(t, filename, false)
	service = newService(ctx, func() {}, db, github, connectedCLI{}, model)
	service.vm = vm
	if err := service.advanceEvidence(ctx, "test", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	job = workerJob(t, db, target, "analyzed")
	if len(vm.keys) != 2 || vm.keys[0] != vm.keys[1] || vm.head != target.HeadSHA || !slices.Equal(policy.TestCommand, vm.command) || job.Evidence.Checks.Status != "empty" || job.Evidence.Tests[0].Status != "passed" {
		t.Fatalf("lost attempt or SHA evidence: %+v", job.Evidence)
	}
	settings.Enabled = false
	updatedCommand := []string{"go", "test", "./pkg/..."}
	settings.RepositoryPolicies[target.Repository] = RepositoryPolicy{Rules: policy.Rules, TestCommand: updatedCommand}
	settings = configureWorker(t, db, settings.Config)
	req := RetryRequest{Repository: target.Repository, Number: target.Number, BaseSHA: target.BaseSHA, HeadSHA: target.HeadSHA, Revision: settings.Revision}
	if err := service.refreshChecks(ctx, req); err != nil {
		t.Fatalf("refresh while polling is paused: %v", err)
	}
	changed := req
	changed.BaseSHA = strings.Repeat("c", 40)
	if err := service.refreshChecks(ctx, changed); !errors.Is(err, ErrRetryStale) {
		t.Fatalf("refresh with another base: %v", err)
	}
	changed = req
	changed.Revision++
	if err := service.refreshChecks(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("refresh with another settings revision: %v", err)
	}
	if err := service.advanceEvidence(ctx, "checks", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.retryTest(ctx, req, 1); err != nil {
		t.Fatal(err)
	}
	if err := service.retryTest(ctx, req, 1); !errors.Is(err, ErrRetryStale) {
		t.Fatalf("duplicate retry: %v", err)
	}
	for i := range 2 {
		if err := service.advanceEvidence(ctx, "test", now.Add(time.Duration(i+2)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	job = workerJob(t, db, target, "analyzed")
	if !slices.Equal(job.Evidence.Tests[0].Command, policy.TestCommand) || !slices.Equal(job.Evidence.Tests[1].Command, updatedCommand) || !slices.Equal(vm.command, updatedCommand) {
		t.Fatalf("each attempt must retain its own test command: %+v", job.Evidence.Tests)
	}
	if len(vm.keys) != 4 || vm.keys[1] == vm.keys[2] || vm.keys[2] != vm.keys[3] || len(job.Evidence.Tests) != 2 || job.Evidence.Tests[0].TaskID == job.Evidence.Tests[1].TaskID || len(job.Findings) != 1 || len(model.rules) != 1 {
		t.Fatalf("test retry changed review or original receipt: %+v", job)
	}
	if budget, _ := db.budget(ctx, now); budget.Used != 1 {
		t.Fatal("test retry consumed review budget")
	}
	newer := target
	newer.HeadSHA = strings.Repeat("c", 40)
	github.targets = []Target{newer}
	service.scan(ctx, settings, now.Add(5*time.Minute))
	stale := workerJob(t, db, target, "stale")
	if len(stale.Evidence.Tests) != 2 || stale.Evidence.Review.Status != "completed" || len(stale.Findings) != 1 {
		t.Fatalf("lost superseded evidence: %+v", stale)
	}
	if err := service.retryTest(ctx, req, 2); !errors.Is(err, ErrRetryStale) {
		t.Fatalf("retry on superseded revision: %v", err)
	}
	if next := workerJob(t, db, newer, "queued"); next.Evidence != nil || len(next.Findings) != 0 {
		t.Fatal("a newer SHA inherited old evidence")
	}
}

func TestCheckRunConclusions(t *testing.T) {
	test := func(c string) CheckRun { return CheckRun{Status: "completed", Conclusion: c} }
	for _, c := range []struct {
		want string
		runs []CheckRun
	}{
		{"empty", nil}, {"neutral", []CheckRun{test("skipped"), test("neutral")}}, {"passed", []CheckRun{test("success"), test("skipped")}},
		{"failed", []CheckRun{test("success"), test("failure")}}, {"pending", []CheckRun{test("success"), {Status: "queued"}}},
	} {
		if got := checksStatus(c.runs); got != c.want {
			t.Errorf("checks = %s, want %s", got, c.want)
		}
	}
}

func TestPoliciesRequireWatchedRepositoryAndBoundedArgv(t *testing.T) {
	cfg := Config{Repositories: []string{"Owner/Repo"}, PublishRepositories: []string{}, DailyLimit: 5, IntervalMinutes: 10}
	for _, policies := range []map[string]RepositoryPolicy{
		{"other/repo": {}}, {"owner/repo": {}, "Owner/Repo": {}}, {"owner/repo": {Rules: strings.Repeat("a", 4097)}}, {"owner/repo": {TestCommand: []string{"go", "bad\nargument"}}},
	} {
		cfg.RepositoryPolicies = policies
		if _, err := cfg.normalized(); !errors.Is(err, ErrInvalid) {
			t.Fatal(policies, err)
		}
	}
}

type duringReview func()

func (f duringReview) Review(_ context.Context, _ Target, _, _ string, progress func(string) error) ([]Finding, error) {
	if err := progress("Captured the original diff."); err != nil {
		return nil, err
	}
	f()
	return []Finding{{Title: "Finding on the original commit."}}, nil
}

func TestHeadChangeWhileModelRunningDoesNotReviveOrRunOldTest(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	target := workerTestTarget("owner/alpha", 1, sourceRepository)
	next := target
	next.HeadSHA = strings.Repeat("e", 40)
	cfg := workerSettings(t, db).Config
	cfg.PublishRepositories = []string{}
	cfg.RepositoryPolicies = map[string]RepositoryPolicy{target.Repository: {Rules: "Check lifecycle.", TestCommand: []string{"go", "test", "./pkg"}}}
	settings := configureWorker(t, db, cfg)
	github := &workerGitHub{targets: []Target{target}}
	service := newService(ctx, func() {}, db, github, connectedCLI{}, nil)
	vm := &evidenceVM{}
	service.vm = vm
	now := time.Now()
	service.scan(ctx, settings, now)
	service.runner = duringReview(func() {
		github.targets = []Target{next}
		service.scan(ctx, settings, now)
	})
	if err := service.processNext(ctx, settings, now); err != nil {
		t.Fatal(err)
	}
	if err := service.advanceEvidence(ctx, "test", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	old := workerJob(t, db, target, "stale")
	if old.Evidence.Review.Status != "completed" || len(old.Evidence.Review.Messages) != 3 || len(old.Findings) != 1 || len(vm.keys) != 0 || old.Evidence.Tests[0].Status != "canceled" {
		t.Fatalf("incorrect supersession: %+v", old)
	}
	workerJob(t, db, next, "queued")
}
