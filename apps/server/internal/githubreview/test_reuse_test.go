package githubreview

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type receiptReviewVM struct {
	binding compute.ProjectBinding
	starts  int
}

func (v *receiptReviewVM) currentReviewBinding(context.Context) (compute.ProjectBinding, error) {
	return v.binding, nil
}

func (v *receiptReviewVM) Poll(_ context.Context, target Target, _ []string, key string, attempt VMTestAttempt) (VMTestAttempt, error) {
	if attempt.TaskID == "" {
		v.starts++
		bindVMTestAttempt(&attempt, v.binding, key)
		attempt.TaskID, attempt.AttemptID, attempt.Status = "task-"+key, "attempt-"+key, "running"
		return attempt, nil
	}
	code := 0
	attempt.Status, attempt.CheckoutSHA = "passed", target.HeadSHA
	attempt.ExitCode, attempt.AssertionExitCode = &code, &code
	return attempt, nil
}

func TestSameHeadReusesOnlyMatchingAcceptedQuickTests(t *testing.T) {
	for _, tc := range []struct {
		name      string
		newBoot   bool
		newPolicy bool
	}{
		{name: "waits for and reuses the original task"},
		{name: "a different boot needs a new task", newBoot: true},
		{name: "a different command needs a new task", newPolicy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			db := testReviewStore(t, filepath.Join(t.TempDir(), "reviews.sqlite"), true)
			original := workerTestTarget("owner/alpha", 3, sourceRepository)
			current := original
			cfg := workerSettings(t, db).Config
			cfg.RepositoryPolicies = map[string]RepositoryPolicy{original.Repository: {TestCommand: []string{"go", "test", "./pkg"}}}
			saved := configureWorker(t, db, cfg)
			g := &workerGitHub{
				current: func(Target) (Target, error) { return current, nil },
				merge:   func(target Target) (string, error) { return target.BaseSHA, nil },
				targets: []Target{original},
			}
			vm := &receiptReviewVM{binding: compute.ProjectBinding{HostID: "host", MachineID: "machine", ProjectID: "project", BootID: "first-boot", InstanceID: "guest", ProjectRevision: 1, VolumeID: "volume", VolumeRevision: 1}}
			s := newService(ctx, func() {}, db, g, connectedCLI{}, &countedReviewer{})
			s.vm = vm
			now := time.Now()
			s.scan(ctx, saved, now)
			if err := s.processNext(ctx, saved, now); err != nil {
				t.Fatal(err)
			}
			step := func() {
				t.Helper()
				now = now.Add(time.Minute)
				if err := s.advanceEvidence(ctx, "test", now); err != nil {
					t.Fatal(err)
				}
			}
			step()
			oldTask := workerJob(t, db, original, "analyzed").Evidence.Tests[0].TaskID
			if vm.starts != 1 || oldTask == "" {
				t.Fatal("expected an accepted test for the first comparison")
			}
			current.BaseSHA = strings.Repeat("f", 40)
			if tc.newBoot {
				vm.binding.BootID = "different-boot"
			}
			if tc.newPolicy {
				cfg.RepositoryPolicies[original.Repository] = RepositoryPolicy{TestCommand: []string{"go", "test", "./newpkg"}}
				saved = configureWorker(t, db, cfg)
			}
			if _, err := s.reviewLatest(ctx, latestReviewRef(original, saved.Revision)); err != nil {
				t.Fatal(err)
			}
			if err := s.processNext(ctx, saved, now); err != nil {
				t.Fatal(err)
			}
			step()
			if tc.newBoot || tc.newPolicy {
				step()
				step()
				fresh := workerJob(t, db, current, "analyzed").Evidence.Tests[0]
				if vm.starts != 2 || fresh.Status != "passed" || fresh.TaskID == oldTask || fresh.ReusedFrom != nil {
					t.Fatalf("changed plan or binding reused a previous task: %+v", fresh)
				}
				return
			}
			waiting := workerJob(t, db, current, "analyzed").Evidence.Tests[0]
			if vm.starts != 1 || waiting.Status != "running" || waiting.TaskID != "" || waiting.Binding != nil {
				t.Fatalf("the new review submitted a task while the original was running: %+v", waiting)
			}
			step()
			step()
			old, err := readExactJob(ctx, db.db, saved.account, original)
			if err != nil || old.Evidence == nil || len(old.Evidence.Tests) != 1 {
				t.Fatalf("original attempt disappeared: %+v, %v", old, err)
			}
			previous, fresh := old.Evidence.Tests[0], workerJob(t, db, current, "analyzed").Evidence.Tests[0]
			if vm.starts != 1 || previous.Status != "passed" || previous.ReusedFrom != nil || fresh.Status != "passed" || fresh.TaskID != oldTask || fresh.AttemptID != previous.AttemptID || fresh.ReceiptBaseSHA != original.BaseSHA || fresh.ReusedFrom == nil || *fresh.ReusedFrom != (QuickTestOrigin{BaseSHA: original.BaseSHA, Attempt: previous.Attempt}) {
				t.Fatalf("original receipt was changed or duplicated: old=%+v new=%+v", previous, fresh)
			}
		})
	}
}
