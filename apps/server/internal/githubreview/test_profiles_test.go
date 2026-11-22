package githubreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type profileGitHub struct {
	*workerGitHub
	paths, inspected         []string
	files                    map[string]bool
	changesError, entryError error
	compares, trees          []Target
}

func (g *profileGitHub) ChangedTestPaths(_ context.Context, _ string, t Target) ([]string, error) {
	g.compares = append(g.compares, t)
	return g.paths, g.changesError
}

func (g *profileGitHub) testEntryCheck(_ context.Context, _ string, t Target) (testEntryCheck, error) {
	g.trees = append(g.trees, t)
	return func(path string, _ bool) (bool, error) {
		g.inspected = append(g.inspected, path)
		return g.files[path], g.entryError
	}, nil
}

func exampleProfile(id, entry string, scopes ...string) TestProfile {
	return TestProfile{ID: id, Name: id, EntryPath: entry, Command: []string{"python3", entry}, AppliesTo: scopes}
}

func TestNamedTestAutomaticSelectionAndIsolatedOverride(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "review.sqlite")
	db := testReviewStore(t, filename, true)
	target := workerTestTarget("owner/alpha", 5, sourceRepository)
	cfg := workerSettings(t, db).Config
	cfg.PublishRepositories = []string{}
	sdk := exampleProfile("SDK", "sdk/tests.py", "sdk/")
	old := exampleProfile("Old", "old_config.py", "deploy")
	deploy := exampleProfile("Deploy", "deploy/tests.py", "deploy")
	cfg.RepositoryPolicies = map[string]RepositoryPolicy{target.Repository: {TestCommand: []string{"unrelated"}, TestProfiles: []TestProfile{sdk, old, deploy}}}
	settings := configureWorker(t, db, cfg)
	gh := &profileGitHub{workerGitHub: &workerGitHub{targets: []Target{target}}, paths: []string{"sdk2/new.py", "deploy/chart.yaml"}, files: map[string]bool{sdk.EntryPath: true, deploy.EntryPath: true}}
	model, vm := &evidenceReviewer{}, &evidenceVM{}
	service := newService(ctx, func() {}, db, gh, connectedCLI{}, model)
	service.vm = vm
	now := time.Now()
	service.scan(ctx, settings, now)
	if err := service.processNext(ctx, settings, now); err != nil {
		t.Fatal(err)
	}
	step := func() {
		t.Helper()
		now = now.Add(time.Minute)
		if err := service.advanceEvidence(ctx, "test", now); err != nil {
			t.Fatal(err)
		}
	}
	step()
	first := workerJob(t, db, target, "analyzed").Evidence.Tests[0]
	if first.TestID != deploy.ID || first.TestName != deploy.Name || first.EntryPath != deploy.EntryPath || first.Selection != "automatic" || first.Status != "running" || !slices.Equal(vm.command, deploy.Command) || !strings.Contains(first.SelectionReason, old.EntryPath) || !strings.Contains(first.SelectionReason, "Changes matched deploy") || !slices.Equal(gh.inspected, []string{old.EntryPath, deploy.EntryPath}) {
		t.Fatalf("automatic attempt: %+v (paths %v)", first, gh.inspected)
	}
	if len(gh.compares) != 1 || len(gh.trees) != 1 || gh.compares[0].BaseSHA != target.BaseSHA || gh.compares[0].HeadSHA != target.HeadSHA || gh.trees[0].HeadSHA != target.HeadSHA {
		t.Fatalf("unbound preflight: %+v %+v", gh.compares, gh.trees)
	}
	if err := db.db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testReviewStore(t, filename, false)
	service = newService(ctx, func() {}, db, gh, connectedCLI{}, model)
	service.vm = vm
	step()
	req := RetryRequest{Repository: target.Repository, Number: target.Number, BaseSHA: target.BaseSHA, HeadSHA: target.HeadSHA, Revision: settings.Revision}
	if err := service.retrySelectedTest(ctx, req, 1, "unknown"); !errors.Is(err, ErrRetryStale) {
		t.Fatalf("unknown selection: %v", err)
	}
	mux := http.NewServeMux()
	service.Register(mux)
	params := map[string]any{"repository": target.Repository, "number": target.Number, "baseSha": target.BaseSHA, "headSha": target.HeadSHA, "revision": settings.Revision, "attempt": 1, "testId": sdk.ID}
	post := func(operation string, want int) {
		t.Helper()
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		response := reviewRequest(t, mux, "POST", route+operation, "http://localhost", "127.0.0.1:39282", string(body))
		if response.Code != want {
			t.Fatalf("%s returned %v: %s", operation, response.Code, response.Body.String())
		}
	}
	params["testId"] = 42
	post("/retry-test", http.StatusBadRequest)
	params["testId"] = sdk.ID
	post("/refresh-checks", http.StatusBadRequest)
	post("/retry-test", http.StatusOK)
	if err := service.retrySelectedTest(ctx, req, 1, deploy.ID); !errors.Is(err, ErrRetryStale) {
		t.Fatalf("duplicate retry: %v", err)
	}
	step()
	step()
	job := workerJob(t, db, target, "analyzed")
	tests := job.Evidence.Tests
	if len(tests) != 2 || tests[0].Status != "passed" || tests[1].Status != "passed" || tests[1].TestID != sdk.ID || tests[1].Selection != "manual" || !slices.Equal(tests[1].Command, sdk.Command) || len(vm.keys) != 4 || vm.keys[0] != vm.keys[1] || vm.keys[1] == vm.keys[2] || len(gh.compares) != 1 || len(gh.trees) != 2 {
		t.Fatalf("manual retry changed another receipt or failed to bypass scope: %+v", tests)
	}
	if s := workerSettings(t, db); s.Revision != settings.Revision || s.policy(target.Repository).TestProfiles[0].ID != sdk.ID || len(model.rules) != 1 {
		t.Fatalf("manual test changed settings or reran Codex: %+v", s)
	}
}

func TestProfileSelectionInapplicableAndInfrastructureErrors(t *testing.T) {
	target := workerTestTarget("owner/alpha", 2, sourceRepository)
	p := exampleProfile("deploy", "deploy/test.py", "deploy")
	for _, tc := range []struct {
		name, testID, changed string
		changeErr, entryErr   error
		want                  string
		entries               int
	}{
		{"outside scope", "", "deployment/config", nil, nil, "not_applicable", 0},
		{"absent entry", "", "deploy", nil, nil, "not_applicable", 1},
		{"manual still checks entry", "deploy", "sdk/code", nil, nil, "not_applicable", 1},
		{"incomplete comparison", "", "", errors.New("compare unavailable"), nil, "error", 0},
		{"entry access failed", "", "deploy/foo", nil, errors.New("tree unavailable"), "error", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
			gh := &profileGitHub{workerGitHub: &workerGitHub{targets: []Target{target}}, paths: []string{tc.changed}, changesError: tc.changeErr, entryError: tc.entryErr}
			cfg := workerSettings(t, db).Config
			cfg.PublishRepositories = []string{}
			settings := configureWorker(t, db, cfg)
			vm := &evidenceVM{}
			service := newService(ctx, func() {}, db, gh, connectedCLI{}, &evidenceReviewer{})
			service.vm = vm
			now := time.Now()
			service.scan(ctx, settings, now)
			if err := service.processNext(ctx, settings, now); err != nil {
				t.Fatal(err)
			}
			if len(workerJob(t, db, target, "analyzed").Evidence.Tests) != 0 {
				t.Fatal("test configured before request")
			}
			cfg.RepositoryPolicies = map[string]RepositoryPolicy{target.Repository: {TestProfiles: []TestProfile{p}, TestCommand: []string{"unrelated"}}}
			settings = configureWorker(t, db, cfg)
			req := RetryRequest{Repository: target.Repository, Number: target.Number, BaseSHA: target.BaseSHA, HeadSHA: target.HeadSHA, Revision: settings.Revision}
			if err := service.retrySelectedTest(ctx, req, 0, tc.testID); err != nil {
				t.Fatal(err)
			}
			if err := service.advanceEvidence(ctx, "test", now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			a := workerJob(t, db, target, "analyzed").Evidence.Tests[0]
			if a.Status != tc.want || len(vm.keys) != 0 || len(gh.inspected) != tc.entries || (a.Error == "" && a.SelectionReason == "") {
				t.Fatalf("attempt %+v, VM %v, entries %v", a, vm.keys, gh.inspected)
			}
		})
	}
}

func TestTestProfileValidationAndLegacyCompatibility(t *testing.T) {
	profile := exampleProfile("a", "tests/verify.py", "docs/")
	if !validTestProfiles([]TestProfile{profile}) || validTestProfiles([]TestProfile{profile, profile}) {
		t.Fatal("profile ID validation")
	}
	for _, s := range []string{"", "../README.md", "docs/../README", "/etc/passwd", "docs/**", "./tests", "foo//bar", ".git/config", "a\nb"} {
		if validTestPath(s) {
			t.Errorf("accepted invalid path %q", s)
		}
	}
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"python3", "deploy/scripts/test_configure.py"}, "deploy/scripts/test_configure.py"},
		{[]string{"./tools/test"}, "tools/test"},
		{[]string{"go", "test", "./pkg/unit"}, ""},
		{[]string{"sh", "-c", "./tests/quick.sh"}, ""},
		{[]string{"python3", "-m", "sdk.tests"}, ""},
		{[]string{"node", "node_modules/tools"}, ""},
	} {
		if got := legacyTestEntry(tc.argv); got != tc.want {
			t.Errorf("legacy %q: %q != %q", tc.argv, got, tc.want)
		}
	}
	policy := RepositoryPolicy{TestProfiles: []TestProfile{profile}}
	auto, _ := newTestAttempt(policy, "", "base", 1, time.Now())
	manual, _ := newTestAttempt(policy, "a", "base", 2, time.Now())
	policy.TestProfiles = append(policy.TestProfiles, exampleProfile("b", "another/file.sh"))
	if auto.matchesPolicy(policy, nil) || !manual.matchesPolicy(policy, nil) {
		t.Fatal("pending recipes did not bind relevant configuration")
	}
	policy.TestProfiles[0].Command = []string{"changed"}
	if manual.matchesPolicy(policy, nil) {
		t.Fatal("manual recipe changed before delivery")
	}
}

func TestRetargetPreservesSubmittedTestAndSelectsFromNewComparison(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	target := workerTestTarget("owner/alpha", 9, sourceRepository)
	sdk, deploy := exampleProfile("sdk", "test_sdk.py", "sdk"), exampleProfile("deploy", "test_deploy.py", "deploy")
	cfg := workerSettings(t, db).Config
	cfg.PublishRepositories = []string{}
	cfg.RepositoryPolicies = map[string]RepositoryPolicy{target.Repository: {TestProfiles: []TestProfile{sdk, deploy}}}
	settings := configureWorker(t, db, cfg)
	gh := &profileGitHub{workerGitHub: &workerGitHub{targets: []Target{target}}, paths: []string{"sdk/main.go"}, files: map[string]bool{sdk.EntryPath: true, deploy.EntryPath: true}}
	vm := &evidenceVM{}
	service := newService(ctx, func() {}, db, gh, connectedCLI{}, &evidenceReviewer{})
	service.vm = vm
	now := time.Now()
	service.scan(ctx, settings, now)
	if err := service.processNext(ctx, settings, now); err != nil {
		t.Fatal(err)
	}
	step := func() {
		t.Helper()
		now = now.Add(time.Minute)
		if err := service.advanceEvidence(ctx, "test", now); err != nil {
			t.Fatal(err)
		}
	}
	step()
	job := workerJob(t, db, target, "analyzed")
	if job.Evidence.Tests[0].TaskID == "" {
		t.Fatal("expected submitted VM task")
	}
	newTarget := target
	newTarget.BaseSHA = strings.Repeat("e", 40)
	gh.paths = []string{"deploy/chart.yaml"}
	gh.merge = func(t Target) (string, error) { return t.BaseSHA, nil }
	gh.current = func(Target) (Target, error) { return newTarget, nil }
	gh.targets = []Target{newTarget}
	service.scan(ctx, settings, now)
	req := RetryRequest{Repository: target.Repository, Number: target.Number, BaseSHA: target.BaseSHA, HeadSHA: target.HeadSHA, Revision: settings.Revision}
	if _, err := service.reviewLatest(ctx, req); err != nil {
		t.Fatal(err)
	}
	step()
	step()
	step()
	job = workerJob(t, db, newTarget, "queued")
	attempts := job.Evidence.Tests
	if len(attempts) != 2 || attempts[0].SelectionBaseSHA != target.BaseSHA || attempts[1].SelectionBaseSHA != newTarget.BaseSHA || attempts[0].TestID != sdk.ID || attempts[1].TestID != deploy.ID || attempts[0].Status != "passed" || attempts[1].Status != "passed" || len(vm.keys) != 4 || vm.keys[0] != vm.keys[1] || vm.keys[2] != vm.keys[3] || vm.keys[0] == vm.keys[2] || len(gh.compares) != 2 || gh.compares[1].BaseSHA != newTarget.BaseSHA {
		t.Fatalf("retargeted receipts: %+v, comparisons %+v", attempts, gh.compares)
	}
}
