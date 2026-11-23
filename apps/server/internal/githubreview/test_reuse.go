package githubreview

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

// A test selected only from the head can share a verified result after a base
// change. A queued review never submits or polls the original task itself.
func (s *Service) reuseHeadOnlyTest(ctx context.Context, job Job, attempt VMTestAttempt) (VMTestAttempt, bool, error) {
	vm, ok := s.vmForRepository(job.Repository).(scenarioBindingProvider)
	if !ok || usesChangedPaths(job.Evidence.Policy) {
		return attempt, false, nil
	}
	prior, err := s.store.priorHeadTests(ctx, job, attempt)
	if err != nil || len(prior) == 0 {
		return attempt, false, err
	}
	check, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	binding, err := vm.currentReviewBinding(check)
	if err != nil {
		return attempt, false, nil
	}
	pending := false
	for _, old := range prior {
		if old.Binding == nil || *old.Binding != binding {
			continue
		}
		if old.Status == "running" && (old.RequestID != "" || old.TaskID != "") {
			pending = true
			continue
		}
		if old.CheckoutSHA != job.HeadSHA || old.TaskID == "" || old.AttemptID == "" ||
			old.ProjectID != binding.ProjectID || old.VMID != binding.MachineID || !verifiedVMAssertion(old) {
			continue
		}
		old.ReusedFrom = &QuickTestOrigin{BaseSHA: old.ReceiptBaseSHA, Attempt: old.Attempt}
		old.SelectionBaseSHA = attempt.SelectionBaseSHA
		return old, true, nil
	}
	if pending {
		attempt.Status = "running"
	}
	return attempt, pending, nil
}

func (s *store) priorHeadTests(ctx context.Context, job Job, attempt VMTestAttempt) ([]VMTestAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT entry.value FROM review_jobs AS prior, json_each(prior.tests) AS entry
		WHERE prior.account=? AND prior.repository=? AND prior.number=? AND prior.head_sha=? AND prior.base_sha<>?
		AND json_extract(entry.value,'$.status') IN ('running','passed','failed')
		AND json_extract(entry.value,'$.receiptBaseSha')=prior.base_sha
		AND json_extract(entry.value,'$.reusedFrom') IS NULL
		AND COALESCE(json_extract(entry.value,'$.exactComparison'),0)=0
		AND json_extract(entry.value,'$.selection')='automatic'
		ORDER BY prior.updated_at DESC, prior.rowid DESC, entry.key DESC
	`, job.account, job.Repository, job.Number, job.HeadSHA, job.BaseSHA)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var prior []VMTestAttempt
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var old VMTestAttempt
		if err := json.Unmarshal([]byte(payload), &old); err != nil {
			return nil, err
		}
		if old.PlanHash == attempt.PlanHash && old.TestID == attempt.TestID && old.EntryPath == attempt.EntryPath && slices.Equal(old.Command, attempt.Command) {
			prior = append(prior, old)
		}
	}
	return prior, rows.Err()
}
