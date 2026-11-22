package githubreview

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var (
	errReviewDisconnected = errors.New("Connect GitHub and Codex to review the selected pull request.")
	errReviewOutOfScope   = errors.New("Add this repository in Settings or have a review requested from your connected GitHub account.")
	errReviewNotReady     = errors.New("Choose an open pull request that is ready for review.")
	errReviewLookup       = errors.New("AXV could not verify the pull request on GitHub. Check the connection and try again.")
)

// Resolve the displayed PR against the enrolled GitHub account. The displayed
// SHA can be old, but the request cannot introduce a new repository or PR.
func (s *Service) reviewLatest(ctx context.Context, request RetryRequest) (selectedReview, error) {
	if !request.valid() {
		return selectedReview{}, ErrInvalid
	}
	return s.reviewSelected(ctx, reviewPRRequest{Repository: request.Repository, Number: request.Number, Revision: request.Revision}, &request)
}

func (s *Service) reviewSelected(ctx context.Context, request reviewPRRequest, displayed *RetryRequest) (selectedReview, error) {
	if !request.valid() {
		return selectedReview{}, ErrInvalid
	}
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	settings, err := s.store.settings(ctx)
	if err != nil {
		return selectedReview{}, errStore
	}
	if settings.Revision != request.Revision {
		return selectedReview{}, ErrConflict
	}
	if !settings.connected() {
		return selectedReview{}, errReviewDisconnected
	}
	selected := Target{Repository: request.Repository, Number: request.Number}
	if displayed != nil {
		job, err := displayedReview(ctx, s.store.db, settings.account, *displayed)
		if err != nil {
			return selectedReview{}, err
		}
		selected = job.Target
	}
	remote, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	current, err := s.github.Current(remote, selected, settings.account)
	if err != nil {
		if displayed == nil && !githubConnectionError(err) && !errors.Is(err, ErrGitHubNotFound) {
			return selectedReview{}, errReviewLookup
		}
		return selectedReview{}, err
	}
	if !current.valid() || !strings.EqualFold(current.Repository, selected.Repository) || current.Number != selected.Number {
		if displayed == nil {
			return selectedReview{}, errReviewLookup
		}
		return selectedReview{}, ErrRetryStale
	}
	if displayed == nil {
		if !settings.watches(current.Repository) && !current.Requested {
			return selectedReview{}, errReviewOutOfScope
		}
		if current.Closed || current.Draft {
			return selectedReview{}, errReviewNotReady
		}
	}
	if s.selectedDuringScan != nil {
		s.selectedDuringScan[targetKey(current)] = true
	}
	now := time.Now()
	if err := s.observeComparison(remote, settings.account, current, now); err != nil {
		return selectedReview{}, err
	}
	if err := s.store.enqueueSelected(ctx, settings.account, request.Revision, displayed, current, now); err != nil {
		return selectedReview{}, err
	}
	job, err := s.store.job(ctx, settings.account, current.Repository, current.Number, current.HeadSHA)
	s.wakeReviews()
	if err != nil {
		return selectedReview{}, errStore
	}
	return selectedReview{
		Repository: job.Repository, Number: job.Number, BaseSHA: job.BaseSHA, HeadSHA: job.HeadSHA, Status: job.Status,
	}, nil
}

func displayedReview(ctx context.Context, db rowQuerier, account string, req RetryRequest) (Job, error) {
	job, err := readJob(ctx, db, account, req.Repository, req.Number, req.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && job.BaseSHA != req.BaseSHA) {
		return Job{}, ErrRetryStale
	}
	return job, err
}

func (s *store) enqueueSelected(ctx context.Context, account string, revision uint64, displayed *RetryRequest, current Target, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	if settings.Revision != revision || !strings.EqualFold(settings.account, account) {
		return ErrConflict
	}
	if !settings.connected() {
		return errReviewDisconnected
	}
	if displayed != nil {
		if _, err := displayedReview(ctx, tx, account, *displayed); err != nil {
			return err
		}
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
