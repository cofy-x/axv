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

const reviewDatabaseFormat = 0x41585232 // AXR2

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
	// A model process from the stopped service cannot publish. An uncertain write
	// remains in the outbox and is reconciled before another POST.
	_, err := s.db.ExecContext(ctx, `
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
	var repositories, publishRepositories, policies string
	err := db.QueryRowContext(ctx, `
		SELECT repositories, publish_repositories, interval_minutes, daily_limit, enabled, revision,
			github_account, codex_connected, repository_policies
		FROM review_settings WHERE singleton=1
	`).Scan(&repositories, &publishRepositories, &result.IntervalMinutes, &result.DailyLimit, &result.Enabled,
		&result.Revision, &result.account, &result.codex, &policies)
	if err != nil {
		return savedSettings{}, err
	}
	err = errors.Join(json.Unmarshal([]byte(repositories), &result.Repositories),
		json.Unmarshal([]byte(publishRepositories), &result.PublishRepositories), json.Unmarshal([]byte(policies), &result.RepositoryPolicies))
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
	publishRepositories, err := json.Marshal(c.PublishRepositories)
	if err != nil {
		return err
	}
	policies, err := json.Marshal(c.RepositoryPolicies)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE review_settings SET repositories=?, publish_repositories=?, interval_minutes=?, daily_limit=?,
			enabled=?, repository_policies=?, revision=revision+1
		WHERE singleton=1 AND revision=?`, string(repositories), string(publishRepositories), c.IntervalMinutes,
		c.DailyLimit, c.Enabled, string(policies), c.Revision)
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
    WHERE account=? AND repository=? AND number=? AND head_sha<>? AND status<>'stale'
   `, now.UnixMilli(), account, t.Repository, t.Number, t.HeadSHA); err != nil {
			return err
		}
	}
	for _, t := range targets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO review_jobs (account, repository, number, title, base_sha, head_sha, source, status, updated_at)
			VALUES (?,?,?,?,?,?,?,'queued',?)
			ON CONFLICT(account,repository,number,head_sha)
			DO UPDATE SET title=excluded.title, source=excluded.source,
    checks_due=CASE WHEN review_jobs.policy IS NOT NULL AND review_jobs.status<>'stale'
      AND review_jobs.checks_due IS NULL THEN excluded.updated_at ELSE review_jobs.checks_due END,
				base_sha=CASE WHEN (review_jobs.status='queued' AND review_jobs.policy IS NULL) OR (review_jobs.status='stale' AND review_jobs.observed_base_sha='' AND review_jobs.findings='[]' AND review_jobs.policy IS NULL)
					THEN excluded.base_sha ELSE review_jobs.base_sha END,
				status=CASE WHEN review_jobs.status='stale' AND review_jobs.observed_base_sha='' AND review_jobs.findings='[]' AND review_jobs.policy IS NULL
					THEN 'queued' ELSE review_jobs.status END,
				error=CASE WHEN review_jobs.status='stale' AND review_jobs.observed_base_sha='' AND review_jobs.findings='[]' AND review_jobs.policy IS NULL
					THEN '' ELSE review_jobs.error END
		`, account, t.Repository, t.Number, t.Title, t.BaseSHA, t.HeadSHA, t.Source, now.UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) nextJob(ctx context.Context, account string, retryBefore time.Time, allowAnalysis, pollingEnabled bool) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, "SELECT "+jobFields+`
		FROM review_jobs WHERE account=? AND (? OR (manual_review=1 AND status='queued')) AND
		(status='publishing' OR (? AND status='queued')) AND (attempts=0 OR updated_at<=?)
		ORDER BY (status='publishing') DESC, (source='requested') DESC, updated_at ASC, repository, number LIMIT 1
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
		WHERE account=? AND repository=? AND number=? AND base_sha=? AND head_sha=? AND status='queued'
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
		FROM review_jobs WHERE account=? AND repository=? AND number=? AND head_sha=?
	`, account, repository, number, headSHA))
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
	status, findings := "queued", "[]"
	if job.Status == "failed" && len(job.Findings) > 0 && job.BaseSHA == current.BaseSHA {
		status = "analyzed"
		if !manual && settings.autoPublishes(current.Repository) {
			status = "publishing"
		}
		encoded, err := json.Marshal(job.Findings)
		if err != nil {
			return err
		}
		findings = string(encoded)
	}
	source := sourceRepository
	if current.Requested {
		source = sourceRequested
	}
	policy, err := json.Marshal(settings.policy(current.Repository))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE review_jobs SET base_sha=?, title=?, source=?, status=?, findings=?, comment_url='', error='',
			merge_base_sha=CASE WHEN base_sha<>? THEN '' ELSE merge_base_sha END,
        policy=CASE WHEN base_sha<>? AND policy IS NOT NULL THEN ? ELSE policy END,
        observed_base_sha='', equivalent_base_sha='',comparison_saved_status='',
        checks_due=CASE WHEN base_sha<>? AND policy IS NOT NULL THEN ? ELSE checks_due END,
        manual_review=?, attempts=0, updated_at=? WHERE account=? AND repository=? AND number=? AND head_sha=?
	`, current.BaseSHA, current.Title, source, status, findings, current.BaseSHA, current.BaseSHA, string(policy), current.BaseSHA, now.UnixMilli(), manual, now.UnixMilli(), job.account, job.Repository,
		job.Number, job.HeadSHA); err != nil {
		return err
	}
	return queueRetargetedTest(ctx, tx, job, current, settings.policy(current.Repository), now)
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
	case "analyzed", "clean", "publishing", "published":
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
  UPDATE review_jobs SET status=CASE WHEN status='stale' THEN status WHEN manual_review=1 AND ?='publishing' THEN 'analyzed' ELSE ? END,
  findings=?,comment_url=?,error=CASE WHEN status='stale' THEN error ELSE ? END,attempts=?,updated_at=?,
  review_stage=CASE WHEN ?<>'' AND json_extract(review_stage,'$.status') IN ('running','queued')
    THEN json_set(review_stage,'$.status',?,'$.updatedAt',?,'$.error',?,
     '$.messages[#]',json_object('at',?,'text',?)) ELSE review_stage END
  WHERE account=? AND repository=? AND number=? AND base_sha=? AND head_sha=?
  AND (status<>'stale' OR (?<>'' AND evidence_key=?))
  `, job.Status, job.Status, string(findings), job.CommentURL, job.Error, job.attempts, job.UpdatedAt.UnixMilli(), reviewStatus, reviewStatus,
		job.UpdatedAt.UTC().Format(time.RFC3339Nano), job.Error, job.UpdatedAt.UTC().Format(time.RFC3339Nano), message, job.account, job.Repository, job.Number, job.BaseSHA, job.HeadSHA, job.evidenceKey, job.evidenceKey)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 1 && len(job.Findings) > 0 {
		persisted, err := readJob(ctx, tx, job.account, job.Repository, job.Number, job.HeadSHA)
		if err != nil {
			return err
		}
		if err := indexCandidates(ctx, tx, persisted); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var job Job
	var findings, checks, tests, stage string
	var policy sql.NullString
	var updated int64
	fields := []any{&job.account, &job.Repository, &job.Number, &job.Title, &job.BaseSHA, &job.HeadSHA,
		&job.Source, &job.Status, &findings, &job.CommentURL, &job.Error, &job.attempts, &updated, &policy, &checks, &tests, &job.evidenceKey, &stage, &job.MergeBaseSHA, &job.ObservedBaseSHA, &job.equivalentBaseSHA, &job.manualReview}
	err := row.Scan(fields...)
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
