package githubreview

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/sqlitemigrate"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

const reviewDatabaseFormat = 0x41585233 // AXR3

const jobFields = "account, repository, number, title, base_sha, head_sha, source, status, findings, comment_url, error, attempts, updated_at, policy, checks, tests, COALESCE(evidence_key, ''), review_stage, merge_base_sha, observed_base_sha, equivalent_base_sha, manual_review"

type savedSettings struct {
	Config
	account string
	codex   bool
}

func (s savedSettings) active() bool { return s.Enabled && s.connected() }

func (s savedSettings) connected() bool { return s.account != "" && s.codex }

type store struct{ db *sql.DB }

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func openStore(ctx context.Context, filename string) (*store, error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	switch {
	case err == nil:
		if err := file.Close(); err != nil {
			return nil, err
		}
	case errors.Is(err, os.ErrExist):
		info, err := os.Lstat(filename)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("GitHub Review storage must be a regular file")
		}
	default:
		return nil, err
	}
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db}
	if err := s.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) initialize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA busy_timeout=5000;"); err != nil {
		return err
	}
	if err := sqlitemigrate.Apply(ctx, s.db, migrations, reviewDatabaseFormat, nil); err != nil {
		return err
	}
	storeID, err := randomNotificationStoreID()
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO review_notification_store (singleton, store_id) VALUES (1, ?)
	`, storeID); err != nil {
		return err
	}
	// A review interrupted by a desktop shutdown can be retried.
	_, err = s.db.ExecContext(ctx, `
		UPDATE review_jobs SET status='queued', error='',review_stage=json_set(review_stage,'$.status','queued') WHERE status='reviewing';
  UPDATE review_jobs SET review_stage=json_set(review_stage,'$.status','failed',
    '$.error','AXV stopped while reviewing this superseded revision.')
  WHERE status='stale' AND json_extract(review_stage,'$.status')='running';
	`)
	return err
}

func (s *store) settings(ctx context.Context) (savedSettings, error) {
	return readSettings(ctx, s.db)
}

func readSettings(ctx context.Context, db rowQuerier) (savedSettings, error) {
	var result savedSettings
	var repositories, policies, vmTargets string
	err := db.QueryRowContext(ctx, `
		SELECT repositories, interval_minutes, daily_limit, enabled, revision,
			github_account, codex_connected, repository_policies, watch_only, repository_vm_targets
		FROM review_settings WHERE singleton=1
	`).Scan(&repositories, &result.IntervalMinutes, &result.DailyLimit, &result.Enabled,
		&result.Revision, &result.account, &result.codex, &policies, &result.WatchOnly, &vmTargets)
	if err != nil {
		return savedSettings{}, err
	}
	result.PublishRepositories = []string{}
	var decodedVMTargets map[string]ReviewVMTarget
	err = errors.Join(
		json.Unmarshal([]byte(repositories), &result.Repositories),
		json.Unmarshal([]byte(policies), &result.RepositoryPolicies),
		json.Unmarshal([]byte(vmTargets), &decodedVMTargets),
	)
	if len(decodedVMTargets) > 0 {
		result.RepositoryVMTargets = decodedVMTargets
	}
	return result, err
}

func (s *store) configure(ctx context.Context, c Config) error {
	c, err := c.normalized()
	if err != nil {
		return err
	}
	repositories, err := json.Marshal(c.Repositories)
	if err != nil {
		return err
	}
	policies, err := json.Marshal(c.RepositoryPolicies)
	if err != nil {
		return err
	}
	vmTargetsMap := c.RepositoryVMTargets
	if vmTargetsMap == nil {
		vmTargetsMap = map[string]ReviewVMTarget{}
	}
	vmTargets, err := json.Marshal(vmTargetsMap)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE review_settings SET repositories=?, interval_minutes=?, daily_limit=?,
			enabled=?, repository_policies=?, watch_only=?, repository_vm_targets=?, revision=revision+1
		WHERE singleton=1 AND revision=?`, string(repositories), c.IntervalMinutes,
		c.DailyLimit, c.Enabled, string(policies), c.WatchOnly, string(vmTargets), c.Revision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return ErrConflict
	}
	return err
}

func (s *store) attach(ctx context.Context, provider, identity string) error {
	var err error
	if provider == "github" {
		_, err = s.db.ExecContext(ctx, `
			UPDATE review_settings SET github_account=?, revision=revision+1
			WHERE singleton=1 AND github_account<>? COLLATE NOCASE`, identity, identity)
	} else {
		connected := identity != ""
		_, err = s.db.ExecContext(ctx, `
			UPDATE review_settings SET codex_connected=?, revision=revision+1
			WHERE singleton=1 AND codex_connected<>?`, connected, connected)
	}
	return err
}

func (s *store) enqueue(ctx context.Context, account string, observations, targets []Target, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := enqueueJobs(ctx, tx, account, observations, targets, now); err != nil {
		return err
	}
	return tx.Commit()
}

func enqueueJobs(ctx context.Context, tx *sql.Tx, account string, observations, targets []Target, now time.Time) error {
	// A newer head also invalidates older evidence if this particular PR is
	// currently a draft and therefore cannot enter the analysis queue.
	for _, t := range observations {
		if !t.valid() {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
    UPDATE review_jobs SET status='stale', error='Newer pull request version discovered.', updated_at=?
    WHERE account=? AND repository=? AND number=? AND head_sha<>? AND status<>'stale' AND selected_for_head=1
   `, now.UnixMilli(), account, t.Repository, t.Number, t.HeadSHA); err != nil {
			return err
		}
	}
	for _, t := range targets {
		var base string
		var canRetarget, manual bool
		err := tx.QueryRowContext(ctx, `
            UPDATE review_jobs SET title=?, source=?,
                checks_due=CASE WHEN policy IS NOT NULL AND status<>'stale' AND checks_due IS NULL THEN ? ELSE checks_due END,
                status=CASE WHEN status='stale' AND observed_base_sha='' AND findings='[]' AND policy IS NULL THEN 'queued' ELSE status END,
                error=CASE WHEN status='stale' AND observed_base_sha='' AND findings='[]' AND policy IS NULL THEN '' ELSE error END
            WHERE account=? AND repository=? AND number=? AND head_sha=? AND selected_for_head=1
            RETURNING base_sha, status='queued' AND policy IS NULL, manual_review
        `, t.Title, t.Source, now.UnixMilli(), account, t.Repository, t.Number, t.HeadSHA).Scan(&base, &canRetarget, &manual)
		if errors.Is(err, sql.ErrNoRows) {
			err = selectReviewComparison(ctx, tx, account, "", t, false, now)
		} else if err == nil && canRetarget && base != t.BaseSHA {
			err = selectReviewComparison(ctx, tx, account, base, t, manual, now)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *store) nextJob(ctx context.Context, account string, retryBefore time.Time, allowAnalysis, pollingEnabled bool) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, "SELECT "+jobFields+`
	FROM review_jobs WHERE account=? AND status='queued' AND selected_for_head=1 AND (? OR manual_review=1) AND
		? AND (attempts=0 OR updated_at<=?)
		AND (manual_review=1 OR (SELECT watch_only=0 FROM review_settings WHERE singleton=1) OR
			EXISTS (SELECT 1 FROM json_each((SELECT repositories FROM review_settings WHERE singleton=1)) WHERE value=review_jobs.repository COLLATE NOCASE))
		ORDER BY manual_review DESC, (source='requested') DESC, updated_at ASC, repository, number LIMIT 1
	`, account, pollingEnabled, allowAnalysis, retryBefore.UnixMilli()))
}

func (s *store) budget(ctx context.Context, now time.Time) (Budget, error) {
	return readBudget(ctx, s.db, now)
}

func readBudget(ctx context.Context, db rowQuerier, now time.Time) (Budget, error) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	budget := Budget{ResetsAt: start.AddDate(0, 0, 1)}
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM review_attempts WHERE started_at>=? AND started_at<?
	`, start.UnixMilli(), budget.ResetsAt.UnixMilli()).Scan(&budget.Used)
	return budget, err
}

func (s *store) startReview(ctx context.Context, job Job, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	if !settings.connected() || (!settings.Enabled && !job.manualReview) || !strings.EqualFold(settings.account, job.account) {
		return ErrInactive
	}
	if settings.WatchOnly && !job.manualReview && !settings.watches(job.Repository) {
		return ErrInactive
	}
	if !settings.watches(job.Repository) && !job.Requested {
		return ErrConflict
	}
	budget, err := readBudget(ctx, tx, now)
	if err != nil {
		return err
	}
	if budget.Used >= settings.DailyLimit {
		return ErrDailyLimit
	}
	stage, err := json.Marshal(ReviewStage{AuthorContext: job.authorContext, Status: "running", StartedAt: now.UTC(), UpdatedAt: now.UTC(), Messages: []ReviewMessage{{At: now.UTC(), Text: "Reserved a local Codex review for this revision."}}})
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
  UPDATE review_jobs SET status='reviewing', error='', updated_at=?, review_stage=?
		WHERE account=? AND repository=? AND number=? AND base_sha=? AND head_sha=? AND status='queued' AND selected_for_head=1
	`, now.UnixMilli(), string(stage), job.account, job.Repository, job.Number, job.BaseSHA, job.HeadSHA)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	if err := seedEvidence(ctx, tx, job, settings.policy(job.Repository), now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO review_attempts (started_at) VALUES (?)`, now.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *store) job(ctx context.Context, account, repository string, number int, headSHA string) (Job, error) {
	return readJob(ctx, s.db, account, repository, number, headSHA)
}

func readJob(ctx context.Context, db rowQuerier, account, repository string, number int, headSHA string) (Job, error) {
	return scanJob(db.QueryRowContext(ctx, "SELECT "+jobFields+`
		FROM review_jobs WHERE account=? AND repository=? AND number=? AND head_sha=? AND selected_for_head=1
	`, account, repository, number, headSHA))
}

func readExactJob(ctx context.Context, db rowQuerier, account string, target Target) (Job, error) {
	return scanJob(db.QueryRowContext(ctx, "SELECT "+jobFields+`
        FROM review_jobs WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=?
    `, account, target.Repository, target.Number, target.HeadSHA, target.BaseSHA))
}

func (s *store) retry(ctx context.Context, account string, request RetryRequest, current Target, now time.Time) error {
	if !request.valid() {
		return ErrInvalid
	}
	if !current.valid() || current.Closed || current.Draft || !strings.EqualFold(request.Repository, current.Repository) ||
		request.Number != current.Number || request.HeadSHA != current.HeadSHA {
		return ErrRetryStale
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	if settings.Revision != request.Revision {
		return ErrConflict
	}
	if !settings.active() || !strings.EqualFold(settings.account, account) {
		return ErrInactive
	}
	if !settings.watches(current.Repository) && !current.Requested {
		return ErrRetryStale
	}
	job, err := readJob(ctx, tx, account, request.Repository, request.Number, request.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRetryStale
	}
	if err != nil {
		return err
	}
	if !job.canRetryReview() || job.BaseSHA != request.BaseSHA {
		return ErrRetryStale
	}
	if err := resetReviewForRetry(ctx, tx, settings, job, current, job.manualReview, now); err != nil {
		return err
	}
	return tx.Commit()
}

func resetReviewForRetry(ctx context.Context, tx *sql.Tx, settings savedSettings, job Job, current Target, manual bool, now time.Time) error {
	current.Source = sourceRepository
	if current.Requested {
		current.Source = sourceRequested
	}
	if job.BaseSHA != current.BaseSHA {
		if err := selectReviewComparison(ctx, tx, job.account, job.BaseSHA, current, manual, now); err != nil {
			return err
		}
		// Keep observing already submitted tests on the original comparison.
		// Path-scoped tests can select a new command while analysis is queued.
		policy := settings.policy(current.Repository)
		if job.Evidence != nil && usesChangedPaths(policy) {
			return seedEvidence(ctx, tx, Job{Target: current, account: job.account}, policy, now)
		}
		return nil
	}
	status, findings := "queued", "[]"
	if job.Status == "failed" && len(job.Findings) > 0 {
		status = "analyzed"
		encoded, err := json.Marshal(job.Findings)
		if err != nil {
			return err
		}
		findings = string(encoded)
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE review_jobs SET title=?, source=?, status=?, findings=?, comment_url='', error='',
        observed_base_sha='', equivalent_base_sha='',comparison_saved_status='',
		manual_review=?, attempts=0, updated_at=?
		WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=? AND selected_for_head=1
	`, current.Title, current.Source, status, findings, manual, now.UnixMilli(), job.account, job.Repository, job.Number, job.HeadSHA, job.BaseSHA)
	return err
}

// Selecting another base changes the queue target without rewriting the older
// row. A previously completed exact comparison can be selected again.
func selectReviewComparison(ctx context.Context, tx *sql.Tx, account, previousBase string, current Target, manual bool, now time.Time) error {
	if previousBase != "" {
		if _, err := tx.ExecContext(ctx, `
            UPDATE review_jobs SET selected_for_head=0, status='stale',
                comparison_saved_status=CASE WHEN status IN ('clean','analyzed','published') THEN status ELSE comparison_saved_status END,
                error='The pull request has a different comparison base.', observed_base_sha=?, equivalent_base_sha='', updated_at=?
            WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=? AND selected_for_head=1
        `, current.BaseSHA, now.UnixMilli(), account, current.Repository, current.Number, current.HeadSHA, previousBase); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO review_jobs (account,repository,number,title,base_sha,head_sha,source,status,updated_at,manual_review)
		VALUES (?,?,?,?,?,?,?,'queued',?,?)
		ON CONFLICT(account,repository,number,head_sha,base_sha) DO UPDATE SET
			selected_for_head=1, title=excluded.title, source=excluded.source,
			status=COALESCE(NULLIF(review_jobs.comparison_saved_status,''),'queued'),
			comparison_saved_status='', observed_base_sha='', equivalent_base_sha='', error='',
			attempts=0, updated_at=excluded.updated_at, manual_review=excluded.manual_review,
			checks_due=CASE WHEN review_jobs.policy IS NOT NULL THEN COALESCE(review_jobs.checks_due,excluded.updated_at) ELSE review_jobs.checks_due END
	`, account, current.Repository, current.Number, current.Title, current.BaseSHA, current.HeadSHA, current.Source, now.UnixMilli(), manual)
	return err
}

func (s *store) history(ctx context.Context, account string) ([]Job, error) {
	jobs := []Job{}
	if account == "" {
		return jobs, nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT "+jobFields+` FROM review_jobs WHERE account=?
        ORDER BY (
            status<>'stale' AND (
                status='reviewing' OR test_due IS NOT NULL OR json_extract(checks,'$.status')='running'
                OR EXISTS (
                    SELECT 1 FROM review_candidates c JOIN review_candidate_comparisons v ON v.candidate_id=c.id
                    WHERE c.account=review_jobs.account AND c.repository=review_jobs.repository AND c.number=review_jobs.number
                    AND c.base_sha=review_jobs.base_sha AND c.head_sha=review_jobs.head_sha AND c.review_key=review_jobs.evidence_key
                    AND v.status IN ('queued','running')
                )
            )
        ) DESC, updated_at DESC, (status='stale'), repository, number, head_sha LIMIT 100`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range jobs {
		if err := loadCandidateView(ctx, tx, &jobs[i]); err != nil {
			return nil, err
		}
	}
	return jobs, tx.Commit()
}

func (s *store) update(ctx context.Context, job Job) error {
	if job.Findings == nil {
		job.Findings = []Finding{}
	}
	findings, err := json.Marshal(job.Findings)
	if err != nil {
		return err
	}
	// A slow model may finish after discovery observes a new head. Keep the
	// finding and stage evidence on the old row without reviving its job status.
	reviewStatus := ""
	switch job.Status {
	case "analyzed", "clean", "published":
		reviewStatus = "completed"
	case "failed":
		reviewStatus = "failed"
	case "queued":
		reviewStatus = "queued"
	}
	message := map[string]string{"completed": "Finished checking candidate findings against changed lines.", "failed": "The review stopped before completing.", "queued": "Scheduled another review attempt."}[reviewStatus]
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
  UPDATE review_jobs SET status=CASE WHEN status='stale' THEN status ELSE ? END,
  findings=?,comment_url=?,error=CASE WHEN status='stale' THEN error ELSE ? END,attempts=?,updated_at=?,
  comparison_saved_status=CASE WHEN status='stale' AND observed_base_sha<>'' AND ? IN ('analyzed','clean','published')
    THEN ? ELSE comparison_saved_status END,
  review_stage=CASE WHEN ?<>'' AND json_extract(review_stage,'$.status') IN ('running','queued')
    THEN json_set(review_stage,'$.status',?,'$.updatedAt',?,'$.error',?,
     '$.messages[#]',json_object('at',?,'text',?)) ELSE review_stage END
  WHERE account=? AND repository=? AND number=? AND base_sha=? AND head_sha=?
  AND (status<>'stale' OR (?<>'' AND evidence_key=?))
  `, job.Status, string(findings), job.CommentURL, job.Error, job.attempts, job.UpdatedAt.UnixMilli(), job.Status, job.Status, reviewStatus, reviewStatus,
		job.UpdatedAt.UTC().Format(time.RFC3339Nano), job.Error, job.UpdatedAt.UTC().Format(time.RFC3339Nano), message, job.account, job.Repository, job.Number, job.BaseSHA, job.HeadSHA, job.evidenceKey, job.evidenceKey)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 1 && len(job.Findings) > 0 {
		persisted, err := readExactJob(ctx, tx, job.account, job.Target)
		if err != nil {
			return err
		}
		if err := indexCandidates(ctx, tx, persisted); err != nil {
			return err
		}
		if err := recordReviewNotification(ctx, tx, persisted, job.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanJob(row interface{ Scan(...any) error }, extra ...any) (Job, error) {
	var job Job
	var findings, checks, tests, stage string
	var policy sql.NullString
	var updated int64
	fields := []any{&job.account, &job.Repository, &job.Number, &job.Title, &job.BaseSHA, &job.HeadSHA,
		&job.Source, &job.Status, &findings, &job.CommentURL, &job.Error, &job.attempts, &updated, &policy, &checks, &tests, &job.evidenceKey, &stage, &job.MergeBaseSHA, &job.ObservedBaseSHA, &job.equivalentBaseSHA, &job.manualReview}
	err := row.Scan(append(fields, extra...)...)
	if err == nil {
		job.UpdatedAt = time.UnixMilli(updated).UTC()
		err = json.Unmarshal([]byte(findings), &job.Findings)
	}
	if err == nil && policy.Valid {
		ev := new(Evidence)
		err = errors.Join(json.Unmarshal([]byte(policy.String), &ev.Policy), json.Unmarshal([]byte(checks), &ev.Checks), json.Unmarshal([]byte(tests), &ev.Tests), json.Unmarshal([]byte(stage), &ev.Review))
		job.Evidence = ev
	}
	return job, err
}

func targetKey(t Target) string {
	return strings.ToLower(t.Repository) + "#" + strconv.Itoa(t.Number)
}
