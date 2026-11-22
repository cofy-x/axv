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
	post    func(Target, string, string) (string, error)
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

func (g *workerGitHub) Publish(_ context.Context, target Target, _ string, marker, body string) (string, error) {
	return g.post(target, marker, body)
}

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
		Enabled: true, PublishRepositories: []string{"owner/alpha", "owner/beta"}, DailyLimit: 10,
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
	workerJob(t, ledger, ordinary, "publishing")
	github.current = func(target Target) (Target, error) {
		target.HeadSHA = strings.Repeat("c", 40)
		target.Requested = true
		return target, nil
	}
	if err := service.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
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

func TestWorkerRecoversPublicationWithoutRepeatingCodex(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "jobs.sqlite")
	ledger := testReviewStore(t, filename, true)
	reviewer := &countedReviewer{}
	target := workerTestTarget("owner/alpha", 4, sourceRepository)
	calls := 0
	firstMarker := ""
	const link = "https://github.com/owner/alpha/pull/4#issuecomment-42"
	github := &workerGitHub{targets: []Target{target}, post: func(_ Target, marker, body string) (string, error) {
		calls++
		if !strings.Contains(body, "main.go:12") || !strings.Contains(body, "Automated Codex review") {
			t.Fatalf("unexpected review comment: %s", body)
		}
		if calls == 1 {
			firstMarker = marker
			return "", errors.New("Connection interrupted after submission.")
		}
		if marker != firstMarker {
			t.Fatal("publication retry changed identity")
		}
		return link, nil
	}}
	service := newService(ctx, func() {}, ledger, github, connectedCLI{}, reviewer)
	saved := workerSettings(t, ledger)
	now := time.Now()
	service.scan(ctx, saved, now)
	for range 2 {
		if err := service.processNext(ctx, saved, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := ledger.db.Close(); err != nil {
		t.Fatal(err)
	}
	ledger = testReviewStore(t, filename, false)
	service = newService(ctx, func() {}, ledger, github, connectedCLI{}, reviewer)
	if err := service.processNext(ctx, workerSettings(t, ledger), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	view, err := ledger.history(ctx, "owner")
	if err != nil || len(view) != 1 || view[0].Status != "published" || view[0].CommentURL != link || calls != 2 || len(reviewer.numbers) != 1 {
		t.Fatalf("incorrect recovery: %#v, publish calls=%d, model calls=%v, err=%v", view, calls, reviewer.numbers, err)
	}
	provider := github
	provider.targets[0].BaseSHA = strings.Repeat("d", 40)
	service.scan(ctx, saved, now.Add(3*time.Minute))
	view, err = ledger.history(ctx, "owner")
	if err != nil || len(view) != 1 || view[0].Status != "published" || view[0].BaseSHA != target.BaseSHA {
		t.Fatalf("a new raw base duplicated an already reviewed head: %#v, %v", view, err)
	}
}

func TestWorkerHonorsRepositoryPublishingAndDailyCap(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	outside := workerTestTarget("acme/outside", 1, sourceRequested)
	requested := workerTestTarget("owner/alpha", 2, sourceRequested)
	watched := workerTestTarget("owner/bravo", 3, sourceRepository)
	saved := configureWorker(t, ledger, Config{
		Repositories:        []string{requested.Repository, watched.Repository},
		PublishRepositories: []string{requested.Repository}, DailyLimit: 2, IntervalMinutes: 1, Enabled: true,
	})
	var posted []string
	github := &workerGitHub{targets: []Target{requested, watched, outside}, post: func(target Target, _, _ string) (string, error) {
		posted = append(posted, target.Repository)
		return "https://github.com/owner/alpha/pull/2#issuecomment-42", nil
	}}
	reviewer := &countedReviewer{}
	service := newService(ctx, func() {}, ledger, github, connectedCLI{}, reviewer)
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
	workerJob(t, ledger, requested, "publishing")
	workerJob(t, ledger, watched, "queued")
	if budget, err := ledger.budget(ctx, now); err != nil || budget.Used != 2 {
		t.Fatalf("daily budget = %#v, err=%v", budget, err)
	}
	if err := service.processNext(ctx, saved, now); err != nil {
		t.Fatal("publication at the daily cap:", err)
	}
	workerJob(t, ledger, requested, "published")
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
	if len(posted) != 1 || posted[0] != requested.Repository || len(reviewer.numbers) != 3 {
		t.Fatalf("posted=%v, model calls=%v", posted, reviewer.numbers)
	}
}

func TestPendingPublicationStaysLocalAfterRepositoryRemoval(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	saved := workerSettings(t, ledger)
	selected := workerTestTarget("owner/alpha", 1, sourceRequested)
	github := &workerGitHub{targets: []Target{selected}}
	service := newService(ctx, func() {}, ledger, github, connectedCLI{}, &countedReviewer{})
	now := time.Now()
	service.scan(ctx, saved, now)
	if err := service.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	workerJob(t, ledger, selected, "publishing")
	config := saved.Config
	config.Repositories = []string{"owner/beta"}
	config.PublishRepositories = []string{"owner/beta"}
	saved = configureWorker(t, ledger, config)
	if err := service.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	if result := workerJob(t, ledger, selected, "analyzed"); len(result.Findings) != 1 || result.CommentURL != "" {
		t.Fatalf("repository removal did not preserve findings locally: %#v", result)
	}
}

func TestRetryResumesOneFailedPublicationAtDailyCap(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	saved := workerSettings(t, ledger)
	config := saved.Config
	config.DailyLimit = 1
	saved = configureWorker(t, ledger, config)
	selected := workerTestTarget("owner/alpha", 1, sourceRepository)
	other := workerTestTarget("owner/beta", 2, sourceRepository)
	const link = "https://github.com/owner/alpha/pull/1#issuecomment-42"
	var marker string
	github := &workerGitHub{targets: []Target{selected, other}, post: func(target Target, currentMarker, _ string) (string, error) {
		if target.Number != selected.Number {
			t.Fatalf("published a different pull request: %#v", target)
		}
		if marker == "" {
			marker = currentMarker
			return "", ErrGitHubForbidden
		}
		if currentMarker != marker {
			t.Fatal("the targeted publish retry changed the deduplication marker")
		}
		return link, nil
	}}
	reviewer := &countedReviewer{}
	service := newService(ctx, func() {}, ledger, github, connectedCLI{}, reviewer)
	mux := http.NewServeMux()
	service.Register(mux)
	now := time.Now()
	service.scan(ctx, saved, now)
	for range 2 {
		if err := service.processNext(ctx, saved, now); err != nil {
			t.Fatal(err)
		}
	}
	job := workerJob(t, ledger, selected, "failed")
	retryReview(t, mux, job, saved.Revision, http.StatusOK)
	if retried := workerJob(t, ledger, selected, "publishing"); len(retried.Findings) != 1 || retried.attempts != 0 {
		t.Fatalf("targeted retry discarded findings or failed to reset publish attempts: %#v", retried)
	}
	retryReview(t, mux, job, saved.Revision, http.StatusConflict)
	workerJob(t, ledger, other, "queued")
	if err := service.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	if result := workerJob(t, ledger, selected, "published"); result.CommentURL != link {
		t.Fatalf("unexpected publication result: %#v", result)
	}
	if budget, err := ledger.budget(ctx, now); err != nil || budget.Used != 1 || len(reviewer.numbers) != 1 {
		t.Fatalf("publication retry spent another review: budget=%#v, model calls=%v, err=%v", budget, reviewer.numbers, err)
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
