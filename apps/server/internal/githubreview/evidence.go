package githubreview

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

func seedEvidence(ctx context.Context, tx *sql.Tx, job Job, policy RepositoryPolicy, now time.Time) error {
	encoded, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	tests := []VMTestAttempt{}
	var testDue any
	if len(policy.TestProfiles) > 0 || len(policy.TestCommand) > 0 {
		a, err := newTestAttempt(policy, "", job.BaseSHA, 1, now)
		if err != nil {
			return err
		}
		tests = append(tests, a)
		testDue = now.UnixMilli()
	}
	attempts, err := json.Marshal(tests)
	if err != nil {
		return err
	}
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE review_jobs SET policy=?,checks='{"status":"queued"}',tests=?,checks_due=?,test_due=?,evidence_key=?
  WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=? AND policy IS NULL`, string(encoded), string(attempts), now.UnixMilli(), testDue, hex.EncodeToString(key[:]), job.account, job.Repository, job.Number, job.HeadSHA, job.BaseSHA)
	return err
}

// Independent observers let a long model review, an unavailable check provider
// and a running guest task make progress without blocking PR discovery.
func (s *Service) runEvidence() {
	var workers sync.WaitGroup
	for _, stage := range []string{"checks", "test"} {
		workers.Go(func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for s.ctx.Err() == nil {
				err := s.advanceEvidence(s.ctx, stage, time.Now())
				if err != nil && !errors.Is(err, sql.ErrNoRows) && s.ctx.Err() == nil {
					s.recordError(err)
				}
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
				}
			}
		})
	}
	workers.Wait()
}

func (s *Service) advanceEvidence(ctx context.Context, stage string, now time.Time) error {
	due := "test_due"
	if stage == "checks" {
		due = "checks_due"
	}
	job, err := scanJob(s.store.db.QueryRowContext(ctx, "SELECT "+jobFields+" FROM review_jobs WHERE account=(SELECT github_account FROM review_settings) AND "+due+"<=? ORDER BY "+due+", repository,number LIMIT 1", now.UnixMilli()))
	if err != nil {
		return err
	}
	key := job.evidenceKey
	if job.Evidence == nil {
		return errStore
	}
	if stage == "checks" {
		run, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if job.Status == "stale" {
			return s.writeChecks(ctx, job, key, job.Evidence.Checks, nil)
		}
		observing := job.Evidence.Checks
		if !observing.hasObservation() {
			observing.Status = "running"
		}
		if err := s.writeChecks(ctx, job, key, observing, now.UnixMilli()); err != nil {
			return errStore
		}
		result, err := s.github.Checks(run, job.Target, job.account, job.Evidence.Checks)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		observed := time.Now().UTC()
		if err != nil {
			result = job.Evidence.Checks
			if !result.hasObservation() {
				result = Checks{Status: "error"}
			}
			result.Error = safeError(err)
			result.RefreshErrorAt = &observed
		} else {
			result.UpdatedAt = &observed
		}
		// A later discovery of this open PR refreshes the snapshot, including checks
		// that arrive late or are rerun after a terminal result.
		return s.writeChecks(ctx, job, key, result, nil)
	}
	return s.advanceTest(ctx, job, key, now)
}

func (c Checks) hasObservation() bool {
	return c.UpdatedAt != nil && (c.Status == "passed" || c.Status == "failed" || c.Status == "empty" || c.Status == "neutral" || c.Status == "pending")
}

func (s *Service) writeChecks(ctx context.Context, job Job, key string, result Checks, due any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.store.db.ExecContext(ctx, `UPDATE review_jobs SET checks=?,checks_due=? WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=? AND evidence_key=?`, string(encoded), due, job.account, job.Repository, job.Number, job.HeadSHA, job.BaseSHA, key)
	return err
}

func (s *Service) advanceTest(ctx context.Context, job Job, key string, now time.Time) error {
	tests := job.Evidence.Tests
	if len(tests) == 0 {
		return errStore
	}
	index := 0
	for index < len(tests) && tests[index].Status != "queued" && tests[index].Status != "running" {
		index++
	}
	if index == len(tests) {
		return errStore
	}
	latest := tests[index]
	timeout := 40 * time.Second
	if latest.Status == "queued" && (latest.PlanHash != "" || latest.EntryPath != "") {
		timeout = 2 * time.Minute
	}
	run, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := latest.Command
	if len(command) == 0 && latest.PlanHash == "" {
		command = job.Evidence.Policy.TestCommand
		latest.Command = command
	}
	// Once submitted, observation stays attached to the original compute task,
	// even if discovery subsequently marks this revision stale.
	if latest.TaskID == "" {
		settings, err := s.store.settings(ctx)
		if err != nil {
			return errStore
		}
		policy := settings.policy(job.Repository)
		switch {
		case (latest.SelectionBaseSHA != "" && latest.SelectionBaseSHA != job.BaseSHA) || job.Status == "stale" || !settings.watches(job.Repository) || !latest.matchesPolicy(policy, command):
			latest.Status, latest.Error = "canceled", "The PR or repository test configuration changed before submission."
		case !strings.EqualFold(settings.account, job.account):
			return nil
		default:
			current, err := s.github.Current(run, job.Target, job.account)
			if err != nil {
				latest.Status, latest.Error = "error", safeError(err)
			} else if !matchesJob(settings.Config, job, current) {
				s.policyMu.Lock()
				if current.valid() && !current.Closed && !current.Draft && strings.EqualFold(job.Repository, current.Repository) &&
					job.Number == current.Number && job.HeadSHA == current.HeadSHA && job.BaseSHA != current.BaseSHA {
					// Preflight can see the new base before discovery does. Preserve the
					// completed review so selecting its exact comparison can reuse it.
					err = s.store.recordChangedBase(ctx, job.account, current, job.BaseSHA, key, job.MergeBaseSHA, errOutdated.Error(), now)
				} else {
					_, err = s.store.db.ExecContext(ctx, `UPDATE review_jobs SET status='stale',error=?,updated_at=? WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=? AND evidence_key=?`, errOutdated.Error(), now.UnixMilli(), job.account, job.Repository, job.Number, job.HeadSHA, job.BaseSHA, key)
				}
				s.policyMu.Unlock()
				if err != nil {
					return errStore
				}
				latest.Status, latest.Error = "canceled", errOutdated.Error()
			} else if latest.ExactComparison && current.BaseSHA != job.BaseSHA {
				latest.Status, latest.Error = "canceled", "The PR comparison changed before the confirmed test was submitted."
			} else if latest.Binding == nil && latest.Selection != "" && latest.SelectionReason == "" {
				if err := s.selectTest(run, job, policy, &latest); err != nil {
					latest.Status, latest.Error = "error", safeError(err)
				}
				command = latest.Command
			}
			if err == nil && latest.TaskID == "" && latest.Binding == nil && current.BaseSHA == job.BaseSHA && (latest.Status == "queued" || latest.Status == "running") {
				latest.ReceiptBaseSHA = job.BaseSHA
			}
		}
	}
	firstDispatch := latest.TaskID == "" && (latest.Status == "running" || latest.Status == "queued")
	if firstDispatch {
		var observation VMTestAttempt
		var reuse bool
		if latest.Binding == nil && latest.Attempt == 1 && !latest.ExactComparison && latest.Selection == "automatic" {
			var err error
			observation, reuse, err = s.reuseHeadOnlyTest(run, job, latest)
			if err != nil {
				return err
			}
		}
		// The lookup or test selection may have outlived the approved policy.
		// Serialize final admission and dispatch against settings changes.
		s.policyMu.Lock()
		defer s.policyMu.Unlock()
		allowed, err := s.quickTestDispatchAllowed(ctx, job, latest, command)
		if err != nil {
			return errStore
		}
		if !allowed {
			latest.Status, latest.Error = "canceled", "The PR or repository test configuration changed before submission."
		} else if reuse {
			return s.saveObservedTest(ctx, job, index, observation, now)
		}
	}
	// A previous submission may have reached Compute without its receipt
	// reaching SQLite. On revocation, observe that request without resubmitting.
	if latest.Binding != nil && (latest.Status == "canceled" || latest.Status == "error") {
		latest.ObserveOnly = true
	}
	if latest.ObserveOnly || latest.Status == "running" || latest.Status == "queued" {
		if s.vm == nil {
			latest.Status, latest.Error = "error", "Configure a dedicated review VM on this AXV host."
		} else {
			poll := run
			if firstDispatch {
				var stop context.CancelFunc
				poll, stop = context.WithTimeout(run, 8*time.Second)
				defer stop()
			}
			result, err := s.vmForRepository(job.Repository).Poll(poll, job.Target, command, fmt.Sprintf("%s-%d", key, latest.Attempt), latest)
			if err != nil {
				latest.Error = safeError(err)
				if latest.TaskID != "" {
					latest.Status = "running"
				} else {
					latest.Status = "error"
				}
			} else {
				latest = result
			}
		}
	}
	latest.Command = command
	return s.saveObservedTest(ctx, job, index, latest, now)
}

func (s *Service) saveObservedTest(ctx context.Context, job Job, index int, latest VMTestAttempt, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tests := job.Evidence.Tests
	latest.Attempt = tests[index].Attempt
	latest.UpdatedAt = now.UTC()
	var due any
	switch latest.Status {
	case "running":
		due = now.Add(4 * time.Second).UnixMilli()
	case "passed", "failed", "error", "canceled", "not_applicable":
		if index+1 < len(tests) {
			due = now.UnixMilli()
		}
	default:
		return errors.New("The VM task adapter returned an unknown status.")
	}
	tests[index] = latest
	return saveTestAttempts(ctx, s.store.db, job, job.evidenceKey, tests, due)
}

func (s *Service) quickTestDispatchAllowed(ctx context.Context, job Job, attempt VMTestAttempt, command []string) (bool, error) {
	settings, err := s.store.settings(ctx)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(settings.account, job.account) || !settings.watches(job.Repository) ||
		!attempt.matchesPolicy(settings.policy(job.Repository), command) {
		return false, nil
	}
	current, err := s.store.job(ctx, job.account, job.Repository, job.Number, job.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && current.BaseSHA == job.BaseSHA && current.evidenceKey == job.evidenceKey && current.Status != "stale", err
}

type evidenceWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func saveTestAttempts(ctx context.Context, db evidenceWriter, job Job, key string, tests []VMTestAttempt, due any) error {
	encoded, err := json.Marshal(tests)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE review_jobs SET tests=?,test_due=? WHERE account=? AND repository=? AND number=? AND base_sha=? AND head_sha=? AND evidence_key=?`, string(encoded), due, job.account, job.Repository, job.Number, job.BaseSHA, job.HeadSHA, key)
	return err
}

func (s *Service) retryTest(ctx context.Context, req RetryRequest, attempt int) error {
	return s.retrySelectedTest(ctx, req, attempt, "")
}

func (s *Service) retrySelectedTest(ctx context.Context, req RetryRequest, attempt int, testID string) error {
	if !req.valid() || attempt < 0 || (testID != "" && !testProfileID.MatchString(testID)) {
		return ErrInvalid
	}
	saved, err := s.store.settings(ctx)
	if err != nil {
		return errStore
	}
	if saved.Revision != req.Revision {
		return ErrConflict
	}
	if saved.account == "" || !saved.watches(req.Repository) {
		return ErrRetryStale
	}
	target := Target{Repository: req.Repository, Number: req.Number, BaseSHA: req.BaseSHA, HeadSHA: req.HeadSHA}
	check, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	current, err := s.github.Current(check, target, saved.account)
	if err != nil {
		return err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errStore
	}
	defer tx.Rollback()
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return errStore
	}
	if settings.Revision != req.Revision {
		return ErrConflict
	}
	job, err := readJob(ctx, tx, saved.account, req.Repository, req.Number, req.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRetryStale
	}
	if err != nil {
		return errStore
	}
	if !matchesJob(settings.Config, job, current) || job.BaseSHA != req.BaseSHA || job.Status == "stale" || job.Evidence == nil {
		return ErrRetryStale
	}
	latest, idle := lastQuickTestAttempt(job.Evidence.Tests)
	if !idle || latest != attempt {
		return ErrRetryStale
	}
	if err := queueQuickTest(ctx, tx, job, settings.policy(job.Repository), testID, attempt, false); err != nil {
		return err
	}
	return tx.Commit()
}

func queueQuickTest(ctx context.Context, tx *sql.Tx, job Job, policy RepositoryPolicy, testID string, attempt int, exactComparison bool) error {
	now := time.Now().UTC()
	next, err := newTestAttempt(policy, testID, job.BaseSHA, attempt+1, now)
	if err != nil {
		return err
	}
	next.ExactComparison = exactComparison
	tests := append(job.Evidence.Tests, next)
	if err := saveTestAttempts(ctx, tx, job, job.evidenceKey, tests, now.UnixMilli()); err != nil {
		return errStore
	}
	return nil
}
