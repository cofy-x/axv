package githubreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var comparisonRequestID = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)

var errComparisonBusy = errors.New("This candidate already has a comparison running.")
var errComparisonNotFound = errors.New("This comparison is unavailable for the connected GitHub account.")
var errComparisonVM = errors.New("Configure a dedicated review VM before running a comparison.")

// Command is a trusted operator-selected interpreter prefix. The confirmed
// assertion is a single final argument, without interpolation or tokenization.
type ComparisonRuntime struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Command []string `json:"command"`
}

func (r *ComparisonRuntime) UnmarshalJSON(data []byte) error {
	return decodeFields(data, map[string]any{"id": &r.ID, "name": &r.Name, "command": &r.Command})
}

func validComparisonRuntimes(runtimes []ComparisonRuntime) bool {
	if len(runtimes) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, runtime := range runtimes {
		if !testProfileID.MatchString(runtime.ID) || seen[runtime.ID] || strings.TrimSpace(runtime.Name) == "" || len(runtime.Name) > 80 || strings.IndexFunc(runtime.Name, unicode.IsControl) >= 0 || len(runtime.Command) == 0 || !validTestCommand(runtime.Command) {
			return false
		}
		seen[runtime.ID] = true
	}
	return true
}

func (p RepositoryPolicy) comparisonRuntime(id string) (ComparisonRuntime, bool) {
	if len(p.ComparisonRuntimes) == 0 && id == "shell" {
		return ComparisonRuntime{ID: "shell", Name: "POSIX shell", Command: []string{"/bin/sh", "-ec"}}, true
	}
	for _, v := range p.ComparisonRuntimes {
		if v.ID == id {
			return v, true
		}
	}
	return ComparisonRuntime{}, false
}

type ComparisonLeg struct {
	TargetSHA string `json:"targetSha"`
	VMTestAttempt
}

type CandidateComparison struct {
	ScenarioID   string            `json:"scenarioId,omitempty"`
	ID           string            `json:"id"`
	CandidateID  string            `json:"candidateId"`
	Account      string            `json:"account"`
	Repository   string            `json:"repository"`
	Number       int               `json:"number"`
	BaseSHA      string            `json:"baseSha"`
	MergeBaseSHA string            `json:"mergeBaseSha"`
	HeadSHA      string            `json:"headSha"`
	Runtime      ComparisonRuntime `json:"runtime"`
	Assertion    string            `json:"assertion,omitempty"`
	Status       string            `json:"status"`
	Outcome      string            `json:"outcome,omitempty"`
	Error        string            `json:"error,omitempty"`
	Baseline     ComparisonLeg     `json:"baseline"`
	Head         ComparisonLeg     `json:"head"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	version      int
}

type comparisonRequest struct {
	RetryRequest
	Account, CandidateID, RequestID, RuntimeID, Assertion string
}

func (q comparisonRequest) validComparison() bool {
	if !q.valid() || !githubAccountPattern.MatchString(q.Account) || !candidateIDPattern.MatchString(q.CandidateID) || !comparisonRequestID.MatchString(q.RequestID) || !testProfileID.MatchString(q.RuntimeID) {
		return false
	}
	return validComparisonAssertion(q.Assertion)
}

func validComparisonAssertion(assertion string) bool {
	return utf8.ValidString(assertion) && len(assertion) <= 8192 && strings.TrimSpace(assertion) != "" && strings.IndexFunc(assertion, func(r rune) bool { return unicode.IsControl(r) && r != '\t' && r != '\r' && r != '\n' }) < 0
}

func (v ComputeVMTests) comparisonReady() bool {
	return v.Gateway != nil && v.HostID != "" && v.MachineID != "" && v.ProjectID != ""
}

func (s *Service) comparisonReady() bool {
	return reviewVMConfigured(s.vm)
}

func comparisonReplay(ctx context.Context, db rowQuerier, q comparisonRequest) (bool, error) {
	var same bool
	err := db.QueryRowContext(ctx, `SELECT c.account=? AND c.repository=? AND c.number=? AND c.base_sha=? AND c.head_sha=? AND v.candidate_id=?
  AND json_extract(v.result,'$.runtime.id')=? AND json_extract(v.result,'$.assertion')=?
  FROM review_candidate_comparisons v JOIN review_candidates c ON c.id=v.candidate_id WHERE v.id=?`,
		q.Account, q.Repository, q.Number, q.BaseSHA, q.HeadSHA, q.CandidateID, q.RuntimeID, q.Assertion, q.RequestID).Scan(&same)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err == nil && !same {
		return false, ErrConflict
	}
	return same, err
}

func candidateOnComparison(ctx context.Context, db rowQuerier, j Job, candidateID string) (bool, error) {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM review_candidates WHERE id=? AND account=? AND repository=? AND number=? AND base_sha=? AND head_sha=? AND review_key=?)`, candidateID, j.account, j.Repository, j.Number, j.BaseSHA, j.HeadSHA, j.evidenceKey).Scan(&ok)
	return ok, err
}

func (s *Service) requestComparison(ctx context.Context, q comparisonRequest) error {
	if !q.validComparison() {
		return ErrInvalid
	}
	cfg, err := s.store.settings(ctx)
	if err != nil {
		return err
	}
	if !strings.EqualFold(q.Account, cfg.account) {
		return ErrConflict
	}
	if replay, err := comparisonReplay(ctx, s.store.db, q); err != nil || replay {
		return err
	}
	if cfg.Revision != q.Revision || !cfg.watches(q.Repository) {
		return ErrConflict
	}
	if !reviewVMConfigured(s.vmForRepository(q.Repository)) {
		return errComparisonVM
	}
	target := Target{Repository: q.Repository, Number: q.Number, BaseSHA: q.BaseSHA, HeadSHA: q.HeadSHA}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	current, err := s.github.Current(check, target, cfg.account)
	if err != nil {
		return err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cfg, err = readSettings(ctx, tx)
	if err != nil {
		return err
	}
	if !strings.EqualFold(q.Account, cfg.account) {
		return ErrConflict
	}
	if replay, err := comparisonReplay(ctx, tx, q); err != nil || replay {
		return err
	}
	if cfg.Revision != q.Revision || !cfg.watches(q.Repository) {
		return ErrConflict
	}
	runtime, ok := cfg.policy(q.Repository).comparisonRuntime(q.RuntimeID)
	if !ok {
		return ErrConflict
	}
	j, err := readJob(ctx, tx, cfg.account, q.Repository, q.Number, q.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRetryStale
	}
	if err != nil {
		return err
	}
	if !comparisonEligible(j) || j.BaseSHA != q.BaseSHA || !matchesJob(cfg.Config, j, current) {
		return ErrRetryStale
	}
	owned, err := candidateOnComparison(ctx, tx, j, q.CandidateID)
	if err != nil {
		return err
	}
	if !owned {
		return errCandidateConflict
	}
	run := newCandidateComparison(j, q.CandidateID, q.RequestID, runtime, q.Assertion, time.Now().UTC())
	if err := insertCandidateComparison(ctx, tx, run); err != nil {
		return err
	}
	return tx.Commit()
}

func newCandidateComparison(j Job, candidateID, id string, runtime ComparisonRuntime, assertion string, now time.Time) CandidateComparison {
	leg := func(sha string) ComparisonLeg {
		return ComparisonLeg{TargetSHA: sha, VMTestAttempt: VMTestAttempt{Status: "queued", UpdatedAt: now}}
	}
	return CandidateComparison{ID: id, CandidateID: candidateID, Account: j.account, Repository: j.Repository, Number: j.Number, BaseSHA: j.BaseSHA, MergeBaseSHA: j.MergeBaseSHA, HeadSHA: j.HeadSHA,
		Runtime: runtime, Assertion: assertion, CreatedAt: now, UpdatedAt: now, Status: "queued", Baseline: leg(j.MergeBaseSHA), Head: leg(j.HeadSHA)}
}

func insertCandidateComparison(ctx context.Context, tx *sql.Tx, run CandidateComparison) error {
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO review_candidate_comparisons(id,candidate_id,status,updated_at,result) VALUES(?,?,'queued',?,?) ON CONFLICT DO NOTHING`, run.ID, run.CandidateID, run.CreatedAt.UnixMilli(), string(payload))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errComparisonBusy
	}
	return nil
}

func comparisonEligible(j Job) bool {
	return (j.Status == "analyzed" || j.Status == "published" || j.Status == "clean") && j.Evidence != nil && j.evidenceKey != "" && commitSHA.MatchString(j.MergeBaseSHA)
}

func (s *store) nextComparison(ctx context.Context) (CandidateComparison, error) {
	var v CandidateComparison
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT result,version FROM review_candidate_comparisons WHERE status IN ('queued','running') ORDER BY updated_at,rowid LIMIT 1`).Scan(&raw, &v.version)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}

func (s *store) saveComparison(ctx context.Context, previous, next CandidateComparison) error {
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	r, err := s.db.ExecContext(ctx, `UPDATE review_candidate_comparisons SET status=?,updated_at=?,result=?,version=version+1 WHERE id=? AND version=? AND status IN ('queued','running')`, next.Status, next.UpdatedAt.UnixMilli(), string(payload), previous.ID, previous.version)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}

func (s *store) comparisonCurrent(ctx context.Context, v CandidateComparison) (bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	cfg, err := readSettings(ctx, tx)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(cfg.account, v.Account) || !cfg.watches(v.Repository) {
		return false, nil
	}
	j, err := readJob(ctx, tx, v.Account, v.Repository, v.Number, v.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !comparisonEligible(j) || j.BaseSHA != v.BaseSHA || j.MergeBaseSHA != v.MergeBaseSHA {
		return false, nil
	}
	if v.ScenarioID != "" {
		return automaticComparisonAllowed(ctx, tx, cfg, j, v)
	}
	return candidateOnComparison(ctx, tx, j, v.CandidateID)
}

// Keep logs and runnable assertion text out of the polling snapshot. The reader
// can open retained occurrences for the locally enrolled account. A known CLI
// disconnect or account switch takes effect without probing GitHub on a read.
func (s *Service) comparisonDetail(ctx context.Context, id string) (CandidateComparison, error) {
	var v CandidateComparison
	if !comparisonRequestID.MatchString(id) {
		return v, ErrInvalid
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	observation, known := s.observed["github"]
	view := observation.value
	if known && (!view.Connected || view.Account == "") {
		return v, errComparisonNotFound
	}
	var data string
	err := s.store.db.QueryRowContext(ctx, `SELECT v.result FROM review_candidate_comparisons v
 JOIN review_candidates c ON c.id=v.candidate_id
 JOIN review_settings settings ON settings.singleton=1 AND settings.github_account=c.account COLLATE NOCASE
 WHERE v.id=? AND (?='' OR c.account=?)`, id, view.Account, view.Account).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return v, errComparisonNotFound
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(data), &v)
	return v, err
}
