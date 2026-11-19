package githubreview

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func (s *Service) runComparisons() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for s.ctx.Err() == nil {
		if err := s.advanceComparison(s.ctx); err != nil && !errors.Is(err, sql.ErrNoRows) && s.ctx.Err() == nil {
			s.recordError(err)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) advanceComparison(ctx context.Context) error {
	before, err := s.store.nextComparison(ctx)
	if err != nil {
		return err
	}
	current, err := s.store.comparisonCurrent(ctx, before)
	if err != nil {
		return err
	}
	run := before
	if !current {
		run.Error = "The candidate comparison is no longer current. Unsubmitted work was canceled."
	}
	allowed := current && run.Error == ""
	stepCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	s.advanceComparisonLeg(stepCtx, &run, allowed)
	if err := ctx.Err(); err != nil {
		// Compute may already have accepted the request. Replay with the same
		// persisted binding and side-specific request key on the next start.
		return err
	}
	run.UpdatedAt = time.Now().UTC()
	return s.store.saveComparison(ctx, before, run)
}

func pendingComparisonLeg(leg ComparisonLeg) bool {
	return leg.Status == "queued" || leg.Status == "running"
}

func completeComparisonBinding(b *compute.ProjectBinding) bool {
	return b != nil && b.HostID != "" && b.ProjectID != "" && b.MachineID != "" && b.BootID != "" && b.InstanceID != ""
}

func (s *Service) advanceComparisonLeg(ctx context.Context, run *CandidateComparison, allowed bool) {
	leg, side := &run.Baseline, "baseline"
	run.Status = "running"
	if !pendingComparisonLeg(*leg) {
		leg, side = &run.Head, "head"
	}
	if pendingComparisonLeg(*leg) {
		leg.UpdatedAt = time.Now().UTC()
		switch {
		case !allowed && leg.Binding == nil:
			leg.Status, leg.Error = "canceled", run.Error
		case side == "head" && !completeComparisonBinding(run.Baseline.Binding):
			leg.Status, leg.Error = "error", "The baseline did not establish a dedicated VM binding for the head."
		case side == "head" && leg.Binding == nil:
			// Make the pinned head dispatch durable before its first Compute
			// write. Revocation after a lost response must recover that receipt.
			binding := *run.Baseline.Binding
			leg.Binding, leg.VMID, leg.ProjectID = &binding, binding.MachineID, binding.ProjectID
			leg.Status = "running"
		case s.vm == nil:
			leg.Status, leg.Error = "error", "Configure a dedicated review VM on this workstation."
		default:
			var pin *compute.ProjectBinding
			if side == "head" {
				pin = run.Baseline.Binding
			}
			s.pollComparisonLeg(ctx, run, leg, side, pin, allowed)
		}
	}
	if !pendingComparisonLeg(run.Baseline) && !pendingComparisonLeg(run.Head) {
		finishComparison(run)
	}
}

func (s *Service) pollComparisonLeg(ctx context.Context, run *CandidateComparison, leg *ComparisonLeg, side string, pin *compute.ProjectBinding, allowed bool) {
	attempt := leg.VMTestAttempt
	if pin != nil {
		if attempt.Binding == nil || *attempt.Binding != *pin {
			leg.Status, leg.Error = "error", "The two comparison sides have different VM bindings."
			return
		}
	}
	attempt.ObserveOnly = !allowed
	target := Target{Repository: run.Repository, Number: run.Number, BaseSHA: run.BaseSHA, HeadSHA: leg.TargetSHA}
	command := append(slices.Clone(run.Runtime.Command), run.Assertion)
	key := reviewDigest("candidate-comparison", run.Account, run.Repository, run.Number, run.CandidateID, run.ID, side)
	result, err := s.vm.Poll(ctx, target, command, key, attempt)
	if err != nil {
		leg.Error = safeError(err)
		if attempt.Binding != nil {
			// An unacknowledged write may have reached Compute.
			leg.Status = "running"
		} else {
			leg.Status = "error"
		}
		return
	}
	if (attempt.Binding != nil && (result.Binding == nil || *result.Binding != *attempt.Binding)) ||
		((result.TaskID != "" || result.Status == "running" || result.Status == "passed" || result.Status == "failed") &&
			(!completeComparisonBinding(result.Binding) || result.VMID != result.Binding.MachineID || result.ProjectID != result.Binding.ProjectID)) {
		leg.Status, leg.Error = "error", "Compute returned a different or incomplete VM binding."
		return
	}
	switch result.Status {
	case "running", "passed", "failed", "canceled", "error":
		result.ObserveOnly = false
		leg.VMTestAttempt = result
	default:
		leg.Status, leg.Error = "error", "Compute returned an unknown comparison task status."
	}
}

func finishComparison(run *CandidateComparison) {
	run.Outcome = "inconclusive"
	b, h := run.Baseline, run.Head
	if b.Status == "canceled" || h.Status == "canceled" {
		run.Status = "canceled"
		return
	}
	run.Status = "error"
	if (b.Status != "passed" && b.Status != "failed") || (h.Status != "passed" && h.Status != "failed") {
		if run.Error == "" {
			run.Error = "The VM could not complete both assertions."
		}
		return
	}
	if !completeComparisonBinding(b.Binding) || !completeComparisonBinding(h.Binding) || *b.Binding != *h.Binding ||
		b.TaskID == "" || h.TaskID == "" || b.AttemptID == "" || h.AttemptID == "" || b.TaskID == h.TaskID ||
		b.CheckoutSHA != run.MergeBaseSHA || h.CheckoutSHA != run.HeadSHA || b.TargetSHA != b.CheckoutSHA || h.TargetSHA != h.CheckoutSHA {
		run.Error = "The two original guest checkouts and their common VM binding could not be verified."
		return
	}
	run.Status, run.Error = "completed", ""
	switch {
	case b.Status == "passed" && h.Status == "passed":
		run.Outcome = "both_passed"
	case b.Status == "failed" && h.Status == "failed":
		run.Outcome = "both_failed"
	case h.Status == "failed":
		run.Outcome = "head_only_failed"
	default:
		run.Outcome = "baseline_only_failed"
	}
}
