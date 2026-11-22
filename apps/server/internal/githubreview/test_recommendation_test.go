package githubreview

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/gateway"
)

func TestQuickTestReuseRequiresProvenanceAndKeepsLaterDiagnostic(t *testing.T) {
	vm := ComputeVMTests{HostID: "host", MachineID: "vm", ProjectID: "project"}
	binding := compute.ProjectBinding{HostID: vm.HostID, MachineID: vm.MachineID, ProjectID: vm.ProjectID,
		ProjectRevision: 1, VolumeID: "volume", VolumeRevision: 1, BootID: "boot", InstanceID: "guest"}
	target := workerTestTarget("owner/alpha", 7, sourceRepository)
	smoke := exampleProfile("smoke", "checks/smoke.py")
	diagnostic := exampleProfile("specific", "checks/specific.py")
	policy := RepositoryPolicy{TestProfiles: []TestProfile{smoke, diagnostic}}
	saved := savedSettings{Config: Config{Revision: 1, Repositories: []string{target.Repository}, RepositoryPolicies: map[string]RepositoryPolicy{target.Repository: policy}}, account: "owner"}
	zero, one, wrapperFailure := 0, 1, 10
	first := VMTestAttempt{Attempt: 1, Status: "passed", Selection: "automatic", SelectionBaseSHA: target.BaseSHA,
		TestID: smoke.ID, TestName: smoke.Name, EntryPath: smoke.EntryPath, Command: smoke.Command,
		PlanHash: reviewDigest(policy.TestProfiles), CheckoutSHA: target.HeadSHA, TaskID: "task1", AttemptID: "try1",
		VMID: vm.MachineID, ProjectID: vm.ProjectID, Binding: &binding, ExitCode: &zero, AssertionExitCode: &zero}
	second := VMTestAttempt{Attempt: 2, Status: "failed", Selection: "manual", SelectionBaseSHA: target.BaseSHA,
		TestID: diagnostic.ID, TestName: diagnostic.Name, EntryPath: diagnostic.EntryPath, Command: diagnostic.Command,
		PlanHash: reviewDigest(diagnostic), TaskID: "task2", AttemptID: "try2"}
	evidence := &Evidence{Tests: []VMTestAttempt{first, second}, Checks: Checks{Status: "passed"}}
	j := Job{Target: target, Status: "analyzed", account: saved.account, evidenceKey: "original-review", Evidence: evidence}
	got := recommendQuickTest(j, saved, vm)
	if got == nil || got.Status != "reuse" || got.ReusableStatus != "passed" || got.ReusableAttempt != 1 || got.LatestAttempt != 2 || got.TestID != smoke.ID || evidence.Tests[1].Status != "failed" {
		t.Fatalf("did not reuse original pass independently of later diagnostic: %+v", got)
	}
	negative := first
	negative.Status, negative.ExitCode, negative.AssertionExitCode = "failed", &wrapperFailure, &one
	evidence.Tests[0] = negative
	if got = recommendQuickTest(j, saved, vm); got == nil || got.Status != "reuse" || got.ReusableStatus != "failed" {
		t.Fatalf("did not preserve a verified negative execution: %+v", got)
	}
	for _, check := range []struct {
		name   string
		change func(*VMTestAttempt)
	}{
		{"unverified checkout", func(a *VMTestAttempt) { a.CheckoutSHA = "" }},
		{"wrong selected base", func(a *VMTestAttempt) { a.SelectionBaseSHA = strings.Repeat("c", 40) }},
		{"wrong VM", func(a *VMTestAttempt) { a.Binding = &compute.ProjectBinding{HostID: "other"} }},
		{"different command", func(a *VMTestAttempt) { a.Command = []string{"another-test"} }},
		{"missing assertion exit", func(a *VMTestAttempt) { a.AssertionExitCode = nil }},
		{"truncated negative", func(a *VMTestAttempt) { a.Truncated = true }},
	} {
		t.Run(check.name, func(t *testing.T) {
			evidence.Tests[0] = negative
			check.change(&evidence.Tests[0])
			if got := recommendQuickTest(j, saved, vm); got == nil || got.Status != "run" {
				t.Fatalf("incorrectly treated incomplete receipt as reusable: %+v", got)
			}
		})
	}
	evidence.Tests[0] = first
	j.equivalentBaseSHA = strings.Repeat("e", 40)
	if got = recommendQuickTest(j, saved, vm); got != nil {
		t.Fatalf("recommended an earlier target for an equivalent but changed base: %+v", got)
	}
}

func TestQuickTestRecommendationUsesCandidateLinkedAttempt(t *testing.T) {
	target := workerTestTarget("owner/alpha", 8, sourceRepository)
	profiles := []TestProfile{exampleProfile("first", "checks/first.py"), exampleProfile("second", "checks/second.py")}
	policy := RepositoryPolicy{TestProfiles: profiles}
	saved := savedSettings{Config: Config{Revision: 1, Repositories: []string{target.Repository}, RepositoryPolicies: map[string]RepositoryPolicy{target.Repository: policy}}, account: "owner"}
	j := Job{Target: target, Status: "analyzed", account: saved.account, evidenceKey: "exact-review", Evidence: &Evidence{Checks: Checks{Status: "passed"}}}
	for i, profile := range profiles {
		a, err := newTestAttempt(policy, profile.ID, target.BaseSHA, i+1, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		a.Status, a.TaskID, a.AttemptID = "failed", "task-"+profile.ID, "receipt-"+profile.ID
		j.Evidence.Tests = append(j.Evidence.Tests, a)
		// The older evidence item exposes a target SHA. This alone cannot qualify it for reuse.
		candidate := Candidate{CandidateOccurrence: CandidateOccurrence{Status: "confirmed", Evidence: []EvidenceItem{quickTestEvidence(j, a, j.HeadSHA)}}}
		if i == 0 {
			candidate.Comparisons = []CandidateComparison{{BaseSHA: target.BaseSHA, HeadSHA: target.HeadSHA, Status: "completed", Outcome: "head_only_failed"}}
		}
		j.Candidates = append(j.Candidates, candidate)
	}
	vm := ComputeVMTests{HostID: "host", MachineID: "vm", ProjectID: "project"}
	got := recommendQuickTest(j, saved, vm)
	if got == nil || got.TestID != profiles[0].ID || got.Status != "run" || got.LatestAttempt != 2 || !strings.Contains(got.Reason, "attempt 1") {
		t.Fatalf("did not recommend the linked older profile: %+v", got)
	}
}

func TestConfirmQuickTestQueuesOneWorkItemForFreshRecommendation(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	target := workerTestTarget("owner/alpha", 9, sourceRepository)
	j := reviewedCandidates(t, db, target, []Finding{})
	profile := exampleProfile("smoke", "checks/smoke.py")
	config := workerSettings(t, db).Config
	config.Enabled = false
	config.RepositoryPolicies = map[string]RepositoryPolicy{target.Repository: {TestProfiles: []TestProfile{profile}}}
	saved := configureWorker(t, db, config)
	vm := ComputeVMTests{Gateway: &gateway.Gateway{}, HostID: "host", MachineID: "vm", ProjectID: "project"}
	s := newService(ctx, func() {}, db, &workerGitHub{}, connectedCLI{}, &countedReviewer{})
	s.vm = vm
	rec := recommendQuickTest(j, saved, vm)
	if rec == nil || rec.Status != "run" || rec.TestID != profile.ID || rec.LatestAttempt != 0 {
		t.Fatalf("missing precise unscoped recommendation: %+v", rec)
	}
	req := RetryRequest{Repository: target.Repository, Number: target.Number, BaseSHA: target.BaseSHA, HeadSHA: target.HeadSHA, Revision: saved.Revision}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { results <- s.confirmQuickTest(ctx, req, rec.ID) })
	}
	wg.Wait()
	close(results)
	var accepted, stale int
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrRetryStale):
			stale++
		default:
			t.Fatal(err)
		}
	}
	next := candidateTestView(t, db, target)
	if accepted != 1 || stale != 1 || len(next.Evidence.Tests) != 1 || next.Evidence.Tests[0].Status != "queued" || next.Evidence.Tests[0].TestID != profile.ID || !next.Evidence.Tests[0].ExactComparison {
		t.Fatalf("confirmation did not queue exactly one pinned test: accepted=%d stale=%d tests=%+v", accepted, stale, next.Evidence.Tests)
	}
}
