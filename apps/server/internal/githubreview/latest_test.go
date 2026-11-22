package githubreview

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func latestReviewRef(target Target, revision uint64) RetryRequest {
	return RetryRequest{Repository: target.Repository, Number: target.Number, BaseSHA: target.BaseSHA, HeadSHA: target.HeadSHA, Revision: revision}
}

func TestReviewLatestWhilePausedSurvivesRestartAndHonorsBudget(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "jobs.sqlite")
	ledger := testReviewStore(t, filename, true)
	config := workerSettings(t, ledger).Config
	config.Enabled = false
	config.DailyLimit = 1
	saved := configureWorker(t, ledger, config)
	old := workerTestTarget("owner/alpha", 174, sourceRepository)
	current := old
	current.HeadSHA = strings.Repeat("c", 40)
	unselected := workerTestTarget("other/repo", 2, sourceRequested)
	next := workerTestTarget("owner/beta", 9, sourceRepository)
	targets := []Target{old, unselected, next}
	if err := ledger.enqueue(ctx, saved.account, targets, targets, time.Now()); err != nil {
		t.Fatal(err)
	}
	remote := &workerGitHub{current: func(target Target) (Target, error) {
		switch target.Number {
		case old.Number:
			return current, nil
		case next.Number:
			return next, nil
		default:
			t.Fatalf("a different PR was queried: %s#%d", target.Repository, target.Number)
			return Target{}, nil
		}
	}}
	reviewer := &countedReviewer{}
	service := newService(ctx, func() {}, ledger, remote, connectedCLI{}, reviewer)
	mux := http.NewServeMux()
	service.Register(mux)
	request := latestReviewRef(old, saved.Revision)
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := reviewRequest(t, mux, "POST", route+"/review-latest", "http://localhost", "127.0.0.1:39282", string(body))
	var result struct {
		Snapshot
		Selected selectedReview `json:"selectedReview"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Config.Enabled {
		t.Fatalf("selected PR response: %d %s", response.Code, response.Body)
	}
	if got := result.Selected; got.Repository != current.Repository || got.Number != current.Number || got.BaseSHA != current.BaseSHA || got.HeadSHA != current.HeadSHA || got.Status != "queued" {
		t.Fatalf("wrong selected comparison: %+v", got)
	}
	workerJob(t, ledger, old, "stale")
	workerJob(t, ledger, current, "queued")
	if err := ledger.db.Close(); err != nil {
		t.Fatal(err)
	}
	ledger = testReviewStore(t, filename, false)
	service = newService(ctx, func() {}, ledger, remote, connectedCLI{}, reviewer)
	if err := service.processNext(ctx, saved, time.Now()); err != nil {
		t.Fatal(err)
	}
	workerJob(t, ledger, current, "analyzed")
	workerJob(t, ledger, unselected, "queued")
	if _, err := service.reviewLatest(ctx, request); err != nil {
		t.Fatal(err)
	}
	workerJob(t, ledger, current, "analyzed")
	if _, err := service.reviewLatest(ctx, latestReviewRef(next, saved.Revision)); err != nil {
		t.Fatal(err)
	}
	if err := service.processNext(ctx, saved, time.Now()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("exhausted budget allowed work: %v", err)
	}
	if budget, err := ledger.budget(ctx, time.Now()); err != nil || budget.Used != 1 {
		t.Fatalf("budget: %+v, %v", budget, err)
	}
	if job := workerJob(t, ledger, next, "queued"); !job.manualReview {
		t.Fatal("selected PR was not held for the next budget window")
	}
	if len(reviewer.numbers) != 1 || reviewer.numbers[0] != old.Number || workerSettings(t, ledger).Enabled {
		t.Fatalf("unexpected reviews or polling change: %v", reviewer.numbers)
	}
}

func TestReviewLatestWithChangingBase(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	saved := workerSettings(t, ledger)
	current := workerTestTarget("owner/alpha", 174, sourceRepository)
	first := current
	github := &workerGitHub{
		current: func(Target) (Target, error) { return current, nil },
		merge:   func(target Target) (string, error) { return target.BaseSHA, nil },
	}
	runner := &countedReviewer{}
	service := newService(ctx, func() {}, ledger, github, connectedCLI{}, runner)
	if err := ledger.enqueue(ctx, saved.account, []Target{first}, []Target{first}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ref := latestReviewRef(first, saved.Revision)
	if _, err := service.reviewLatest(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := service.processNext(ctx, saved, time.Now()); err != nil {
		t.Fatal(err)
	}
	workerJob(t, ledger, first, "analyzed")
	current.BaseSHA = strings.Repeat("d", 40)
	if _, err := service.reviewLatest(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if job := workerJob(t, ledger, current, "queued"); job.BaseSHA != current.BaseSHA {
		t.Fatalf("requeued the wrong comparison: %+v", job.Target)
	}
	if err := service.processNext(ctx, saved, time.Now()); err != nil {
		t.Fatal(err)
	}
	workerJob(t, ledger, current, "analyzed")
	if len(runner.numbers) != 2 {
		t.Fatalf("wanted both comparisons reviewed, got %v", runner.numbers)
	}
}

func TestReviewLatestRevalidatesDirectRequestAndSettings(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	saved := workerSettings(t, ledger)
	direct := workerTestTarget("outside/repo", 13, sourceRequested)
	current := direct
	github := &workerGitHub{current: func(Target) (Target, error) { return current, nil }}
	service := newService(ctx, func() {}, ledger, github, connectedCLI{}, &countedReviewer{})
	if err := ledger.enqueue(ctx, saved.account, []Target{direct}, []Target{direct}, time.Now()); err != nil {
		t.Fatal(err)
	}
	request := latestReviewRef(direct, saved.Revision)
	if _, err := service.reviewLatest(ctx, request); !errors.Is(err, ErrRetryStale) {
		t.Fatalf("revoked direct request: %v", err)
	}
	current.Requested = true
	current.Draft = true
	if _, err := service.reviewLatest(ctx, request); !errors.Is(err, ErrRetryStale) {
		t.Fatalf("draft review request: %v", err)
	}
	current.Draft = false
	github.current = func(Target) (Target, error) {
		configureWorker(t, ledger, saved.Config)
		return current, nil
	}
	if _, err := service.reviewLatest(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed settings: %v", err)
	}
	if workerJob(t, ledger, direct, "queued").manualReview {
		t.Fatal("an ineligible request authorized a paused review")
	}
}

func TestReviewPRQueuesAnUnseenRequestAheadOfDiscovery(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	config := workerSettings(t, ledger).Config
	config.WatchOnly = true
	saved := configureWorker(t, ledger, config)
	unselected := workerTestTarget("owner/alpha", 12, sourceRepository)
	direct := workerTestTarget("outside/repo", 34, sourceRequested)
	direct.Requested = true
	if err := ledger.enqueue(ctx, saved.account, []Target{unselected}, []Target{unselected}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	remote := &workerGitHub{current: func(target Target) (Target, error) {
		if !strings.EqualFold(target.Repository, direct.Repository) || target.Number != direct.Number {
			t.Fatalf("queried the wrong PR: %+v", target)
		}
		return direct, nil
	}}
	reviewer := &countedReviewer{}
	service := newService(ctx, func() {}, ledger, remote, connectedCLI{}, reviewer)
	mux := http.NewServeMux()
	service.Register(mux)
	body, err := json.Marshal(reviewPRRequest{Repository: "Outside/Repo", Number: direct.Number, Revision: saved.Revision})
	if err != nil {
		t.Fatal(err)
	}
	queue := func() {
		response := reviewRequest(t, mux, "POST", route+"/review-pr", "http://localhost", "127.0.0.1:39282", string(body))
		var result struct {
			Selected selectedReview `json:"selectedReview"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("review selected PR: %d %s", response.Code, response.Body)
		}
		if got := result.Selected; got.Repository != direct.Repository || got.Number != direct.Number || got.BaseSHA != direct.BaseSHA || got.HeadSHA != direct.HeadSHA {
			t.Fatalf("wrong selected comparison: %+v", got)
		}
	}
	queue()
	if job := workerJob(t, ledger, direct, "queued"); !job.manualReview || job.BaseSHA != direct.BaseSHA {
		t.Fatalf("unverified queue entry: %+v", job)
	}
	if err := service.processNext(ctx, saved, time.Now()); err != nil {
		t.Fatal(err)
	}
	queue()
	workerJob(t, ledger, direct, "analyzed")
	workerJob(t, ledger, unselected, "queued")
	if used, err := ledger.budget(ctx, time.Now()); err != nil || used.Used != 1 || len(reviewer.numbers) != 1 {
		t.Fatalf("duplicate request or budget debit: %+v, %v, %v", used, err, reviewer.numbers)
	}
}

func TestReviewPRRejectsIneligibleTargetsAndChangedSettings(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"), true)
	saved := workerSettings(t, ledger)
	current := workerTestTarget("outside/repo", 34, sourceRepository)
	remote := &workerGitHub{current: func(Target) (Target, error) { return current, nil }}
	service := newService(ctx, func() {}, ledger, remote, connectedCLI{}, &countedReviewer{})
	request := reviewPRRequest{Repository: current.Repository, Number: current.Number, Revision: saved.Revision}
	if _, err := service.reviewSelected(ctx, request, nil); !errors.Is(err, errReviewOutOfScope) {
		t.Fatalf("outside PR without direct request: %v", err)
	}
	current.Requested = true
	current.Draft = true
	if _, err := service.reviewSelected(ctx, request, nil); !errors.Is(err, errReviewNotReady) {
		t.Fatalf("draft PR: %v", err)
	}
	current.Draft = false
	current.Closed = true
	if _, err := service.reviewSelected(ctx, request, nil); !errors.Is(err, errReviewNotReady) {
		t.Fatalf("closed PR: %v", err)
	}
	current.Closed = false
	remote.current = func(Target) (Target, error) {
		configureWorker(t, ledger, saved.Config)
		return current, nil
	}
	if _, err := service.reviewSelected(ctx, request, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("configuration changed during lookup: %v", err)
	}
	if _, err := ledger.job(ctx, saved.account, current.Repository, current.Number, current.HeadSHA); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ineligible pull request reached the queue: %v", err)
	}
}
