package githubreview

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWatchOnlyHoldsOutsideRequestsWithoutDroppingThem(t *testing.T) {
	store := testReviewStore(t, filepath.Join(t.TempDir(), "reviews.sqlite"), true)
	watched := workerTestTarget("OWNER/alpha", 2, sourceRequested)
	outside := workerTestTarget("example/other", 1, sourceRequested)
	outside.Requested = true
	now := time.Now()
	cfg := workerSettings(t, store).Config
	selected := chooseTargets(cfg, []Target{outside, watched})
	if len(selected) != 2 || selected[0].Repository != outside.Repository {
		t.Fatalf("default lost direct requests: %+v", selected)
	}
	if err := store.enqueue(t.Context(), "owner", selected, selected, now); err != nil {
		t.Fatal(err)
	}
	job, err := store.nextJob(t.Context(), "owner", now, true, true)
	if err != nil || job.Number != outside.Number {
		t.Fatalf("default request priority: %+v %v", job, err)
	}

	cfg.WatchOnly = true
	saved := configureWorker(t, store, cfg)
	selected = chooseTargets(saved.Config, []Target{outside, watched})
	if !saved.WatchOnly || len(selected) != 1 || selected[0].Source != sourceRepository {
		t.Fatalf("watch-only discovery: %+v", selected)
	}
	job, err = store.nextJob(t.Context(), "owner", now, true, true)
	if err != nil || job.Number != watched.Number {
		t.Fatalf("watch-only queue: %+v %v", job, err)
	}
	page, err := store.inboxPage(t.Context(), inboxActive, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Jobs {
		if item.Number == outside.Number && item.QueueReason != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("the previously queued request has no visible hold reason")
	}

	if err := store.enqueueSelected(t.Context(), "owner", saved.Revision, &RetryRequest{
		Repository: outside.Repository, Number: outside.Number, HeadSHA: outside.HeadSHA,
		BaseSHA: outside.BaseSHA, Revision: saved.Revision,
	}, outside, now); err != nil {
		t.Fatal(err)
	}
	job, err = store.nextJob(t.Context(), "owner", now, true, false)
	if err != nil || job.Number != outside.Number || !job.manualReview {
		t.Fatalf("explicit review must remain possible: %+v %v", job, err)
	}
}
