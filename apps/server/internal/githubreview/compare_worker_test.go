package githubreview

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type comparisonPoll func(context.Context, Target, []string, string, VMTestAttempt) (VMTestAttempt, error)

func (p comparisonPoll) Poll(ctx context.Context, t Target, cmd []string, key string, a VMTestAttempt) (VMTestAttempt, error) {
	return p(ctx, t, cmd, key, a)
}

func workerComparison() CandidateComparison {
	merge, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	return CandidateComparison{
		ID: "request-one", Account: "reviewer", CandidateID: "candidate", Repository: "owner/library", Number: 17,
		MergeBaseSHA: merge, HeadSHA: head, BaseSHA: strings.Repeat("c", 40), Runtime: ComparisonRuntime{ID: "python", Name: "Python", Command: []string{"python3", "-c"}},
		Assertion: "value = 1\nassert value == 1", Status: "queued",
		Baseline: ComparisonLeg{TargetSHA: merge, VMTestAttempt: VMTestAttempt{Status: "queued"}},
		Head:     ComparisonLeg{TargetSHA: head, VMTestAttempt: VMTestAttempt{Status: "queued"}},
	}
}

func workerBinding() compute.ProjectBinding {
	return compute.ProjectBinding{HostID: "host", ProjectID: "project", MachineID: "vm", BootID: "boot", InstanceID: "instance", ProjectRevision: 1, VolumeID: "volume", VolumeRevision: 1}
}

func TestComparisonWorkerPinsSameBootAcrossLostReceiptsAndRestart(t *testing.T) {
	run := workerComparison()
	pin := workerBinding()
	keys := make(map[string]string)
	calls := make(map[string]int)
	vm := comparisonPoll(func(_ context.Context, target Target, command []string, key string, a VMTestAttempt) (VMTestAttempt, error) {
		if !slices.Equal(command, []string{"python3", "-c", run.Assertion}) || target.BaseSHA != run.BaseSHA || compute.ValidateTaskSpec(reviewTaskSpec(target, command)) != nil {
			t.Fatalf("incorrect command or review target: %+v %q", target, command)
		}
		sha := target.HeadSHA
		if a.Binding == nil {
			if sha != run.MergeBaseSHA || len(calls) > 0 {
				t.Fatal("bound a new guest after the initial baseline capture")
			}
			a.Binding, a.VMID, a.ProjectID = &pin, pin.MachineID, pin.ProjectID
			a.Status = "running"
			return a, nil
		}
		if *a.Binding != pin {
			t.Fatal("guest or boot changed between submissions")
		}
		if old := keys[sha]; old != "" && key != old {
			t.Fatal("request key changed during receipt recovery")
		}
		keys[sha] = key
		calls[sha]++
		if calls[sha] == 1 {
			return VMTestAttempt{}, errors.New("lost acknowledgement")
		}
		a.TaskID, a.AttemptID = "task-"+sha, "attempt-"+sha
		a.CheckoutSHA, a.Status = sha, "passed"
		if sha == run.HeadSHA {
			a.Status = "failed"
		}
		return a, nil
	})
	service := &Service{vm: vm}
	for i := 0; i < 6; i++ {
		service.advanceComparisonLeg(t.Context(), &run, true)
		// Reconstruct only what SQLite can persist between progress polls.
		saved, err := json.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}
		run = CandidateComparison{}
		if err := json.Unmarshal(saved, &run); err != nil {
			t.Fatal(err)
		}
	}
	if run.Status != "completed" || run.Outcome != "head_only_failed" || run.Baseline.CheckoutSHA != run.MergeBaseSHA ||
		run.Head.CheckoutSHA != run.HeadSHA || calls[run.MergeBaseSHA] != 2 || calls[run.HeadSHA] != 2 ||
		keys[run.MergeBaseSHA] == keys[run.HeadSHA] {
		t.Fatalf("did not retain both original receipts across replay: %+v", run)
	}
}

func TestComparisonWorkerRejectsDifferentBootAndUnverifiedCheckout(t *testing.T) {
	pin := workerBinding()
	for _, test := range []struct{ name, base, head, outcome string }{
		{"passes", "passed", "passed", "both_passed"},
		{"two failures", "failed", "failed", "both_failed"},
		{"fixed", "failed", "passed", "baseline_only_failed"},
		{"regressed", "passed", "failed", "head_only_failed"},
		{"unknown checkout", "passed", "failed", "inconclusive"},
		{"different boot", "passed", "failed", "inconclusive"},
		{"no baseline", "error", "passed", "inconclusive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := workerComparison()
			run.Baseline.Binding, run.Baseline.CheckoutSHA, run.Baseline.Status = &pin, run.MergeBaseSHA, test.base
			run.Baseline.TaskID, run.Baseline.AttemptID = "task-1", "attempt-1"
			if test.name == "no baseline" {
				run.Baseline.Binding = nil
			}
			vm := comparisonPoll(func(_ context.Context, _ Target, _ []string, _ string, a VMTestAttempt) (VMTestAttempt, error) {
				if a.Binding == nil || test.name == "no baseline" {
					t.Fatal("started head without the baseline's binding")
				}
				a.TaskID, a.AttemptID, a.Status, a.CheckoutSHA = "task-2", "attempt-2", test.head, run.HeadSHA
				if test.name == "unknown checkout" {
					a.CheckoutSHA = ""
				}
				if test.name == "different boot" {
					other := *a.Binding
					other.BootID = "replacement"
					a.Binding = &other
				}
				return a, nil
			})
			service := &Service{vm: vm}
			service.advanceComparisonLeg(t.Context(), &run, true)
			if pendingComparisonLeg(run.Head) {
				service.advanceComparisonLeg(t.Context(), &run, true)
			}
			if run.Outcome != test.outcome || ((run.Status == "completed") != (test.outcome != "inconclusive")) {
				t.Fatalf("wrong classification: %+v", run)
			}
		})
	}
}

func TestComparisonRevocationObservesOriginalReceiptAndSkipsUnsubmittedHead(t *testing.T) {
	run := workerComparison()
	pin := workerBinding()
	run.Baseline.VMID, run.Baseline.ProjectID, run.Baseline.Binding = pin.MachineID, pin.ProjectID, &pin
	run.Baseline.Status = "running"
	run.Error = "The review comparison changed."
	calls := 0
	s := &Service{vm: comparisonPoll(func(_ context.Context, target Target, _ []string, _ string, a VMTestAttempt) (VMTestAttempt, error) {
		calls++
		if !a.ObserveOnly || target.HeadSHA != run.MergeBaseSHA {
			t.Fatal("submitted additional work after eligibility was revoked")
		}
		a.Status, a.CheckoutSHA, a.TaskID, a.AttemptID = "passed", target.HeadSHA, "t1", "a1"
		return a, nil
	})}
	s.advanceComparisonLeg(t.Context(), &run, false)
	s.advanceComparisonLeg(t.Context(), &run, false)
	if calls != 1 || run.Status != "canceled" || run.Outcome != "inconclusive" || run.Baseline.CheckoutSHA != run.MergeBaseSHA || run.Head.TaskID != "" {
		t.Fatalf("incorrect cancellation or lost evidence: %+v", run)
	}
}

func TestComparisonPersistsHeadBindingBeforeDispatchAndRecoversAfterRevocation(t *testing.T) {
	run := workerComparison()
	pin := workerBinding()
	run.Baseline.Binding = &pin
	run.Baseline.VMID, run.Baseline.ProjectID = pin.MachineID, pin.ProjectID
	run.Baseline.Status, run.Baseline.CheckoutSHA = "passed", run.MergeBaseSHA
	run.Baseline.TaskID, run.Baseline.AttemptID = "base-task", "base-try"
	var calls int
	s := &Service{vm: comparisonPoll(func(_ context.Context, target Target, _ []string, _ string, a VMTestAttempt) (VMTestAttempt, error) {
		calls++
		if !a.ObserveOnly || target.HeadSHA != run.HeadSHA || a.Binding == nil || *a.Binding != pin {
			t.Fatal("revoked head must recover its original dispatch without resubmitting")
		}
		a.Status = "canceled"
		return a, nil
	})}
	s.advanceComparisonLeg(t.Context(), &run, true)
	if calls != 0 || run.Head.Binding == nil || *run.Head.Binding != pin || run.Head.Status != "running" {
		t.Fatal("head dispatch binding did not survive before the first write")
	}
	s.advanceComparisonLeg(t.Context(), &run, false)
	if calls != 1 || run.Head.Status != "canceled" || run.Status != "canceled" {
		t.Fatalf("lost head dispatch was not reconciled: %+v", run)
	}
}
