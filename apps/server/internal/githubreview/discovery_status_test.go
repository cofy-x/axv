package githubreview

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type discoveryStatusGitHub struct {
	*workerGitHub
	err error
}

type slowDiscoveryGitHub struct {
	*workerGitHub
	started chan struct{}
	result  chan []Target
}

func (g *slowDiscoveryGitHub) Discover(ctx context.Context, _ []string, _ string, _ bool) ([]Target, error) {
	close(g.started)
	select {
	case targets := <-g.result:
		return targets, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestReviewPRWhileDiscoveryIsFetchingKeepsNewerTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ledger := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	saved := workerSettings(t, ledger)
	older := workerTestTarget("owner/alpha", 8, sourceRepository)
	latest := older
	latest.HeadSHA = "cccccccccccccccccccccccccccccccccccccccc"
	other := workerTestTarget("owner/beta", 9, sourceRepository)
	github := &slowDiscoveryGitHub{
		workerGitHub: &workerGitHub{current: func(Target) (Target, error) { return latest, nil }},
		started:      make(chan struct{}), result: make(chan []Target, 1),
	}
	service := newService(ctx, func() {}, ledger, github, connectedCLI{}, &countedReviewer{})
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		service.scan(ctx, saved, time.Now())
	}()
	select {
	case <-github.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ref, err := service.reviewSelected(ctx, reviewPRRequest{Repository: latest.Repository, Number: latest.Number, Revision: saved.Revision}, nil)
	if err != nil || ref.HeadSHA != latest.HeadSHA {
		t.Fatalf("manual review waited for scan or selected the wrong head: %+v %v", ref, err)
	}
	github.result <- []Target{older, other}
	select {
	case <-scanDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	workerJob(t, ledger, latest, "queued")
	workerJob(t, ledger, other, "queued")
	if _, err := ledger.job(ctx, saved.account, older.Repository, older.Number, older.HeadSHA); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale scan result reached the queue: %v", err)
	}
}

func (g *discoveryStatusGitHub) Discover(context.Context, []string, string, bool) ([]Target, error) {
	return nil, g.err
}

func TestDiscoveryStatusRetainsLastSuccessAndTracksNextScan(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	saved := workerSettings(t, db)
	github := &discoveryStatusGitHub{workerGitHub: &workerGitHub{}}
	service := newService(ctx, func() {}, db, github, connectedCLI{}, &countedReviewer{})
	now := time.Now().Truncate(time.Second)
	takeSnapshot := func() Snapshot {
		t.Helper()
		value, err := service.snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if status := takeSnapshot().Discovery; status.NextScanAt != nil || !service.discoveryDue(now, saved.IntervalMinutes) {
		t.Fatalf("initial scan isn't due: %+v", status)
	}
	service.scan(ctx, saved, now)
	first := takeSnapshot().Discovery
	if first.Error != "" || first.LastSuccessAt == nil || !first.LastSuccessAt.Equal(now) ||
		first.NextScanAt == nil || !first.NextScanAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("unexpected status after successful scan: %+v", first)
	}
	retry := now.Add(15 * time.Second)
	if service.discoveryDue(retry, saved.IntervalMinutes) {
		t.Fatal("discovery due before the next interval")
	}
	if err := service.requestRun(ctx); err != nil {
		t.Fatal(err)
	}
	if queued := takeSnapshot().Discovery; queued.NextScanAt != nil || !service.discoveryDue(retry, saved.IntervalMinutes) {
		t.Fatalf("scan request didn't override the interval: %+v", queued)
	}
	github.err = errors.New("fixture discovery failure")
	service.scan(ctx, saved, retry)
	failed := takeSnapshot()
	if failed.LastPollAt == nil || !failed.LastPollAt.Equal(retry) || failed.Discovery.Error != github.err.Error() ||
		failed.Discovery.LastSuccessAt == nil || !failed.Discovery.LastSuccessAt.Equal(now) ||
		failed.Discovery.NextScanAt == nil || !failed.Discovery.NextScanAt.Equal(retry.Add(time.Minute)) {
		t.Fatalf("failed scan didn't retain the success and current deadline: %+v", failed.Discovery)
	}
	config := saved.Config
	config.Enabled = false
	if err := service.configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	if paused := takeSnapshot().Discovery; paused.NextScanAt != nil || paused.LastSuccessAt == nil || !paused.LastSuccessAt.Equal(now) {
		t.Fatalf("pause lost the last scan or reported a future scan: %+v", paused)
	}
}
