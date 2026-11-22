package githubreview

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

const (
	inboxPageSize  = 25
	inboxScanLimit = 100
)

var errReviewJobNotFound = errors.New("This exact review revision is no longer available.")

type InboxPage struct {
	Account    string `json:"account"`
	Jobs       []Job  `json:"jobs"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type ReviewJobView struct {
	Account string `json:"account"`
	Job     Job    `json:"job"`
}

type inboxView string

const (
	inboxAction  inboxView = "action"
	inboxActive  inboxView = "active"
	inboxHistory inboxView = "history"
)

func (v inboxView) valid() bool {
	return v == inboxAction || v == inboxActive || v == inboxHistory
}

// Job insertion order is stable while a review, checks or VM tests update.
// The scoped cursor cannot be carried between accounts or views.
func inboxCursor(account string, view inboxView, rowID int64) string {
	key := string(view) + "/" + strings.ToLower(account) + "/" + strconv.FormatInt(rowID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func inboxBefore(account string, view inboxView, cursor string) (int64, error) {
	if cursor == "" {
		return math.MaxInt64, nil
	}
	if len(cursor) > 130 {
		return 0, ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, ErrInvalid
	}
	id, ok := strings.CutPrefix(string(data), string(view)+"/"+strings.ToLower(account)+"/")
	rowID, err := strconv.ParseInt(id, 10, 64)
	if !ok || err != nil || rowID <= 0 || strconv.FormatInt(rowID, 10) != id {
		return 0, ErrInvalid
	}
	return rowID, nil
}

// A base-only change blocks the current PR only while it has no replacement.
// Determine that over the whole account, before selecting a page.
const inboxStaleBlocker = `(j.status='stale' AND j.observed_base_sha<>'' AND NOT EXISTS (
	SELECT 1 FROM review_jobs newer WHERE newer.account=j.account AND newer.repository=j.repository AND newer.number=j.number
	AND (newer.status<>'stale' OR newer.updated_at>j.updated_at OR (newer.updated_at=j.updated_at AND newer.rowid>j.rowid))
))`

const inboxCurrentCandidate = `c.account=j.account AND c.repository=j.repository AND c.number=j.number
	AND c.job_order=j.rowid AND c.head_sha=j.head_sha AND c.base_sha=j.base_sha AND c.review_key=COALESCE(j.evidence_key,'')`

// Narrow the action scan to jobs that can need attention. The final
// classification below uses the current settings and candidate annotations.
const inboxPossibleAction = inboxStaleBlocker + ` OR (j.status<>'stale' AND (
	j.status='failed' OR
	EXISTS (SELECT 1 FROM review_candidates c
		LEFT JOIN review_candidate_followups f ON f.id=c.id
		LEFT JOIN review_candidate_comment_drafts d ON d.candidate_id=c.id
		WHERE ` + inboxCurrentCandidate + ` AND (
			(c.status='pending' AND COALESCE(f.resolved,0)=0) OR (d.candidate_id IS NOT NULL AND d.reviewed_at IS NULL)
			OR (
				SELECT s.status<>'completed' OR json_extract(s.result,'$.outcome')='inconclusive'
				FROM review_candidate_comparisons s WHERE s.candidate_id=c.id ORDER BY s.rowid DESC LIMIT 1
			)
		)) OR
	(j.status NOT IN ('queued','reviewing') AND (j.policy IS NULL OR COALESCE(json_extract(j.review_stage,'$.status'),'')='')) OR
	(j.policy IS NOT NULL AND COALESCE(json_extract(j.review_stage,'$.status'),'') NOT IN
		('','queued','running','reviewing','publishing','pending','completed','passed','empty','not_applicable','not_configured','not_run')) OR
	(j.policy IS NOT NULL AND (json_extract(j.checks,'$.refreshErrorAt') IS NOT NULL OR
		COALESCE(json_extract(j.checks,'$.status'),'') NOT IN
		('queued','running','reviewing','publishing','pending','completed','passed','empty','not_applicable','not_configured','not_run'))) OR
	(j.policy IS NOT NULL AND json_array_length(j.tests)>0 AND
		(COALESCE(json_extract(j.tests,'$[#-1].status'),'') NOT IN
		('queued','running','reviewing','publishing','pending','completed','passed','empty','not_applicable','not_configured','not_run') OR
		COALESCE(json_extract(j.tests,'$[#-1].selectionBaseSha'),'') NOT IN ('',j.base_sha) OR
		(json_extract(j.tests,'$[#-1].status')='passed' AND COALESCE(json_extract(j.tests,'$[#-1].checkoutSha'),'')<>j.head_sha))) OR
	(j.policy IS NOT NULL AND j.status NOT IN ('queued','reviewing') AND json_array_length(j.tests)=0 AND
		(COALESCE(json_array_length(j.policy,'$.testProfiles'),0)>0 OR COALESCE(json_array_length(j.policy,'$.testCommand'),0)>0 OR
		lower(j.repository) IN (SELECT value FROM json_each(?))))
))`

const inboxPossibleActive = `j.status<>'stale' AND (
	j.status IN ('queued','reviewing','publishing') OR
	(j.policy IS NOT NULL AND (
		json_extract(j.review_stage,'$.status') IN ('queued','running','reviewing','publishing','pending') OR
		json_extract(j.checks,'$.status') IN ('queued','running','reviewing','publishing','pending') OR
		json_extract(j.tests,'$[#-1].status') IN ('queued','running','reviewing','publishing','pending')
	)) OR
	EXISTS (SELECT 1 FROM review_candidates c WHERE ` + inboxCurrentCandidate + ` AND (
		c.status='pending' OR EXISTS (SELECT 1 FROM review_candidate_comparisons s WHERE s.candidate_id=c.id AND s.status IN ('queued','running'))
		OR (c.status='confirmed' AND EXISTS (SELECT 1 FROM review_comparison_scenarios scenario
			JOIN review_candidate_comparisons source ON source.id=scenario.source_comparison_id
			WHERE scenario.account=c.account AND scenario.repository=c.repository AND source.candidate_id=c.id))
	))
)`

// Keep list polling independent of a PR's log sizes and revision history.
const inboxJobFields = `j.account,j.repository,j.number,j.title,j.base_sha,j.head_sha,j.source,j.status,
	j.findings,j.comment_url,j.error,j.attempts,j.updated_at,j.policy,
	json_remove(j.checks,'$.runs'),
	CASE WHEN json_array_length(j.tests)>0 THEN json_array(json_remove(json_extract(j.tests,'$[#-1]'),'$.log','$.command')) ELSE '[]' END,
	COALESCE(j.evidence_key,''),json_remove(j.review_stage,'$.messages','$.authorContext'),
	j.merge_base_sha,j.observed_base_sha,j.equivalent_base_sha,j.manual_review`

type inboxEntry struct {
	job          Job
	rowID        int64
	staleBlocker bool
}

func (s *store) inboxPage(ctx context.Context, view inboxView, cursor string, vmReady func(string) bool) (InboxPage, error) {
	page := InboxPage{Jobs: []Job{}}
	if !view.valid() {
		return page, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return page, err
	}
	page.Account = settings.account
	before, err := inboxBefore(settings.account, view, cursor)
	if err != nil || settings.account == "" {
		return page, err
	}
	entries, err := readInboxEntries(ctx, tx, view, settings, before)
	if err != nil {
		return page, err
	}
	scenarios, err := approvedComparisonScenarios(ctx, tx, settings.account)
	if err != nil {
		return page, err
	}
	for i, entry := range entries {
		if i == inboxScanLimit {
			page.NextCursor = inboxCursor(settings.account, view, entries[i-1].rowID)
			break
		}
		if entry.job.Status != "stale" {
			if err := loadInboxCandidates(ctx, tx, &entry.job, entry.rowID); err != nil {
				return page, err
			}
			jobs := []Job{entry.job}
			annotateReviewJobs(jobs, settings.Config, scenarios, vmReady)
			entry.job = jobs[0]
		}
		group := inboxGroup(entry, settings.policy(entry.job.Repository), view)
		if group == "" {
			continue
		}
		entry.job.InboxGroup = group
		describeReviewQueue(&entry.job, settings.Config)
		page.Jobs = append(page.Jobs, entry.job)
		if len(page.Jobs) == inboxPageSize {
			if i+1 < len(entries) {
				page.NextCursor = inboxCursor(settings.account, view, entry.rowID)
			}
			break
		}
	}
	return page, tx.Commit()
}

func readInboxEntries(ctx context.Context, tx *sql.Tx, view inboxView, settings savedSettings, before int64) ([]inboxEntry, error) {
	args := []any{settings.account, before}
	filter := ""
	switch view {
	case inboxAction:
		watched := []string{}
		for repo, policy := range settings.RepositoryPolicies {
			if inboxHasQuickTest(policy) {
				watched = append(watched, strings.ToLower(repo))
			}
		}
		encoded, _ := json.Marshal(watched)
		filter = " AND (" + inboxPossibleAction + ")"
		args = append(args, string(encoded))
	case inboxActive:
		filter = " AND (" + inboxPossibleActive + ")"
	}
	args = append(args, inboxScanLimit+1)
	rows, err := tx.QueryContext(ctx, "SELECT "+inboxJobFields+",j.rowid,"+inboxStaleBlocker+
		" FROM review_jobs j WHERE j.account=? AND j.rowid<?"+filter+" ORDER BY j.rowid DESC LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []inboxEntry
	for rows.Next() {
		var entry inboxEntry
		entry.job, err = scanJob(rows, &entry.rowID, &entry.staleBlocker)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func loadInboxCandidates(ctx context.Context, tx *sql.Tx, job *Job, rowID int64) error {
	occurrences, err := queryCandidateOccurrences(ctx, tx, *job, rowID)
	if err != nil {
		return err
	}
	positions := make(map[string]int, len(job.Findings))
	for i := range job.Findings {
		positions[candidateID(*job, i)] = i
	}
	inOrder := make([]Candidate, len(job.Findings))
	for _, occurrence := range occurrences {
		if idx, ok := positions[occurrence.ID]; ok {
			inOrder[idx] = Candidate{CandidateOccurrence: occurrence, Index: idx}
		} else if occurrence.OriginID != "" {
			job.Followups = append(job.Followups, Candidate{CandidateOccurrence: occurrence, Index: -1})
		}
	}
	for _, item := range inOrder {
		if item.ID != "" {
			job.Candidates = append(job.Candidates, item)
		}
	}
	return nil
}

func inboxHasQuickTest(policy RepositoryPolicy) bool {
	return len(policy.TestCommand) != 0 || len(policy.TestProfiles) != 0
}

type inboxStage string

func (s inboxStage) active() bool {
	switch s {
	case "queued", "running", "reviewing", "publishing", "pending":
		return true
	}
	return false
}

func (s inboxStage) attention() bool {
	switch s {
	case "completed", "passed", "empty", "not_applicable", "not_configured", "not_run":
		return false
	}
	return !s.active()
}

func inboxJobStages(job Job, configured RepositoryPolicy) (review, checks, test inboxStage) {
	review, checks, test = "missing", "missing", "not_configured"
	switch job.Status {
	case "queued", "reviewing":
		review, checks = inboxStage(job.Status), "pending"
	case "failed":
		review = "failed"
	}
	expectsTest := inboxHasQuickTest(configured)
	if ev := job.Evidence; ev != nil {
		expectsTest = expectsTest || inboxHasQuickTest(ev.Policy)
		if ev.Review.Status != "" {
			review = inboxStage(ev.Review.Status)
		}
		if ev.Checks.Status != "" {
			checks = inboxStage(ev.Checks.Status)
		}
		if ev.Checks.RefreshErrorAt != nil {
			checks = "error"
		}
		if n := len(ev.Tests); n > 0 {
			a := ev.Tests[n-1]
			switch {
			case a.SelectionBaseSHA != "" && a.SelectionBaseSHA != job.BaseSHA:
				test = "stale"
			case a.Status == "passed" && a.CheckoutSHA == "":
				test = "missing"
			case a.Status == "passed" && a.CheckoutSHA != job.HeadSHA:
				test = "error"
			default:
				test = inboxStage(a.Status)
			}
			return review, checks, test
		}
	}
	if expectsTest {
		test = "missing"
		if job.Status == "queued" || job.Status == "reviewing" {
			test = "pending"
		}
	}
	return review, checks, test
}

func inboxGroup(entry inboxEntry, configured RepositoryPolicy, view inboxView) string {
	job := entry.job
	if job.Status == "stale" {
		switch {
		case entry.staleBlocker && view == inboxAction:
			return "action"
		case !entry.staleBlocker && view == inboxHistory:
			return "earlier"
		}
		return ""
	}
	var draft, pending, manual, autoRunning, comparisonActive, comparisonIssue bool
	for _, list := range [][]Candidate{job.Candidates, job.Followups} {
		for _, candidate := range list {
			if d := candidate.CommentDraft; d != nil && !d.Stale && d.ReviewedAt == nil {
				draft = true
			}
			validation := candidate.AutoValidation
			running := validation != nil && (validation.State == "queued" || validation.State == "running")
			autoRunning = autoRunning || running
			if candidate.Status == "pending" && !running {
				pending = true
				manual = manual || validation == nil || validation.State != "verified" || validation.Suggestion == nil
			}
			if n := len(candidate.Comparisons); n > 0 {
				run := candidate.Comparisons[n-1]
				comparisonActive = comparisonActive || run.Status == "queued" || run.Status == "running"
				comparisonIssue = comparisonIssue || run.Status != "completed" || run.Outcome == "inconclusive"
			}
		}
	}
	review, checks, test := inboxJobStages(job, configured)
	needsEvidence := len(job.Candidates)+len(job.Followups) == 0 || manual
	action := draft || pending || (comparisonIssue && !comparisonActive)
	for _, stage := range []inboxStage{review, checks, test} {
		if stage.attention() && (stage != "missing" || needsEvidence && job.Evidence != nil) {
			action = true
		}
	}
	action = action || review == "missing" && needsEvidence
	active := autoRunning || comparisonActive || review.active() || checks.active() || test.active()
	switch {
	case view == inboxAction && action:
		return "action"
	case view == inboxActive && active:
		return "active"
	case view == inboxHistory && !action && !active:
		return "recent"
	}
	return ""
}

func (s *store) inboxJob(ctx context.Context, target Target, vmReady func(string) bool) (ReviewJobView, error) {
	var view ReviewJobView
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return view, err
	}
	defer tx.Rollback()
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return view, err
	}
	if settings.account == "" {
		return view, errReviewJobNotFound
	}
	job, err := readJob(ctx, tx, settings.account, target.Repository, target.Number, target.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) || err == nil && job.BaseSHA != target.BaseSHA {
		return view, errReviewJobNotFound
	}
	if err != nil {
		return view, err
	}
	if err := loadCandidateView(ctx, tx, &job); err != nil {
		return view, err
	}
	scenarios, err := approvedComparisonScenarios(ctx, tx, settings.account)
	if err != nil {
		return view, err
	}
	jobs := []Job{job}
	annotateReviewJobs(jobs, settings.Config, scenarios, vmReady)
	describeReviewQueue(&jobs[0], settings.Config)
	view.Account, view.Job = settings.account, jobs[0]
	return view, tx.Commit()
}
