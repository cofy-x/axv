package githubreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type workerGitHub struct {
	merge   func(Target) (string, error)
	checks  func(Checks) (Checks, error)
	targets []Target
	current func(Target) (Target, error)
}

func (g *workerGitHub) Discover(context.Context, []string, string, bool) ([]Target, error) {
	return g.targets, nil
}

func (g *workerGitHub) Current(_ context.Context, target Target, _ string) (Target, error) {
	if g.current != nil {
		return g.current(target)
	}
	target.Requested = target.Source == sourceRequested
	return target, nil
}

func (*workerGitHub) Token(context.Context, string) (string, error) { return "fixture-token", nil }

func (g *workerGitHub) MergeBase(_ context.Context, _ string, t Target) (string, error) {
	if g.merge != nil {
		return g.merge(t)
	}
	return strings.Repeat("a", 40), nil
}

func (g *workerGitHub) Checks(_ context.Context, _ Target, _ string, previous Checks) (Checks, error) {
	if g.checks != nil {
		return g.checks(previous)
	}
	return Checks{Status: "empty"}, nil
}

type connectedCLI struct{}

func (connectedCLI) Inspect(_ context.Context, provider string) Connection {
	return Connection{Available: true, Connected: true, Account: map[string]string{"github": "owner", "codex": "ChatGPT"}[provider]}
}
func (connectedCLI) Connect(context.Context, string) error { return nil }
func (connectedCLI) Stop(string)                           {}

type countedReviewer struct {
	numbers       []int
	authorContext *AuthorContext
}

func (r *countedReviewer) Review(_ context.Context, target Target, _, _ string, _ func(string) error) ([]Finding, error) {
	r.numbers = append(r.numbers, target.Number)
	r.authorContext = target.authorContext
	return []Finding{{Path: "main.go", Line: 12, Side: "head", Severity: "medium", Title: "Handle nil input", Body: "An empty input causes a panic."}}, nil
}

func workerTestTarget(repo string, number int, source string) Target {
	return Target{Repository: repo, Number: number, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40), Source: source}
}

func testReviewStore(t *testing.T, filename string, initialize bool) *store {
	t.Helper()
	ctx := context.Background()
	ledger, err := openStore(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.db.Close() })
	if !initialize {
		return ledger
	}
	if err := ledger.attach(ctx, "github", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.attach(ctx, "codex", "chatgpt"); err != nil {
		t.Fatal(err)
	}
	configureWorker(t, ledger, Config{
		Repositories: []string{"owner/alpha", "owner/beta"}, IntervalMinutes: 1,
		Enabled: true, PublishRepositories: []string{}, DailyLimit: 10,
	})
	return ledger
}

func configureWorker(t *testing.T, ledger *store, config Config) savedSettings {
	t.Helper()
	config.Revision = workerSettings(t, ledger).Revision
	if err := ledger.configure(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	return workerSettings(t, ledger)
}

func workerSettings(t *testing.T, ledger *store) savedSettings {
	t.Helper()
	saved, err := ledger.settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func workerJob(t *testing.T, ledger *store, target Target, status string) Job {
	t.Helper()
	job, err := ledger.job(t.Context(), "owner", target.Repository, target.Number, target.HeadSHA)
	if err != nil || job.Status != status {
		t.Fatalf("%s#%d status = %q, want %q, err=%v", target.Repository, target.Number, job.Status, status, err)
	}
	return job
}

func reviewRequest(t *testing.T, mux *http.ServeMux, method, path, origin, remote, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	req.RemoteAddr = remote
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	return response
}

func retryReview(t *testing.T, mux *http.ServeMux, job Job, revision uint64, want int) {
	t.Helper()
	body, err := json.Marshal(RetryRequest{
		Repository: job.Repository, Number: job.Number, BaseSHA: job.BaseSHA, HeadSHA: job.HeadSHA, Revision: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := reviewRequest(t, mux, "POST", route+"/retry", "http://localhost", "127.0.0.1:39282", string(body))
	if response.Code != want {
		t.Fatalf("retry returned %d, want %d: %s", response.Code, want, response.Body.String())
	}
}

func TestWorkerMergesDiscoveryAndSkipsSupersededReview(t *testing.T) {
	ctx := context.Background()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	ordinary := workerTestTarget("Owner/Alpha", 1, sourceRepository)
	direct := workerTestTarget("other/repo", 2, sourceRequested)
	duplicate := ordinary
	duplicate.Repository = "owner/alpha"
	duplicate.Source = sourceRequested
	draft := workerTestTarget("owner/beta", 3, sourceRepository)
	draft.Draft = true
	github := &workerGitHub{targets: []Target{ordinary, duplicate, direct, draft}}
	reviewer := &countedReviewer{}
	service := newService(ctx, func() {}, ledger, github, connectedCLI{}, reviewer)
	saved := workerSettings(t, ledger)
	now := time.Now()
	service.scan(ctx, saved, now)
	service.scan(ctx, saved, now)
	view, err := ledger.history(ctx, "owner")
	if err != nil || len(view) != 2 || view[0].Source != sourceRequested || view[1].Source != sourceRequested {
		t.Fatalf("incorrectly merged targets: %#v, %v", view, err)
	}
	if err := service.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	if len(reviewer.numbers) != 1 || reviewer.numbers[0] != 2 {
		t.Fatalf("review order = %v", reviewer.numbers)
	}
	workerJob(t, ledger, direct, "analyzed")
	if err := service.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	workerJob(t, ledger, ordinary, "analyzed")
	newer := duplicate
	newer.HeadSHA = strings.Repeat("c", 40)
	github.targets = []Target{newer}
	service.scan(ctx, saved, now.Add(time.Minute))
	workerJob(t, ledger, newer, "queued")
	if job := workerJob(t, ledger, ordinary, "stale"); len(job.Findings) == 0 {
		t.Fatalf("superseded review was not kept locally: %#v", job)
	}
}

func TestWorkerDefersOneUnavailableTargetWithoutBlockingOthers(t *testing.T) {
	ctx := context.Background()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	first := workerTestTarget("owner/alpha", 1, sourceRepository)
	second := workerTestTarget("owner/beta", 2, sourceRepository)
	provider := &workerGitHub{targets: []Target{first, second}, current: func(target Target) (Target, error) {
		if target.Number == 1 {
			return Target{}, errors.New("GitHub returned HTTP 404.")
		}
		return target, nil
	}}
	runner := &countedReviewer{}
	service := newService(ctx, func() {}, ledger, provider, connectedCLI{}, runner)
	saved := workerSettings(t, ledger)
	now := time.Now()
	service.scan(ctx, saved, now)
	for range 2 {
		if err := service.processNext(ctx, saved, now); err != nil {
			t.Fatal(err)
		}
	}
	if len(runner.numbers) != 1 || runner.numbers[0] != second.Number {
		t.Fatalf("a failed preflight blocked other jobs: %v", runner.numbers)
	}
}

func TestReleaseKeepsLegacyPublicationLocal(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "jobs.sqlite")
	ledger := testReviewStore(t, filename, true)
	pending := workerTestTarget("owner/alpha", 4, sourceRepository)
	published := workerTestTarget("owner/alpha", 5, sourceRepository)
	reviewer := &countedReviewer{}
	service := newService(ctx, func() {}, ledger, &workerGitHub{targets: []Target{pending, published}}, connectedCLI{}, reviewer)
	saved := workerSettings(t, ledger)
	now := time.Now()
	service.scan(ctx, saved, now)
	for range 2 {
		if err := service.processNext(ctx, saved, now); err != nil {
			t.Fatal(err)
		}
	}
	const link = "https://github.com/owner/alpha/pull/5#issuecomment-42"
	_, err := ledger.db.ExecContext(ctx, `
		UPDATE review_settings SET publish_repositories='["owner/alpha"]';
		UPDATE review_jobs SET status='publishing', error='Pending previous publication.' WHERE number=4;
		UPDATE review_jobs SET status='published', comment_url=? WHERE number=5;
		PRAGMA user_version=6;
	`, link)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.db.Close(); err != nil {
		t.Fatal(err)
	}
	ledger = testReviewStore(t, filename, false)
	if saved := workerSettings(t, ledger); len(saved.PublishRepositories) != 0 {
		t.Fatalf("publication carried into release settings: %v", saved.PublishRepositories)
	}
	if job := workerJob(t, ledger, pending, "analyzed"); len(job.Findings) != 1 || job.CommentURL != "" || job.Error != "" {
		t.Fatalf("pending publication did not become a local finding: %+v", job)
	}
	if job := workerJob(t, ledger, published, "published"); job.CommentURL != link {
		t.Fatalf("historical link was lost: %+v", job)
	}
	service = newService(ctx, func() {}, ledger, &workerGitHub{}, connectedCLI{}, reviewer)
	if err := service.processNext(ctx, workerSettings(t, ledger), now.Add(time.Minute)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("new release revived old work: %v", err)
	}
	if len(reviewer.numbers) != 2 {
		t.Fatalf("legacy results spent additional reviews: %v", reviewer.numbers)
	}
}

func TestWorkerHonorsDailyCapForWatchedAndRequestedPullRequests(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	outside := workerTestTarget("acme/outside", 1, sourceRequested)
	requested := workerTestTarget("owner/alpha", 2, sourceRequested)
	watched := workerTestTarget("owner/bravo", 3, sourceRepository)
	saved := configureWorker(t, ledger, Config{
		Repositories:        []string{requested.Repository, watched.Repository},
		PublishRepositories: []string{}, DailyLimit: 2, IntervalMinutes: 1, Enabled: true,
	})
	reviewer := &countedReviewer{}
	service := newService(ctx, func() {}, ledger, &workerGitHub{targets: []Target{requested, watched, outside}}, connectedCLI{}, reviewer)
	now := time.Now()
	service.scan(ctx, saved, now)
	for range 2 {
		if err := service.processNext(ctx, saved, now); err != nil {
			t.Fatal(err)
		}
	}
	if job := workerJob(t, ledger, outside, "analyzed"); len(job.Findings) == 0 {
		t.Fatal("the directly requested off-list review did not retain its findings")
	}
	workerJob(t, ledger, requested, "analyzed")
	if budget, err := ledger.budget(ctx, now); err != nil || budget.Used != 2 {
		t.Fatalf("daily budget = %#v, err=%v", budget, err)
	}
	if err := service.processNext(ctx, saved, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("worker started another analysis at the daily cap: %v", err)
	}
	workerJob(t, ledger, watched, "queued")
	config := saved.Config
	config.DailyLimit = 3
	saved = configureWorker(t, ledger, config)
	if err := service.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	workerJob(t, ledger, watched, "analyzed")
	if len(reviewer.numbers) != 3 {
		t.Fatalf("model calls=%v", reviewer.numbers)
	}
}

func TestRetryRevalidatesHeadAndUsesNewBaseForOnePullRequest(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	saved := workerSettings(t, ledger)
	selected := workerTestTarget("owner/alpha", 1, sourceRepository)
	other := workerTestTarget("owner/beta", 2, sourceRepository)
	current := selected
	current.BaseSHA = strings.Repeat("c", 40)
	current.HeadSHA = strings.Repeat("d", 40)
	github := &workerGitHub{targets: []Target{selected, other}, current: func(target Target) (Target, error) {
		if target.Number != selected.Number {
			t.Fatalf("looked up a different pull request: %#v", target)
		}
		return current, nil
	}}
	service := newService(ctx, func() {}, ledger, github, connectedCLI{}, &countedReviewer{})
	mux := http.NewServeMux()
	service.Register(mux)
	service.scan(ctx, saved, time.Now())
	for _, target := range []Target{selected, other} {
		job := workerJob(t, ledger, target, "queued")
		job.Findings = []Finding{{Path: "main.go", Line: 12, Title: "Stale result"}}
		if err := service.setOutcome(ctx, job, "stale", errOutdated); err != nil {
			t.Fatal(err)
		}
	}
	job := workerJob(t, ledger, selected, "stale")
	retryReview(t, mux, job, saved.Revision, http.StatusConflict)
	workerJob(t, ledger, selected, "stale")
	current.HeadSHA = selected.HeadSHA
	retryReview(t, mux, job, saved.Revision-1, http.StatusConflict)
	retryReview(t, mux, job, saved.Revision, http.StatusOK)
	if retried := workerJob(t, ledger, selected, "queued"); retried.BaseSHA != current.BaseSHA || len(retried.Findings) != 0 {
		t.Fatalf("targeted retry kept the stale base or findings: %#v", retried)
	}
	if unchanged := workerJob(t, ledger, other, "stale"); unchanged.BaseSHA != other.BaseSHA || len(unchanged.Findings) != 1 {
		t.Fatalf("targeted retry changed the other job: %#v", unchanged)
	}
}

func TestGitHubReviewRoutesAreLocalAndRevisionChecked(t *testing.T) {
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), false)
	service := newService(context.Background(), func() {}, ledger, &workerGitHub{}, connectedCLI{}, &countedReviewer{})
	mux := http.NewServeMux()
	service.Register(mux)
	send := func(method, path, origin, remote, body string) *httptest.ResponseRecorder {
		return reviewRequest(t, mux, method, path, origin, remote, body)
	}
	const local = "127.0.0.1:39282"
	const body = `{"repositories":["owner/alpha"],"publishRepositories":[],"repositoryPolicies":{"owner/alpha":{"rules":"Check timeouts.","testCommand":[]}},"dailyLimit":10,"intervalMinutes":2,"enabled":false,"revision":1}`
	for _, response := range []*httptest.ResponseRecorder{
		send("GET", route, "", "192.0.2.3:1234", ""),
		send("PUT", route+"/config", "https://outside.example", local, body),
		send("POST", route+"/connect/github", "", local, ""),
		send("POST", route+"/retry", "https://outside.example", local, `{}`),
		send("POST", route+"/review-pr", "https://outside.example", local, `{}`),
	} {
		if response.Code != 403 {
			t.Fatalf("unsafe route returned %d: %s", response.Code, response.Body.String())
		}
	}
	for _, check := range []struct {
		body string
		want int
	}{
		{strings.Replace(body, `"publishRepositories"`, `"PublishRepositories"`, 1), 400},
		{strings.Replace(body, `"dailyLimit":10`, `"dailyLimit":10,"dailyLimit":100`, 1), 400},
		{strings.Replace(body, `"dailyLimit":10`, `"dailyLimit":0`, 1), 400},
		{strings.Replace(body, `"testCommand"`, `"testCommands"`, 1), 400},
		{strings.Replace(body, `"publishRepositories":[]`, `"publishRepositories":["owner/alpha"]`, 1), 400},
		{strings.Replace(body, `"owner/alpha"`, `"outside/repo"`, 1), 400},
		{body, 200},
		{body, 409},
	} {
		if response := send("PUT", route+"/config", "http://localhost", local, check.body); response.Code != check.want {
			t.Fatalf("PUT returned %d, want %d: %s", response.Code, check.want, response.Body.String())
		}
	}
	if response := send("POST", route+"/connect/github", "http://localhost", local, "{}"); response.Code != 200 ||
		!strings.Contains(response.Body.String(), `"connected":true,"account":"owner"`) {
		t.Fatalf("existing CLI login was not attached: %d %s", response.Code, response.Body.String())
	}
}

func TestReviewUsesAuthorContextFromVerifiedRevision(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	queued := workerTestTarget("owner/alpha", 23, sourceRepository)
	queued.Title = "At discovery"
	current := &AuthorContext{Title: "At review start", Description: "Reject stale profiles."}
	github := &workerGitHub{targets: []Target{queued}, current: func(t Target) (Target, error) {
		t.Title = current.Title
		t.authorContext = current
		return t, nil
	}}
	runner := &countedReviewer{}
	s := newService(ctx, func() {}, ledger, github, connectedCLI{}, runner)
	cfg := workerSettings(t, ledger).Config
	cfg.PublishRepositories = []string{}
	saved := configureWorker(t, ledger, cfg)
	now := time.Now()
	s.scan(ctx, saved, now)
	if err := s.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	job := workerJob(t, ledger, queued, "analyzed")
	if runner.authorContext == nil || *runner.authorContext != *current || job.Evidence.Review.AuthorContext == nil || *job.Evidence.Review.AuthorContext != *current {
		t.Fatal("missing or stale PR author context in the model input or review receipt")
	}
	// Editing only the PR text should leave a completed analysis reproducible.
	latest := queued
	latest.Title = "Title edited after review"
	github.targets = []Target{latest}
	s.scan(ctx, saved, now)
	job = workerJob(t, ledger, queued, "analyzed")
	if job.Evidence.Review.AuthorContext.Title != current.Title || job.Title != latest.Title {
		t.Fatal("an edit replaced the captured metadata")
	}
	if err := s.processNext(ctx, saved, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("metadata-only edit queued a new review: %v", err)
	}
	// A different head arriving between discovery and preflight cannot attach
	// its description to the obsolete code.
	newHead := queued
	newHead.HeadSHA = strings.Repeat("c", 40)
	github.targets = []Target{newHead}
	s.scan(ctx, saved, now)
	github.current = func(t Target) (Target, error) {
		t.HeadSHA = strings.Repeat("d", 40)
		t.authorContext = &AuthorContext{Title: "Different head", Description: "Different feature."}
		return t, nil
	}
	if err := s.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	if stale := workerJob(t, ledger, newHead, "stale"); stale.Evidence != nil || len(runner.numbers) != 1 {
		t.Fatal("different revision metadata reached the model")
	}
}
