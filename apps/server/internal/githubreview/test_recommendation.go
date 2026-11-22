package githubreview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type QuickTestRecommendation struct {
	ID              string   `json:"id"`
	Status          string   `json:"status"`
	Reason          string   `json:"reason"`
	TestID          string   `json:"testId,omitempty"`
	TestName        string   `json:"testName"`
	EntryPath       string   `json:"entryPath,omitempty"`
	Command         []string `json:"command,omitempty"`
	BaseSHA         string   `json:"baseSha"`
	HeadSHA         string   `json:"headSha"`
	LatestAttempt   int      `json:"latestAttempt"`
	ReusableAttempt int      `json:"reusableAttempt,omitempty"`
	ReusableStatus  string   `json:"reusableStatus,omitempty"`
}

func lastQuickTestAttempt(tests []VMTestAttempt) (number int, idle bool) {
	for _, test := range tests {
		if test.Status == "queued" || test.Status == "running" {
			return 0, false
		}
		number = max(number, test.Attempt)
	}
	return number, true
}

func matchingQuickTest(t VMTestAttempt, policy RepositoryPolicy, baseSHA string) (TestProfile, bool) {
	if len(policy.TestProfiles) == 0 {
		profile := TestProfile{Name: "Repository quick test command", EntryPath: legacyTestEntry(policy.TestCommand), Command: policy.TestCommand}
		return profile, len(profile.Command) > 0 && t.TestID == "" && t.PlanHash == "" && t.ReceiptBaseSHA == baseSHA && t.EntryPath == profile.EntryPath && slices.Equal(t.Command, profile.Command)
	}
	if t.SelectionBaseSHA != baseSHA || !t.matchesPolicy(policy, t.Command) {
		return TestProfile{}, false
	}
	for _, profile := range policy.TestProfiles {
		if t.TestID == profile.ID && t.EntryPath == profile.EntryPath && slices.Equal(t.Command, profile.Command) {
			return profile, true
		}
	}
	return TestProfile{}, false
}

func reusableQuickTest(t VMTestAttempt, j Job, vm ComputeVMTests) bool {
	binding := t.Binding
	if t.ReceiptBaseSHA != "" && t.ReceiptBaseSHA != j.BaseSHA {
		return false
	}
	if t.CheckoutSHA != j.HeadSHA || t.TaskID == "" || t.AttemptID == "" || t.Error != "" || binding == nil ||
		binding.HostID != vm.HostID || binding.ProjectID != vm.ProjectID || binding.MachineID != vm.MachineID ||
		t.ProjectID != binding.ProjectID || t.VMID != binding.MachineID || binding.ProjectRevision < 1 ||
		binding.VolumeID == "" || binding.VolumeRevision < 1 || binding.BootID == "" || binding.InstanceID == "" ||
		!verifiedVMAssertion(t) {
		return false
	}
	return true
}

// Linked evidence expresses a reviewer's choice of what is relevant to a
// candidate. An independently compared candidate provides the stronger hint.
func quickTestEvidenceRank(j Job) map[string]int {
	ranks := map[string]int{}
	for _, candidates := range [][]Candidate{j.Candidates, j.Followups} {
		for _, candidate := range candidates {
			if candidate.Status != "pending" && candidate.Status != "confirmed" {
				continue
			}
			rank := 3
			for _, comparison := range candidate.Comparisons {
				if comparison.BaseSHA == j.BaseSHA && comparison.HeadSHA == j.HeadSHA && comparison.Status == "completed" && comparison.Outcome != "" && comparison.Outcome != "inconclusive" {
					rank = 4
					break
				}
			}
			for _, evidence := range candidate.Evidence {
				if evidence.Kind == "test" {
					ranks[evidence.ID] = max(ranks[evidence.ID], rank)
				}
			}
		}
	}
	return ranks
}

func needsQuickTestEvidence(j Job, policy RepositoryPolicy, vm ComputeVMTests) bool {
	checks := j.Evidence.Checks
	if checks.RefreshErrorAt != nil || checks.Status == "" || checks.Status == "error" || checks.Status == "failed" {
		return true
	}
	for _, candidates := range [][]Candidate{j.Candidates, j.Followups} {
		for _, candidate := range candidates {
			if n := len(candidate.Comparisons); n > 0 {
				latest := candidate.Comparisons[n-1]
				if latest.Status == "error" || latest.Status == "canceled" || latest.Outcome == "inconclusive" {
					return true
				}
			}
		}
	}
	tests := j.Evidence.Tests
	if len(tests) == 0 {
		return true
	}
	latest := tests[len(tests)-1]
	if latest.Status == "not_applicable" {
		return (latest.SelectionBaseSHA != "" && latest.SelectionBaseSHA != j.BaseSHA) || !latest.matchesPolicy(policy, latest.Command)
	}
	if latest.Status != "passed" || latest.CheckoutSHA != j.HeadSHA {
		return true
	}
	if latest.Binding != nil && (latest.Binding.HostID != vm.HostID || latest.Binding.MachineID != vm.MachineID || latest.Binding.ProjectID != vm.ProjectID) {
		return true
	}
	if len(policy.TestProfiles) == 0 {
		return latest.TestID != "" || !slices.Equal(latest.Command, policy.TestCommand)
	}
	_, matches := matchingQuickTest(latest, policy, j.BaseSHA)
	return !matches
}

func recommendQuickTest(j Job, saved savedSettings, vm ComputeVMTests) *QuickTestRecommendation {
	if j.Evidence == nil || j.evidenceKey == "" || j.account == "" || !strings.EqualFold(j.account, saved.account) || !saved.watches(j.Repository) ||
		(j.ObservedBaseSHA != "" && j.ObservedBaseSHA != j.BaseSHA) || (j.equivalentBaseSHA != "" && j.equivalentBaseSHA != j.BaseSHA) ||
		!j.Target.valid() || (j.Status != "analyzed" && j.Status != "clean" && j.Status != "published") || vm.HostID == "" || vm.MachineID == "" || vm.ProjectID == "" {
		return nil
	}
	policy := saved.policy(j.Repository)
	if len(policy.TestProfiles) == 0 && len(policy.TestCommand) == 0 {
		return nil
	}
	tests := j.Evidence.Tests
	latest, idle := lastQuickTestAttempt(tests)
	if !idle {
		return nil
	}
	rec := QuickTestRecommendation{Status: "run", BaseSHA: j.BaseSHA, HeadSHA: j.HeadSHA, LatestAttempt: latest}
	var selected TestProfile
	selectedRank := 0
	linked := quickTestEvidenceRank(j)
	unavailable := map[string]bool{}
	for i := len(tests) - 1; i >= 0; i-- {
		test := tests[i]
		profile, ok := matchingQuickTest(test, policy, j.BaseSHA)
		if !ok || unavailable[profile.ID] {
			continue
		}
		if test.Status == "not_applicable" {
			unavailable[profile.ID] = true
			continue
		}
		if test.TaskID == "" || (test.Status != "passed" && test.Status != "failed" && test.Status != "error") {
			continue
		}
		rank := linked[quickTestEvidence(j, test, test.CheckoutSHA).ID]
		if test.CheckoutSHA == "" {
			// Earlier assessment snapshots could identify an attempt by its target.
			// The link is a relevance hint only. Reuse still requires recorded checkout.
			rank = max(rank, linked[quickTestEvidence(j, test, j.HeadSHA).ID])
		}
		if test.Selection == "automatic" {
			rank = max(rank, 2)
		} else if test.Selection == "manual" {
			rank = max(rank, 1)
		}
		if rank <= selectedRank {
			continue
		}
		selectedRank, selected = rank, profile
		if rank >= 3 {
			rec.Reason = fmt.Sprintf("A candidate links to this configured test in attempt %d.", test.Attempt)
		} else if test.Selection == "automatic" {
			rec.Reason = fmt.Sprintf("Repository rules selected this test for these commits in attempt %d.", test.Attempt)
		} else {
			rec.Reason = fmt.Sprintf("This configured test was selected for these commits in attempt %d.", test.Attempt)
		}
	}
	if selectedRank == 0 {
		if len(policy.TestProfiles) == 0 {
			selected = TestProfile{Name: "Repository quick test command", EntryPath: legacyTestEntry(policy.TestCommand), Command: policy.TestCommand}
			rec.Reason = "The repository configures this command for its quick test."
		} else {
			for i := len(tests) - 1; i >= 0; i-- {
				test := tests[i]
				if test.Selection == "automatic" && test.PlanHash == reviewDigest(policy.TestProfiles) && test.SelectionBaseSHA == j.BaseSHA {
					if test.Status == "not_applicable" {
						return nil
					}
					break
				}
			}
			if len(policy.TestProfiles) == 1 && len(policy.TestProfiles[0].AppliesTo) == 0 {
				selected = policy.TestProfiles[0]
				rec.Reason = "This is the single configured test. AXV will check its required file at the PR head."
			} else {
				selected.Name = "Automatic repository test"
				rec.Reason = "The saved repository rules will check changed paths and required files before starting a test."
			}
		}
	}
	if selected.ID != "" || len(policy.TestProfiles) == 0 {
		for i := len(tests) - 1; i >= 0; i-- {
			test := tests[i]
			profile, matches := matchingQuickTest(test, policy, j.BaseSHA)
			if !matches || profile.ID != selected.ID {
				continue
			}
			if test.Status == "not_applicable" {
				return nil
			}
			if reusableQuickTest(test, j, vm) {
				rec.Status, rec.ReusableAttempt, rec.ReusableStatus = "reuse", test.Attempt, test.Status
				rec.Reason += fmt.Sprintf(" Attempt %d already verified the same commits and saved test in the configured VM scope.", test.Attempt)
			} else {
				rec.Reason += " There is no reusable execution receipt for this test and these commits."
			}
			break
		}
	}
	if rec.Status == "run" && !needsQuickTestEvidence(j, policy, vm) {
		return nil
	}
	rec.TestID, rec.TestName, rec.EntryPath, rec.Command = selected.ID, selected.Name, selected.EntryPath, selected.Command
	rec.ID = reviewDigest(strings.ToLower(j.account), strings.ToLower(j.Repository), j.Number, j.BaseSHA, j.HeadSHA, j.evidenceKey, saved.Revision, vm.HostID, vm.MachineID, vm.ProjectID, rec)
	return &rec
}

func (s *Service) confirmQuickTest(ctx context.Context, req RetryRequest, recommendationID string) error {
	if !req.valid() || !candidateIDPattern.MatchString(recommendationID) {
		return ErrInvalid
	}
	vm, ok := s.vmForRepository(req.Repository).(ComputeVMTests)
	if !ok || !vm.comparisonReady() {
		return ErrRetryStale
	}
	load := func(tx *sql.Tx) (Job, savedSettings, *QuickTestRecommendation, error) {
		saved, err := readSettings(ctx, tx)
		if err != nil {
			return Job{}, saved, nil, errStore
		}
		if saved.Revision != req.Revision {
			return Job{}, saved, nil, ErrConflict
		}
		job, err := readJob(ctx, tx, saved.account, req.Repository, req.Number, req.HeadSHA)
		if errors.Is(err, sql.ErrNoRows) || err == nil && job.BaseSHA != req.BaseSHA {
			return Job{}, saved, nil, ErrRetryStale
		}
		if err != nil || loadCandidateView(ctx, tx, &job) != nil {
			return Job{}, saved, nil, errStore
		}
		rec := recommendQuickTest(job, saved, vm)
		if rec == nil || rec.ID != recommendationID || rec.Status != "run" {
			return Job{}, saved, nil, ErrRetryStale
		}
		return job, saved, rec, nil
	}
	before, err := s.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return errStore
	}
	job, saved, _, err := load(before)
	if closeErr := before.Rollback(); closeErr != nil && err == nil {
		return errStore
	}
	if err != nil {
		return err
	}
	check, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	current, err := s.github.Current(check, job.Target, saved.account)
	if err != nil {
		return err
	}
	if current.BaseSHA != job.BaseSHA || !matchesJob(saved.Config, job, current) {
		return ErrRetryStale
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errStore
	}
	defer tx.Rollback()
	job, saved, rec, err := load(tx)
	if err != nil {
		return err
	}
	if err := queueQuickTest(ctx, tx, job, saved.policy(job.Repository), rec.TestID, rec.LatestAttempt, true); err != nil {
		return err
	}
	return tx.Commit()
}
