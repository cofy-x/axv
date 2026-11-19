package githubreview

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var errReviewDisconnected = errors.New("Connect GitHub and Codex to review the selected pull request.")

// Resolve the displayed PR against the enrolled GitHub account. The displayed
// SHA can be old, but the request cannot introduce a new repository or PR.
func (s *Service) reviewLatest(ctx context.Context, request RetryRequest) error {
	if !request.valid() {
		return ErrInvalid
	}
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	settings, err := s.store.settings(ctx)
	if err != nil {
		return errStore
	}
	if settings.Revision != request.Revision {
		return ErrConflict
	}
	if !settings.connected() {
		return errReviewDisconnected
	}
	selected, err := displayedReview(ctx, s.store.db, settings.account, request)
	if err != nil {
		return err
	}
	remote, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	current, err := s.github.Current(remote, selected.Target, settings.account)
	if err != nil {
		return err
	}
	if !current.valid() || !strings.EqualFold(current.Repository, selected.Repository) || current.Number != selected.Number {
		return ErrRetryStale
	}
	now := time.Now()
	if err := s.observeComparison(remote, settings.account, current, now); err != nil {
		return err
	}
	if err := s.store.enqueueLatest(ctx, settings.account, request, current, now); err != nil {
		return err
	}
	s.wakeReviews()
	return nil
}

func displayedReview(ctx context.Context, db rowQuerier, account string, req RetryRequest) (Job, error) {
	job, err := readJob(ctx, db, account, req.Repository, req.Number, req.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && job.BaseSHA != req.BaseSHA) {
		return Job{}, ErrRetryStale
	}
	return job, err
}

func (s *store) enqueueLatest(ctx context.Context, account string, req RetryRequest, current Target, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	if settings.Revision != req.Revision || !strings.EqualFold(settings.account, account) {
		return ErrConflict
	}
	if !settings.connected() {
		return errReviewDisconnected
	}
	if _, err := displayedReview(ctx, tx, account, req); err != nil {
		return err
	}
	if current.Closed || current.Draft || (!settings.watches(current.Repository) && !current.Requested) {
		// A newly observed draft/closed head still supersedes prior evidence.
		if err := enqueueJobs(ctx, tx, account, []Target{current}, nil, now); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrRetryStale
	}
	current.Source = sourceRepository
	if current.Requested {
		current.Source = sourceRequested
	}
	if err := enqueueJobs(ctx, tx, account, []Target{current}, []Target{current}, now); err != nil {
		return err
	}
	job, err := readJob(ctx, tx, account, current.Repository, current.Number, current.HeadSHA)
	if err != nil {
		return err
	}
	if job.Status == "failed" || job.Status == "stale" {
		if err := resetReviewForRetry(ctx, tx, settings, job, current, true, now); err != nil {
			return err
		}
	} else if job.Status == "queued" || job.Status == "reviewing" {
		if _, err := tx.ExecContext(ctx, `UPDATE review_jobs SET manual_review=1
 WHERE account=? AND repository=? AND number=? AND head_sha=?`, account, current.Repository, current.Number, current.HeadSHA); err != nil {
			return err
		}
	}
	return tx.Commit()
}
