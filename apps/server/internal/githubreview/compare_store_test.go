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

func TestCandidateComparisonAdmissionHistoryAndReplay(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "review.sqlite")
	db := testReviewStore(t, filename, true)
	target := workerTestTarget("owner/alpha", 667, sourceRepository)
	j := reviewedCandidates(t, db, target, []Finding{{Path: "src/core.py", Line: 12, Side: "head", Title: "Alias ordering changes", Severity: "medium"}})
	merge := strings.Repeat("f", 40)
	_, err := db.db.ExecContext(ctx, `UPDATE review_jobs SET merge_base_sha=? WHERE repository=? AND number=?`, merge, j.Repository, j.Number)
	if err != nil {
		t.Fatal(err)
	}
	cfg := workerSettings(t, db).Config
	cfg.Enabled = false
	cfg.PublishRepositories = []string{}
	runtime := ComparisonRuntime{ID: "python", Name: "Python VM", Command: []string{"python3", "-c"}}
	cfg.RepositoryPolicies = map[string]RepositoryPolicy{target.Repository: {ComparisonRuntimes: []ComparisonRuntime{runtime}}}
	saved := configureWorker(t, db, cfg)
	checked := 0
	g := &workerGitHub{current: func(now Target) (Target, error) { checked++; return now, nil }}
	s := newService(ctx, func() {}, db, g, connectedCLI{}, &countedReviewer{})
	request := comparisonRequest{RetryRequest: RetryRequest{Repository: target.Repository, Number: target.Number, HeadSHA: target.HeadSHA, BaseSHA: target.BaseSHA, Revision: saved.Revision}, Account: "owner", CandidateID: j.Candidates[0].ID, RequestID: "cbcbb44c-49cd-47f2-a27a-a8033aae8522", RuntimeID: runtime.ID, Assertion: "from typing import Tuple\nassert Tuple[int]\n"}
	if err := s.requestComparison(ctx, request); !errors.Is(err, errComparisonVM) {
		t.Fatalf("unbound VM: %v", err)
	}
	s.vm = &evidenceVM{}
	stale := request
	stale.CandidateID = strings.Repeat("a", 64)
	if err := s.requestComparison(ctx, stale); !errors.Is(err, errCandidateConflict) {
		t.Fatalf("foreign candidate: %v", err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	params := map[string]any{"account": request.Account, "repository": request.Repository, "number": request.Number, "baseSha": request.BaseSHA, "headSha": request.HeadSHA, "revision": request.Revision, "candidateId": request.CandidateID, "requestId": request.RequestID, "runtimeId": request.RuntimeID, "assertion": request.Assertion}
	post := func(origin string, want int) {
		t.Helper()
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		res := reviewRequest(t, mux, "POST", route+"/candidate-comparison", origin, "127.0.0.1:19792", string(body))
		if res.Code != want {
			t.Fatalf("comparison API returned %d: %s", res.Code, res.Body.String())
		}
	}
	post("https://untrusted.example", 403)
	params["command"] = []string{"unconfigured-program"}
	post("http://localhost", 400)
	delete(params, "command")
	post("http://localhost", 200)
	if err := db.db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testReviewStore(t, filename, false)
	s.store = db
	run, err := db.nextComparison(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run.Baseline.TargetSHA != merge || run.Head.TargetSHA != target.HeadSHA || run.Runtime.Name != runtime.Name || run.Assertion != request.Assertion || run.version != 1 {
		t.Fatalf("lost frozen request: %+v", run)
	}
	if ok, err := db.comparisonCurrent(ctx, run); err != nil || !ok {
		t.Fatalf("current=%v err=%v", ok, err)
	}
	budget, err := db.budget(ctx, time.Now())
	if err != nil || budget.Used != 1 {
		t.Fatalf("comparison spent a model start: %+v %v", budget, err)
	}
	other := request
	other.RequestID = "abcbb44c-49cd-47f2-a27a-a8033aae8522"
	if err = s.requestComparison(ctx, other); !errors.Is(err, errComparisonBusy) {
		t.Fatalf("concurrent run: %v", err)
	}
	changed := request
	changed.Assertion += "pass"
	if err = s.requestComparison(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("ID reused with changed assertion: %v", err)
	}
	changed = request
	changed.RuntimeID = "shell"
	if err = s.requestComparison(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("ID reused with changed runtime: %v", err)
	}
	final := run
	final.Baseline.Log = "original baseline output"
	final.Head.Log = "original head output"
	final.Baseline.Command = []string{"python3", "-c", request.Assertion}
	final.Head.Command = []string{"python3", "-c", request.Assertion}
	final.Status = "completed"
	final.Outcome = "both_failed"
	if err = db.saveComparison(ctx, run, final); err != nil {
		t.Fatal(err)
	}
	if err = db.saveComparison(ctx, run, run); !errors.Is(err, ErrConflict) {
		t.Fatalf("delayed observer overwrote terminal state: %v", err)
	}
	if _, err = db.nextComparison(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("terminal run stayed runnable: %v", err)
	}
	current := candidateTestView(t, db, target)
	c := current.Candidates[0]
	if len(c.Comparisons) != 1 || c.Comparisons[0].Outcome != final.Outcome || c.Status != "pending" || c.Revision != 1 {
		t.Fatalf("candidate history or disposition: %+v", c)
	}
	replacement := target
	replacement.HeadSHA = strings.Repeat("c", 40)
	if err = db.enqueue(ctx, "owner", []Target{replacement}, []Target{replacement}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.comparisonCurrent(ctx, run); err != nil || ok {
		t.Fatalf("stale current=%v err=%v", ok, err)
	}
	if err = s.requestComparison(ctx, other); !errors.Is(err, ErrRetryStale) {
		t.Fatalf("started new comparison on a superseded head: %v", err)
	}
	summary := current.Candidates[0].Comparisons[0]
	if summary.Assertion != "" || summary.Head.Log != "" || summary.Baseline.Log != "" || len(summary.Head.Command) > 0 || len(summary.Baseline.Command) > 0 {
		t.Fatal("polling snapshot contains comparison payload")
	}
	get := func(want int) CandidateComparison {
		t.Helper()
		res := reviewRequest(t, mux, "GET", route+"/candidate-comparison/"+request.RequestID, "", "127.0.0.1:19792", "")
		if res.Code != want {
			t.Fatalf("comparison detail returned %d: %s", res.Code, res.Body.String())
		}
		var result CandidateComparison
		if want == http.StatusOK {
			if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	// A retained comparison must be readable with no live credential probe,
	// including immediately after a process restart before the next snapshot.
	s.auth = nil
	delete(s.observed, "github")
	detail := get(http.StatusOK)
	if detail.Assertion != request.Assertion || detail.Head.Log != final.Head.Log || detail.Baseline.Log != final.Baseline.Log {
		t.Fatalf("stale comparison detail incomplete: %+v", detail)
	}
	s.observed["github"] = observedConnection{value: Connection{Connected: true, Account: "another"}}
	get(http.StatusNotFound)
	s.observed["github"] = observedConnection{value: Connection{Connected: false}}
	get(http.StatusNotFound)
	s.observed["github"] = observedConnection{value: Connection{Connected: true, Account: "owner"}}
	get(http.StatusOK)
	calls := checked
	configureWorker(t, db, saved.Config)
	if err = s.requestComparison(ctx, request); err != nil || checked != calls {
		t.Fatalf("lost replay on changed revision: %v, lookups %d -> %d", err, calls, checked)
	}
	if after := candidateTestView(t, db, target); len(after.Candidates[0].Comparisons) != 1 {
		t.Fatal("lost historical comparison")
	}
	changed = request
	changed.Account = "another"
	if err = s.requestComparison(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("account mismatch: %v", err)
	}
	if err = db.attach(ctx, "github", "another"); err != nil {
		t.Fatal(err)
	}
	get(http.StatusNotFound)
	if err = db.attach(ctx, "github", ""); err != nil {
		t.Fatal(err)
	}
	get(http.StatusNotFound)
}

func TestComparisonRuntimeAndAssertionBounds(t *testing.T) {
	p := RepositoryPolicy{}
	shell, ok := p.comparisonRuntime("shell")
	if !ok || shell.Command[1] != "-ec" {
		t.Fatal("missing default POSIX runtime")
	}
	p.ComparisonRuntimes = []ComparisonRuntime{{ID: "r", Name: "Runtime", Command: []string{"interpreter", "-e"}}}
	if !p.valid() {
		t.Fatal("rejected configured runtime")
	}
	if _, ok = p.comparisonRuntime("shell"); ok {
		t.Fatal("an unsaved runtime was offered")
	}
	p.ComparisonRuntimes = append(p.ComparisonRuntimes, p.ComparisonRuntimes[0])
	if p.valid() {
		t.Fatal("duplicate runtime IDs")
	}
	q := comparisonRequest{Account: "o", CandidateID: strings.Repeat("a", 64), RequestID: "cbcbb44c-49cd-47f2-a27a-a8033aae8522", RuntimeID: "r", RetryRequest: RetryRequest{Repository: "o/r", Number: 1, BaseSHA: strings.Repeat("b", 40), HeadSHA: strings.Repeat("c", 40), Revision: 1}}
	for _, v := range []string{"", "   ", "x\x00y", "x\x7fy", strings.Repeat("測", 2731), strings.Repeat("x", 8193)} {
		q.Assertion = v
		if q.validComparison() {
			t.Fatalf("invalid assertion length %d", len(v))
		}
	}
	q.Assertion = "if True:\r\n\tpass"
	if !q.validComparison() {
		t.Fatal("valid multiline assertion")
	}
	q.Assertion = strings.Repeat("x", 8192)
	if !q.validComparison() {
		t.Fatal("maximum-size assertion")
	}
}
