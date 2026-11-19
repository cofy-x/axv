package githubreview

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTargetMovesAndRecoversVerifiedComparison(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	target := workerTestTarget("owner/alpha", 174, sourceRepository)
	cfg := workerSettings(t, db).Config
	cfg.PublishRepositories = []string{}
	saved := configureWorker(t, db, cfg)
	g := &workerGitHub{targets: []Target{target}}
	s := newService(ctx, func() {}, db, g, connectedCLI{}, &countedReviewer{})
	now := time.Now()
	s.scan(ctx, saved, now)
	if err := s.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	original := workerJob(t, db, target, "analyzed")
	next := target
	next.BaseSHA = strings.Repeat("c", 40)
	g.targets = []Target{next}
	// A temporary failure cannot silently authorize a new comparison.
	g.merge = func(t Target) (string, error) {
		if t.BaseSHA == next.BaseSHA {
			return "", errors.New("temporarily unavailable")
		}
		return target.BaseSHA, nil
	}
	s.scan(ctx, saved, now)
	expired := workerJob(t, db, target, "stale")
	if expired.ObservedBaseSHA != next.BaseSHA || expired.BaseSHA != target.BaseSHA || len(expired.Findings) != len(original.Findings) {
		t.Fatalf("lost comparison identity %+v", expired)
	}
	g.merge = nil
	s.scan(ctx, saved, now)
	recovered := workerJob(t, db, target, "analyzed")
	if recovered.ObservedBaseSHA != "" || recovered.BaseSHA != target.BaseSHA || !matchesJob(saved.Config, recovered, next) {
		t.Fatalf("did not restore equivalent comparison %+v", recovered)
	}
	next.BaseSHA = strings.Repeat("d", 40)
	g.targets = []Target{next}
	g.merge = func(t Target) (string, error) { return t.BaseSHA, nil }
	s.scan(ctx, saved, now)
	expired = workerJob(t, db, target, "stale")
	if expired.MergeBaseSHA != target.BaseSHA || expired.ObservedBaseSHA != next.BaseSHA || matchesJob(saved.Config, expired, next) {
		t.Fatalf("changed merge base stayed current %+v", expired)
	}
	g.targets = []Target{target}
	s.scan(ctx, saved, now)
	workerJob(t, db, target, "analyzed")
}

func TestChecksFailurePreservesPreviousObservation(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	target := workerTestTarget("owner/alpha", 174, sourceRepository)
	g := &workerGitHub{targets: []Target{target}}
	s := newService(ctx, func() {}, db, g, connectedCLI{}, &countedReviewer{})
	cfg := workerSettings(t, db).Config
	cfg.PublishRepositories = []string{}
	saved := configureWorker(t, db, cfg)
	now := time.Now()
	s.scan(ctx, saved, now)
	if err := s.processNext(ctx, saved, now); err != nil {
		t.Fatal(err)
	}
	refresh := func() Checks {
		t.Helper()
		req := RetryRequest{Repository: target.Repository, Number: target.Number, HeadSHA: target.HeadSHA, BaseSHA: target.BaseSHA, Revision: saved.Revision}
		if err := s.refreshChecks(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := s.advanceEvidence(ctx, "checks", now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return workerJob(t, db, target, "analyzed").Evidence.Checks
	}
	g.checks = func(Checks) (Checks, error) { return Checks{}, errors.New("temporarily unavailable") }
	initial := refresh()
	if initial.Status != "error" || initial.UpdatedAt != nil || initial.RefreshErrorAt == nil {
		t.Fatalf("invented evidence: %+v", initial)
	}
	g.checks = func(Checks) (Checks, error) {
		return Checks{Status: "passed", Runs: []CheckRun{{Name: "test", Status: "completed", Conclusion: "success", ExecutionSHA: target.HeadSHA}}}, nil
	}
	previous := refresh()
	g.checks = func(Checks) (Checks, error) { return Checks{}, errors.New("temporarily unavailable") }
	failed := refresh()
	if failed.Status != previous.Status || previous.UpdatedAt == nil || !failed.UpdatedAt.Equal(*previous.UpdatedAt) || failed.RefreshErrorAt == nil || failed.Error == "" || len(failed.Runs) != 1 || failed.Runs[0] != previous.Runs[0] {
		t.Fatalf("last successful reading was overwritten: %+v", failed)
	}
	g.checks = nil
	recovered := refresh()
	if recovered.Status != "empty" || recovered.Error != "" || recovered.RefreshErrorAt != nil {
		t.Fatalf("refresh did not recover: %+v", recovered)
	}
}
