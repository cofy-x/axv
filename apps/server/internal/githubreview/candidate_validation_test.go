package githubreview

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type scenarioTestVM struct {
	binding      compute.ProjectBinding
	bindingsRead int
	launches     []string
	commands     [][]string
	failureHead  string
}

func (v *scenarioTestVM) currentReviewBinding(context.Context) (compute.ProjectBinding, error) {
	v.bindingsRead++
	return v.binding, nil
}

func (v *scenarioTestVM) Poll(_ context.Context, target Target, command []string, key string, a VMTestAttempt) (VMTestAttempt, error) {
	if a.ObserveOnly && a.TaskID == "" {
		a.Status = "canceled"
		return a, nil
	}
	if a.Binding == nil && !a.ObserveOnly {
		binding := v.binding
		a.Binding, a.VMID, a.ProjectID = &binding, binding.MachineID, binding.ProjectID
		a.Status = "running"
		return a, nil
	}
	if a.Binding == nil || *a.Binding != v.binding || a.ObserveOnly {
		return a, errors.New("test did not receive the established VM binding")
	}
	v.launches = append(v.launches, target.HeadSHA)
	v.commands = append(v.commands, slices.Clone(command))
	a.TaskID, a.AttemptID, a.CheckoutSHA = "task-"+key, "attempt-"+key, target.HeadSHA
	a.Status, a.ExitCode, a.AssertionExitCode = "passed", new(0), new(0)
	if target.HeadSHA == v.failureHead {
		a.Status, a.ExitCode, a.AssertionExitCode = "failed", new(10), new(1)
	}
	return a, nil
}

func TestApprovedCandidateValidationHonorsAmbiguityAndRevocation(t *testing.T) {
	s, vm, old, finding := preparedScenarioTest(t)
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	next := old
	next.HeadSHA = strings.Repeat("e", 40)
	duplicate := finding
	duplicate.Line++
	duplicate.Side = "head"
	reviewScenarioHead(t, s.store, next, []Finding{finding, duplicate})
	if err := s.advanceCandidateValidation(t.Context()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ambiguous exact matches produced work: %v", err)
	}
	view, err := s.snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range view.Jobs {
		if j.HeadSHA == next.HeadSHA {
			for _, c := range j.Candidates {
				if c.AutoValidation == nil || c.AutoValidation.State != "needs_review" || !strings.Contains(c.AutoValidation.Reason, "More than one candidate") {
					t.Fatalf("ambiguous match was not actionable: %+v", c.AutoValidation)
				}
			}
		}
	}
	next.HeadSHA = strings.Repeat("f", 40)
	reviewScenarioHead(t, s.store, next, []Finding{finding})
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved := workerSettings(t, s.store).Config
	policy := saved.policy(next.Repository)
	policy.AutoCandidateValidation = false
	saved.RepositoryPolicies[next.Repository] = policy
	configureWorker(t, s.store, saved)
	completeScenarioTestComparison(t, s)
	if len(vm.launches) != 0 {
		t.Fatalf("disabled automation launched a new VM task: %v", vm.launches)
	}
	view, err = s.snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range view.Jobs {
		if j.HeadSHA == next.HeadSHA {
			c := j.Candidates[0]
			if c.AutoValidation == nil || c.AutoValidation.State != "needs_review" || !strings.Contains(c.AutoValidation.Reason, "disabled") || c.CommentDraft != nil || len(c.Comparisons) != 1 || c.Comparisons[0].Status != "canceled" {
				t.Fatalf("the canceled unit did not retain a useful outcome: %+v", c)
			}
			return
		}
	}
	t.Fatal("missing the review with revoked automation")
}

func preparedScenarioTest(t *testing.T) (*Service, *scenarioTestVM, Target, Finding) {
	t.Helper()
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	target := workerTestTarget("owner/alpha", 7, sourceRepository)
	finding := Finding{Path: "config/settings.go", Line: 23, Title: "Configuration default changes the output", Severity: "medium"}
	v := &scenarioTestVM{binding: compute.ProjectBinding{
		HostID: "host", MachineID: "machine", ProjectID: "project", ProjectRevision: 1,
		VolumeID: "volume", VolumeRevision: 1, BootID: "boot", InstanceID: "guest",
	}, failureHead: target.HeadSHA}
	s := newService(ctx, func() {}, db, &workerGitHub{}, connectedCLI{}, &countedReviewer{})
	s.vm = v
	j := reviewedCandidates(t, db, target, []Finding{finding})
	assignScenarioMergeBase(t, db, target)
	c := j.Candidates[0]
	if err := db.saveCandidate(ctx, candidateDecision{Account: "owner", Target: target, CandidateID: c.ID, Revision: c.Revision, Note: "Original assessment", Status: "confirmed", EvidenceIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if err := db.saveCandidateCommentDraft(ctx, candidateCommentDraftRequest{Account: "owner", Target: target, CandidateID: c.ID, Body: "My original local draft"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	settings := workerSettings(t, db).Config
	settings.Enabled = false
	settings.PublishRepositories = []string{}
	saved := configureWorker(t, db, settings)
	req := RetryRequest{Repository: target.Repository, Number: target.Number, BaseSHA: target.BaseSHA, HeadSHA: target.HeadSHA, Revision: saved.Revision}
	compare := comparisonRequest{RetryRequest: req, Account: "owner", CandidateID: c.ID, RequestID: "abcde123-1111-2222-3333-abcd1234ef00", RuntimeID: "shell", Assertion: "printf '%s\\n' approved"}
	if err := s.requestComparison(ctx, compare); err != nil {
		t.Fatal(err)
	}
	// Let the production comparison worker persist the earlier manual receipt.
	completeScenarioTestComparison(t, s)
	v.launches, v.commands = nil, nil
	if err := s.approveComparisonScenario(ctx, approveScenarioRequest{RetryRequest: req, Account: "owner", CandidateID: c.ID, ComparisonID: compare.RequestID, Name: strings.Repeat("A", 120), Enable: true}); err != nil {
		t.Fatal(err)
	}
	return s, v, target, finding
}

func assignScenarioMergeBase(t *testing.T, db *store, target Target) {
	t.Helper()
	if _, err := db.db.ExecContext(t.Context(), `UPDATE review_jobs SET merge_base_sha=? WHERE repository=? AND number=? AND head_sha=?`, target.BaseSHA, target.Repository, target.Number, target.HeadSHA); err != nil {
		t.Fatal(err)
	}
}

func reviewScenarioHead(t *testing.T, db *store, target Target, findings []Finding) {
	t.Helper()
	settings := workerSettings(t, db).Config
	settings.Enabled = true
	configureWorker(t, db, settings)
	reviewedCandidates(t, db, target, findings)
	assignScenarioMergeBase(t, db, target)
	settings.Enabled = false
	configureWorker(t, db, settings)
}

func completeScenarioTestComparison(t *testing.T, s *Service) {
	t.Helper()
	for range 5 {
		err := s.advanceComparison(t.Context())
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("the comparison did not finish")
}

func TestApprovedCandidateValidationReusesCurrentReceiptWithoutNewVMTask(t *testing.T) {
	s, vm, target, _ := preparedScenarioTest(t)
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := candidateTestView(t, s.store, target).Candidates[0]
	v := first.AutoValidation
	if len(vm.launches) != 0 || vm.bindingsRead != 1 || len(first.Comparisons) != 1 || v == nil || v.State != "verified" || !v.Reused || v.ComparisonID != first.Comparisons[0].ID || v.Suggestion == nil || v.Suggestion.Status != "confirmed" {
		t.Fatalf("reuse did not preserve the original receipt: %+v launches=%v binding checks=%d", v, vm.launches, vm.bindingsRead)
	}
	if first.Note != "Original assessment" || first.CommentDraft == nil || first.CommentDraft.Body != "My original local draft" || first.CommentDraft.Revision != 1 {
		t.Fatalf("modified an existing judgement or draft: %+v", first)
	}
	if err := s.advanceCandidateValidation(t.Context()); !errors.Is(err, sql.ErrNoRows) || vm.bindingsRead != 1 {
		t.Fatalf("verified candidate was scheduled again: %v", err)
	}
	view, err := s.snapshot(t.Context())
	if err != nil || len(view.Config.policy(target.Repository).ComparisonScenarios) != 1 || view.Config.Enabled {
		t.Fatalf("missing opted-in scenario in the paused local profile: %+v, %v", view.Config, err)
	}
}

func TestApprovedCandidateValidationRunsPinnedPairAndExpiresPriorEvidence(t *testing.T) {
	s, vm, old, finding := preparedScenarioTest(t)
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	next := old
	next.HeadSHA = strings.Repeat("c", 40)
	vm.failureHead = next.HeadSHA
	vm.binding.BootID, vm.binding.InstanceID = "new-boot", "new-guest"
	finding.Line++
	unmatched := finding
	unmatched.Title = "Another concern without an approved scenario"
	reviewScenarioHead(t, s.store, next, []Finding{finding, unmatched})
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	queued := candidateTestView(t, s.store, next).Candidates[0]
	if queued.AutoValidation == nil || queued.AutoValidation.State != "queued" || queued.CommentDraft != nil || len(vm.launches) != 0 {
		t.Fatalf("produced evidence before test execution: %+v", queued)
	}
	completeScenarioTestComparison(t, s)
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	j := candidateTestView(t, s.store, next)
	c := j.Candidates[0]
	if c.Status != "pending" || c.AutoValidation == nil || c.AutoValidation.State != "verified" || c.AutoValidation.Reused || c.CommentDraft == nil || c.CommentDraft.HeadSHA != next.HeadSHA ||
		!strings.Contains(c.CommentDraft.Body, next.HeadSHA) || !slices.Equal(vm.launches, []string{next.BaseSHA, next.HeadSHA}) {
		t.Fatalf("did not suggest based on both original checkouts without changing human verdict: candidate=%+v launches=%v", c, vm.launches)
	}
	if len(c.Comparisons) != 1 || c.Comparisons[0].ScenarioID == "" || *c.Comparisons[0].Baseline.Binding != *c.Comparisons[0].Head.Binding || !slices.Equal(vm.commands[0], vm.commands[1]) {
		t.Fatalf("both anchored tests did not use the same approved command and binding: %+v", c.Comparisons)
	}
	original := candidateTestView(t, s.store, old).Candidates[0]
	if original.AutoValidation == nil || original.AutoValidation.State != "stale" || original.AutoValidation.Suggestion != nil || original.CommentDraft == nil || !original.CommentDraft.Stale {
		t.Fatalf("superseded receipt or draft remained live: %+v", original)
	}
	view, err := s.snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range view.Jobs {
		if job.HeadSHA == next.HeadSHA {
			manual := job.Candidates[1]
			if manual.AutoValidation == nil || manual.AutoValidation.State != "needs_review" || !strings.Contains(manual.AutoValidation.Reason, "No approved scenario") || manual.CommentDraft != nil {
				t.Fatalf("unmatched candidate did not explain the manual decision: %+v", manual)
			}
			return
		}
	}
	t.Fatal("current job was absent from the snapshot")
}

func TestApprovedCandidateValidationKeepsNonDifferentialResultsForHumanReview(t *testing.T) {
	s, vm, old, finding := preparedScenarioTest(t)
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	next := old
	next.HeadSHA = strings.Repeat("d", 40)
	reviewScenarioHead(t, s.store, next, []Finding{finding})
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	completeScenarioTestComparison(t, s)
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := candidateTestView(t, s.store, next).Candidates[0]
	if c.AutoValidation == nil || c.AutoValidation.State != "needs_review" || c.AutoValidation.Suggestion != nil || c.CommentDraft != nil || !strings.Contains(c.AutoValidation.Reason, "passed on both") || len(vm.launches) != 2 {
		t.Fatalf("non-differential result received an automatic decision: %+v", c)
	}
	if err := s.advanceCandidateValidation(t.Context()); !errors.Is(err, sql.ErrNoRows) || len(vm.launches) != 2 {
		t.Fatalf("non-differential receipt was automatically retried: %v", err)
	}
}

func TestApprovedScenarioReferenceRunsChangedTitleAsEvidenceOnly(t *testing.T) {
	s, vm, original, finding := preparedScenarioTest(t)
	approved, err := approvedComparisonScenarios(t.Context(), s.store.db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	scenario := approved[original.Repository][0]
	next := original
	next.HeadSHA = strings.Repeat("9", 40)
	vm.failureHead = next.HeadSHA
	finding.Title = "Default output changed with this configuration"
	finding.VerificationScenarioID = scenario.ID
	reviewScenarioHead(t, s.store, next, []Finding{finding})
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	completeScenarioTestComparison(t, s)
	if err := s.advanceCandidateValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := candidateTestView(t, s.store, next).Candidates[0]
	if c.VerificationScenarioID != scenario.ID || c.Status != "pending" || c.AutoValidation == nil ||
		c.AutoValidation.State != "needs_review" || c.AutoValidation.Suggestion != nil || c.CommentDraft != nil ||
		len(c.Comparisons) != 1 || c.Comparisons[0].ID != c.AutoValidation.ComparisonID || c.Comparisons[0].Outcome != "head_only_failed" ||
		!strings.Contains(c.AutoValidation.Reason, "deciding whether it proves") || !slices.Equal(vm.launches, []string{next.BaseSHA, next.HeadSHA}) {
		t.Fatalf("expected a pinned scenario receipt for human relevance assessment: candidate=%+v launches=%v", c, vm.launches)
	}
	if err := s.advanceCandidateValidation(t.Context()); !errors.Is(err, sql.ErrNoRows) || len(vm.launches) != 2 {
		t.Fatalf("scenario with a complete receipt was retried: %v", err)
	}
}

func TestApprovedScenarioReferenceRejectsUnavailableScopeAndDuplicateClaims(t *testing.T) {
	t.Run("incompatible or unrecognized reference does not fall back to exact title", func(t *testing.T) {
		s, vm, original, finding := preparedScenarioTest(t)
		approved, err := approvedComparisonScenarios(t.Context(), s.store.db, "owner")
		if err != nil {
			t.Fatal(err)
		}
		scenario := approved[original.Repository][0]
		next := original
		next.HeadSHA = strings.Repeat("8", 40)
		incompatible := finding
		incompatible.Side = "base"
		incompatible.VerificationScenarioID = scenario.ID
		finding.VerificationScenarioID = strings.Repeat("f", 64)
		reviewScenarioHead(t, s.store, next, []Finding{finding, incompatible})
		if err := s.advanceCandidateValidation(t.Context()); !errors.Is(err, sql.ErrNoRows) || len(vm.launches) != 0 {
			t.Fatalf("an unavailable scenario started work: %v", err)
		}
		snapshot, err := s.snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range snapshot.Jobs {
			if j.HeadSHA != next.HeadSHA {
				continue
			}
			for _, c := range j.Candidates {
				if c.AutoValidation == nil || c.AutoValidation.State != "needs_review" || !strings.Contains(c.AutoValidation.Reason, "unavailable") {
					t.Fatalf("candidate did not explain the invalid reference: %+v", c.AutoValidation)
				}
			}
			return
		}
		t.Fatal("missing the current revision")
	})
	t.Run("legacy title and explicit reference cannot claim the same grant", func(t *testing.T) {
		s, vm, original, finding := preparedScenarioTest(t)
		approved, err := approvedComparisonScenarios(t.Context(), s.store.db, "owner")
		if err != nil {
			t.Fatal(err)
		}
		next := original
		next.HeadSHA = strings.Repeat("7", 40)
		ref := finding
		ref.Title = "Configuration is changing the default output"
		ref.VerificationScenarioID = approved[original.Repository][0].ID
		reviewScenarioHead(t, s.store, next, []Finding{finding, ref})
		if err := s.advanceCandidateValidation(t.Context()); !errors.Is(err, sql.ErrNoRows) || len(vm.launches) != 0 {
			t.Fatalf("ambiguous scenario started work: %v", err)
		}
	})
}
