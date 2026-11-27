package githubreview

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func TestRepositoryVMTargetsPersistAndRespectEnvOverride(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "reviews.sqlite"), true)
	adapter := &gateway.Gateway{}
	envTarget := ReviewVMTarget{
		Name:      "Env Python VM",
		HostID:    compute.NewID("host"),
		MachineID: compute.NewID("vm"),
		ProjectID: compute.NewID("project"),
	}
	uiTargetAlpha := ReviewVMTarget{
		Name:      "UI Alpha VM",
		HostID:    compute.NewID("host"),
		MachineID: compute.NewID("vm"),
		ProjectID: compute.NewID("project"),
	}
	uiTargetBeta := ReviewVMTarget{
		Name:      "UI Beta VM",
		HostID:    compute.NewID("host"),
		MachineID: compute.NewID("vm"),
		ProjectID: compute.NewID("project"),
	}
	service := newService(ctx, func() {}, ledger, &workerGitHub{}, connectedCLI{}, &countedReviewer{})
	service.vm = ComputeVMTests{
		Gateway:           adapter,
		RepositoryTargets: map[string]ReviewVMTarget{"owner/alpha": envTarget},
	}

	settings := workerSettings(t, ledger)
	cfg := settings.Config
	cfg.RepositoryVMTargets = map[string]ReviewVMTarget{
		"owner/alpha": uiTargetAlpha,
		"OWNER/BETA":  uiTargetBeta,
	}
	if err := service.configure(ctx, cfg); err != nil {
		t.Fatalf("configure with repositoryVmTargets failed: %v", err)
	}

	reloaded, err := ledger.settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RepositoryVMTargets["owner/beta"] != uiTargetBeta {
		t.Fatalf("persisted UI VM target mismatch: %+v", reloaded.RepositoryVMTargets)
	}

	// Env override wins for owner/alpha; UI setting applies to owner/beta.
	alphaVM := service.vmForRepository("owner/alpha").(ComputeVMTests)
	if alphaVM.MachineID != envTarget.MachineID || alphaVM.ProjectID != envTarget.ProjectID {
		t.Fatalf("expected env override for owner/alpha, got %+v", alphaVM)
	}
	betaVM := service.vmForRepository("owner/beta").(ComputeVMTests)
	if betaVM.Gateway != adapter || betaVM.MachineID != uiTargetBeta.MachineID || betaVM.ProjectID != uiTargetBeta.ProjectID {
		t.Fatalf("expected UI target for owner/beta, got %+v", betaVM)
	}

	envs := service.repositoryEnvironments([]string{"owner/alpha", "owner/beta"})
	if envs["owner/alpha"].Source != "env" || !envs["owner/alpha"].Configured || envs["owner/alpha"].MachineID != envTarget.MachineID {
		t.Fatalf("unexpected alpha env metadata: %+v", envs["owner/alpha"])
	}
	if envs["owner/beta"].Source != "settings" || !envs["owner/beta"].Configured || envs["owner/beta"].MachineID != uiTargetBeta.MachineID {
		t.Fatalf("unexpected beta env metadata: %+v", envs["owner/beta"])
	}
}

func TestGitHubReviewNotificationsRecordAndResolveFindings(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "reviews.sqlite"), true)
	service := newService(ctx, func() {}, ledger, &workerGitHub{}, connectedCLI{}, &countedReviewer{})
	now := time.Now().UTC()
	target := Target{
		Repository: "owner/alpha",
		Number:     42,
		Title:      "Fix auth check",
		BaseSHA:    strings.Repeat("a", 40),
		HeadSHA:    strings.Repeat("b", 40),
		Source:     sourceRepository,
	}
	if err := ledger.enqueue(ctx, "owner", []Target{target}, []Target{target}, now); err != nil {
		t.Fatal(err)
	}
	job := workerJob(t, ledger, target, "queued")
	if err := ledger.startReview(ctx, job, now); err != nil {
		t.Fatal(err)
	}
	started, err := readExactJob(ctx, ledger.db, "owner", target)
	if err != nil {
		t.Fatal(err)
	}
	started.Status = "analyzed"
	started.UpdatedAt = now.Add(time.Second)
	started.Findings = []Finding{{
		Path:     "auth.go",
		Line:     10,
		Side:     "head",
		Severity: "high",
		Title:    "Missing permission check",
		Body:     "Ensure owner check runs before mutation.",
	}}
	if err := ledger.update(ctx, started); err != nil {
		t.Fatal(err)
	}

	batches, err := service.NotificationSources(ctx, nil)
	if err != nil {
		t.Fatalf("NotificationSources failed: %v", err)
	}
	if len(batches) != 1 || len(batches[0].Events) != 1 {
		t.Fatalf("expected 1 review finding event, got %+v", batches)
	}
	event := batches[0].Events[0]
	if event.State != "findings" || event.Target.Kind != "github_review" || event.Target.Repository != "owner/alpha" || event.Target.PullRequest != 42 {
		t.Fatalf("unexpected notification event: %+v", event)
	}

	// Cursor advances cleanly with no duplicate emission.
	nextBatches, err := service.NotificationSources(ctx, map[string]uint64{batches[0].SourceID: batches[0].Next})
	if err != nil || len(nextBatches) != 1 || len(nextBatches[0].Events) != 0 {
		t.Fatalf("expected no duplicate events after cursor, got %+v, err=%v", nextBatches, err)
	}

	// Resolve confirms current analyzed revision.
	res, err := service.ResolveNotification(ctx, event.Target)
	if err != nil || !res.Available || !res.Current || res.State != "findings" {
		t.Fatalf("expected current available resolution, got %+v, err=%v", res, err)
	}

	// Superseding the PR marks the notification stale while keeping it available.
	newer := target
	newer.HeadSHA = strings.Repeat("c", 40)
	if err := ledger.enqueue(ctx, "owner", []Target{newer}, []Target{newer}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	staleRes, err := service.ResolveNotification(ctx, event.Target)
	if err != nil || !staleRes.Available || staleRes.Current {
		t.Fatalf("expected available but non-current resolution after supersede, got %+v, err=%v", staleRes, err)
	}

	// Mismatched store ID stays unavailable.
	wrongStore := event.Target
	wrongStore.StoreID = strings.Repeat("0", 32)
	missingRes, err := service.ResolveNotification(ctx, wrongStore)
	if err != nil || missingRes.Available {
		t.Fatalf("expected unavailable resolution for wrong store, got %+v, err=%v", missingRes, err)
	}
}

func TestResolveNotificationIgnoresWrongKind(t *testing.T) {
	ctx := t.Context()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "reviews.sqlite"), true)
	service := newService(ctx, func() {}, ledger, &workerGitHub{}, connectedCLI{}, &countedReviewer{})
	res, err := service.ResolveNotification(ctx, notificationevent.Target{Kind: "codex", StoreID: "other", EventID: "evt"})
	if err != nil || res.Available {
		t.Fatalf("expected unavailable resolution for non-review target, got %+v, err=%v", res, err)
	}
}
