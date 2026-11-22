package githubreview

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reviewedCandidates(t *testing.T, db *store, target Target, findings []Finding) Job {
	t.Helper()
	ctx := t.Context()
	if err := db.enqueue(ctx, "owner", []Target{target}, []Target{target}, time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err := db.job(ctx, "owner", target.Repository, target.Number, target.HeadSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.startReview(ctx, job, time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err = db.job(ctx, "owner", target.Repository, target.Number, target.HeadSHA)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = "analyzed"
	job.Findings = findings
	if err = db.update(ctx, job); err != nil {
		t.Fatal(err)
	}
	return candidateTestView(t, db, target)
}
func candidateTestView(t *testing.T, db *store, target Target) Job {
	t.Helper()
	jobs, err := db.history(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Repository == target.Repository && j.Number == target.Number && j.HeadSHA == target.HeadSHA {
			return j
		}
	}
	t.Fatal("missing job")
	return Job{}
}

func TestCandidateDecisionKeepsVerifiedEvidenceAndTracksEachRevision(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "review.sqlite")
	db := testReviewStore(t, filename, true)
	first := workerTestTarget("owner/alpha", 7, sourceRepository)
	finding := Finding{Path: "app/config.go", Side: "head", Line: 80, Title: "Missing defaults prevent initialization", Body: "Unconfigured instances fail to start.", Severity: "medium"}
	original := reviewedCandidates(t, db, first, []Finding{finding})
	tests, err := json.Marshal([]VMTestAttempt{{
		Attempt: 1, Status: "failed", TaskID: "task-1", AttemptID: "attempt-1",
		CheckoutSHA: first.HeadSHA, Command: []string{"go", "test", "./app"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, "UPDATE review_jobs SET tests=? WHERE number=7", string(tests)); err != nil {
		t.Fatal(err)
	}
	original = candidateTestView(t, db, first)
	if len(original.EvidenceOptions) != 1 || original.EvidenceOptions[0].Kind != "test" ||
		original.EvidenceOptions[0].Status != "failed" || original.EvidenceOptions[0].SHA != first.HeadSHA {
		t.Fatalf("incorrect test evidence: %+v", original.EvidenceOptions)
	}
	c := original.Candidates[0]
	req := candidateDecision{Account: "owner", Target: first, CandidateID: c.ID, Revision: c.Revision, Note: "The pinned test reproduces the failure.", Status: "confirmed", EvidenceIDs: []string{"forged"}}
	if err := db.saveCandidate(ctx, req); !errors.Is(err, errCandidateConflict) {
		t.Fatal(err)
	}
	req.EvidenceIDs = []string{original.EvidenceOptions[0].ID}
	if err := db.saveCandidate(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := db.saveCandidate(ctx, req); !errors.Is(err, errCandidateConflict) {
		t.Fatalf("lost update: %v", err)
	}
	if err = db.db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testReviewStore(t, filename, false)
	old := candidateTestView(t, db, first).Candidates[0]
	if old.Revision != 2 || old.Status != "confirmed" || len(old.Evidence) != 1 || old.Evidence[0].Detail != "go test ./app" {
		t.Fatalf("decision lost: %+v", old)
	}
	second := first
	second.HeadSHA = strings.Repeat("c", 40)
	finding.Line = 103
	next := reviewedCandidates(t, db, second, []Finding{finding})
	fresh := next.Candidates[0]
	if fresh.TrackID != old.TrackID || fresh.ID == old.ID || fresh.Status != "pending" || len(fresh.Evidence) != 0 || len(fresh.History) != 1 || fresh.History[0].ID != old.ID || fresh.History[0].Line != 80 || fresh.History[0].Status != "confirmed" {
		t.Fatalf("incorrect revision history: %+v", fresh)
	}
	req.Target = second
	req.CandidateID = fresh.ID
	req.Revision = 1
	if err := db.saveCandidate(ctx, req); !errors.Is(err, errCandidateConflict) {
		t.Fatalf("other head's evidence: %v", err)
	}
	req.Target = first
	req.CandidateID = old.ID
	req.Revision = 2
	if err := db.saveCandidate(ctx, req); !errors.Is(err, errCandidateConflict) {
		t.Fatalf("stale review write: %v", err)
	}
	third := second
	third.HeadSHA = strings.Repeat("d", 40)
	finding.Title = "The default config fails to load"
	thirdView := reviewedCandidates(t, db, third, []Finding{finding})
	manual := thirdView.Candidates[0]
	if manual.TrackID == old.TrackID {
		t.Fatal("ambiguous title auto-linked")
	}
	req = candidateDecision{Account: "owner", Target: third, CandidateID: manual.ID, PreviousID: fresh.ID, Revision: 1, Status: "false_positive", Note: "Checked the default config.", EvidenceIDs: []string{}}
	if err := db.saveCandidate(ctx, req); err != nil {
		t.Fatal(err)
	}
	manuallyLinked := candidateTestView(t, db, third).Candidates[0]
	if manuallyLinked.TrackID != old.TrackID || manuallyLinked.Status != "false_positive" || len(manuallyLinked.History) != 2 {
		t.Fatalf("manual tracking: %+v", manuallyLinked)
	}
	if old := candidateTestView(t, db, first).Candidates[0]; old.Status != "confirmed" {
		t.Fatal("mutated older verdict")
	}
}
func TestCandidatesDoNotGuessAmongDuplicateFindings(t *testing.T) {
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	target := workerTestTarget("owner/alpha", 8, sourceRepository)
	first := Finding{Path: "a.go", Title: "Missing initialization", Line: 80}
	second := first
	second.Line = 100
	a := reviewedCandidates(t, db, target, []Finding{first, second})
	if a.Candidates[0].TrackID == a.Candidates[1].TrackID {
		t.Fatal("linked two separate findings")
	}
	target.HeadSHA = strings.Repeat("c", 40)
	b := reviewedCandidates(t, db, target, []Finding{first})
	if len(b.PreviousCandidates) != 2 || len(b.Candidates[0].History) != 0 {
		t.Fatal("ambiguity linked silently")
	}
}

func TestCandidateLinksSnapshotOneVersionOfCIAndSeparateVMObservations(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	target := workerTestTarget("owner/alpha", 9, sourceRepository)
	f := Finding{Title: "Retrying discards setup", Path: "retry.go", Line: 20}
	j := reviewedCandidates(t, db, target, []Finding{f})
	merge := strings.Repeat("c", 40)
	checks := Checks{Status: "passed", Runs: []CheckRun{{Name: "linux", Status: "completed", Conclusion: "success", ExecutionSHA: merge, URL: "https://github.com/owner/alpha/actions/runs/1/job/1", Scope: "Run tests"}}}
	tests := []VMTestAttempt{{Attempt: 2, Status: "passed", TaskID: "task-1", AttemptID: "attempt-2", CheckoutSHA: target.HeadSHA, Command: []string{"go", "test", "./pkg"}}}
	encodedChecks, _ := json.Marshal(checks)
	encodedTests, _ := json.Marshal(tests)
	if _, err := db.db.ExecContext(ctx, `UPDATE review_jobs SET checks=?,tests=? WHERE number=9`, string(encodedChecks), string(encodedTests)); err != nil {
		t.Fatal(err)
	}
	j = candidateTestView(t, db, target)
	if len(j.EvidenceOptions) != 2 || j.EvidenceOptions[0].Kind != "check" || j.EvidenceOptions[1].Kind != "test" ||
		j.EvidenceOptions[0].SHA != merge || j.EvidenceOptions[1].SHA != target.HeadSHA {
		t.Fatal(j.EvidenceOptions)
	}
	ids := []string{j.EvidenceOptions[0].ID, j.EvidenceOptions[1].ID}
	c := j.Candidates[0]
	r := candidateDecision{Account: "owner", Target: target, CandidateID: c.ID, Revision: c.Revision, Status: "pending", EvidenceIDs: ids}
	if err := db.saveCandidate(ctx, r); err != nil {
		t.Fatal(err)
	}
	checks.Runs[0].ExecutionSHA = target.HeadSHA
	checks.Runs[0].Scope = "Replacement run"
	encodedChecks, _ = json.Marshal(checks)
	if _, err := db.db.ExecContext(ctx, `UPDATE review_jobs SET checks=? WHERE number=9`, string(encodedChecks)); err != nil {
		t.Fatal(err)
	}
	j = candidateTestView(t, db, target)
	if len(j.Candidates[0].Evidence) != 2 || j.Candidates[0].Evidence[0].SHA != merge || j.Candidates[0].Evidence[0].Detail != "Run tests" || j.Candidates[0].Evidence[0].ID == j.EvidenceOptions[0].ID {
		t.Fatal("lost original CI observation")
	}
	r.Revision = 2
	r.Note = "Rechecking the original results."
	if err := db.saveCandidate(ctx, r); err != nil {
		t.Fatalf("cannot preserve frozen CI evidence after rerun: %v", err)
	}
}

func TestSameHeadCanBeReviewedAgainstAnotherBase(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "review.sqlite")
	db := testReviewStore(t, filename, true)
	target := workerTestTarget("owner/alpha", 30, sourceRepository)
	f := Finding{Title: "Invalid defaults prevent startup", Path: "app/config.go", Severity: "medium", Body: "The old config has no defaults.", Line: 80, Side: "head"}
	c := reviewedCandidates(t, db, target, []Finding{f}).Candidates[0]
	req := candidateDecision{Target: target, Account: "owner", CandidateID: c.ID, Revision: c.Revision, Status: "confirmed", Note: "Old configuration verified", EvidenceIDs: []string{}}
	if err := db.saveCandidate(ctx, req); err != nil {
		t.Fatal(err)
	}
	target.BaseSHA = strings.Repeat("f", 40)
	// Simulate the version change after a re-analysis request is accepted.
	if _, err := db.db.ExecContext(ctx, `UPDATE review_jobs SET base_sha=?,policy=NULL,evidence_key=NULL,findings='[]',status='queued' WHERE number=30`, target.BaseSHA); err != nil {
		t.Fatal(err)
	}
	after := reviewedCandidates(t, db, target, []Finding{f}).Candidates[0]
	if after.ID == c.ID || after.TrackID != c.TrackID || after.Status != "pending" || after.Note != "" || len(after.Evidence) != 0 || len(after.History) != 1 || after.History[0].BaseSHA != c.BaseSHA || after.History[0].Note != "Old configuration verified" {
		t.Fatalf("old base leaked into new decision: %+v", after)
	}
	req.Revision = 2
	if err := db.saveCandidate(ctx, req); !errors.Is(err, errCandidateConflict) {
		t.Fatal("superseded write accepted", err)
	}
}

func TestMissingFindingCanBeFollowedAndResolvedWithoutManufacturingModelOutput(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	first := workerTestTarget("owner/alpha", 19, sourceRepository)
	f := Finding{Path: "app/settings.go", Line: 80, Title: "Old settings prevent startup", Body: "Required keys are absent"}
	original := reviewedCandidates(t, db, first, []Finding{f}).Candidates[0]
	originalDecision := candidateDecision{Account: "owner", Target: first, CandidateID: original.ID, Revision: 1, Status: "confirmed", EvidenceIDs: []string{}, Note: "Failed during startup."}
	if err := db.saveCandidate(ctx, originalDecision); err != nil {
		t.Fatal(err)
	}
	next := first
	next.HeadSHA = strings.Repeat("d", 40)
	review := reviewedCandidates(t, db, next, nil)
	if _, err := db.db.ExecContext(ctx, "UPDATE review_jobs SET status='clean' WHERE account='owner' AND number=? AND head_sha=?", next.Number, next.HeadSHA); err != nil {
		t.Fatal(err)
	}
	if len(review.Findings) != 0 || len(review.PreviousCandidates) != 1 || review.PreviousCandidates[0].Note != "Failed during startup." || len(review.Followups) != 0 {
		t.Fatalf("lost prior concern: %+v", review)
	}
	r := candidateDecision{Account: "owner", Target: next, PreviousID: original.ID}
	if err := db.followCandidate(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := db.followCandidate(ctx, r); err != nil {
		t.Fatal(err)
	}
	view := candidateTestView(t, db, next)
	if view.Status != "clean" || len(view.Findings) != 0 || len(view.Candidates) != 0 || len(view.Followups) != 1 {
		t.Fatal("a manual follow-up became a model finding")
	}
	manual := view.Followups[0]
	if manual.Status != "pending" || manual.OriginID != original.ID || manual.OriginHeadSHA != first.HeadSHA || manual.Line != 80 || manual.TrackID != original.TrackID || len(manual.History) != 1 {
		t.Fatalf("wrong follow-up %+v", manual)
	}
	checks := Checks{Status: "passed", Runs: []CheckRun{{Name: "Regression", Status: "completed", Conclusion: "success", ExecutionSHA: next.HeadSHA}}}
	data, _ := json.Marshal(checks)
	if _, err := db.db.ExecContext(ctx, "UPDATE review_jobs SET checks=? WHERE number=? AND head_sha=?", string(data), next.Number, next.HeadSHA); err != nil {
		t.Fatal(err)
	}
	view = candidateTestView(t, db, next)
	r.PreviousID = ""
	r.CandidateID = manual.ID
	r.Revision = 1
	r.Status = "resolved"
	r.Note = "Current revision now renders the fixture."
	r.EvidenceIDs = []string{view.EvidenceOptions[0].ID}
	if err := db.saveCandidate(ctx, r); err != nil {
		t.Fatal(err)
	}
	view = candidateTestView(t, db, next)
	if view.Followups[0].Status != "resolved" || len(view.Followups[0].Evidence) != 1 || view.Followups[0].History[0].Status != "confirmed" || len(view.Findings) > 0 {
		t.Fatal("incorrect follow-up after resolution")
	}
	r.Status = "pending"
	r.Revision = 2
	if err := db.saveCandidate(ctx, r); err != nil {
		t.Fatal(err)
	}
	if candidateTestView(t, db, next).Followups[0].Status != "pending" {
		t.Fatal("cannot reopen the latest follow-up")
	}
}
