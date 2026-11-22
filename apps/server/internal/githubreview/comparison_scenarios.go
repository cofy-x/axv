package githubreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode"
)

type ComparisonScenario struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Path               string `json:"path"`
	Title              string `json:"title"`
	Side               string `json:"side"`
	SourceComparisonID string `json:"sourceComparisonId"`
}

type approveScenarioRequest struct {
	RetryRequest
	Account, CandidateID, ComparisonID, Name string
	Enable                                   bool
}

type approvedScenario struct {
	ComparisonScenario
	matchKey string
	source   CandidateComparison
}

var errScenarioApproval = errors.New("Choose a confirmed candidate with a verified PR-head-only failure and the same saved runtime.")

func (s *Service) approveComparisonScenario(ctx context.Context, q approveScenarioRequest) error {
	name := strings.TrimSpace(q.Name)
	if !q.valid() || !githubAccountPattern.MatchString(q.Account) || !candidateIDPattern.MatchString(q.CandidateID) ||
		!comparisonRequestID.MatchString(q.ComparisonID) || name == "" || len(name) > 200 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return ErrInvalid
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cfg, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	if !strings.EqualFold(cfg.account, q.Account) || cfg.Revision != q.Revision || !cfg.watches(q.Repository) {
		return ErrConflict
	}
	j, err := readJob(ctx, tx, cfg.account, q.Repository, q.Number, q.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRetryStale
	}
	if err != nil {
		return err
	}
	if !exactScenarioJob(j) || j.BaseSHA != q.BaseSHA {
		return ErrRetryStale
	}
	if err := loadCandidateView(ctx, tx, &j); err != nil {
		return err
	}
	c := findScenarioCandidate(j, q.CandidateID)
	if c == nil || c.Status != "confirmed" || !validReviewPath(c.Path) || c.Line < 1 || strings.TrimSpace(c.Title) == "" {
		return errScenarioApproval
	}
	source, err := comparisonByID(ctx, tx, q.ComparisonID)
	if errors.Is(err, sql.ErrNoRows) {
		return errScenarioApproval
	}
	if err != nil {
		return err
	}
	policy := cfg.policy(j.Repository)
	if source.CandidateID != c.ID || source.ScenarioID != "" || !comparisonPairMatches(j, source) || !verifiedScenarioDifferential(source) ||
		!validComparisonAssertion(source.Assertion) || !scenarioRuntimeMatches(policy, source.Runtime) {
		return errScenarioApproval
	}
	var count int
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(source_comparison_id=?),0) FROM review_comparison_scenarios WHERE account=? AND repository=?`, source.ID, j.account, j.Repository).Scan(&count, &exists); err != nil {
		return err
	}
	if count >= 8 && !exists {
		return ErrInvalid
	}
	key := scenarioFindingKey(c.Finding)
	id := reviewDigest("comparison-scenario", strings.ToLower(j.account), strings.ToLower(j.Repository), source.ID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO review_comparison_scenarios(id,account,repository,name,path,title,side,match_key,source_comparison_id)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(account,repository,source_comparison_id) DO NOTHING`, id, j.account, j.Repository, name, c.Path, c.Title, scenarioFindingSide(c.Side), key, source.ID); err != nil {
		return err
	}
	if q.Enable && !policy.AutoCandidateValidation {
		for _, watched := range cfg.Repositories {
			if strings.EqualFold(watched, j.Repository) {
				policy.AutoCandidateValidation = true
				cfg.RepositoryPolicies[watched] = policy
				break
			}
		}
	}
	encoded, err := json.Marshal(cfg.RepositoryPolicies)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE review_settings SET repository_policies=?,revision=revision+1 WHERE singleton=1`, string(encoded)); err != nil {
		return err
	}
	return tx.Commit()
}

type scenarioQuery interface {
	rowQuerier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const approvedScenariosSelect = `SELECT s.id,s.repository,s.name,s.path,s.title,s.side,s.match_key,s.source_comparison_id,json_remove(c.result,'$.baseline.log','$.head.log')
	FROM review_comparison_scenarios s JOIN review_candidate_comparisons c ON c.id=s.source_comparison_id WHERE s.account=?`

func approvedComparisonScenarios(ctx context.Context, db scenarioQuery, account string) (map[string][]approvedScenario, error) {
	return queryApprovedComparisonScenarios(ctx, db, approvedScenariosSelect+" ORDER BY s.rowid", account)
}

func queryApprovedComparisonScenarios(ctx context.Context, db scenarioQuery, query string, args ...any) (map[string][]approvedScenario, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byRepo := map[string][]approvedScenario{}
	for rows.Next() {
		var scenario approvedScenario
		var repository, payload string
		if err := rows.Scan(&scenario.ID, &repository, &scenario.Name, &scenario.Path, &scenario.Title, &scenario.Side, &scenario.matchKey, &scenario.SourceComparisonID, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &scenario.source); err != nil {
			return nil, err
		}
		key := strings.ToLower(repository)
		byRepo[key] = append(byRepo[key], scenario)
	}
	return byRepo, rows.Err()
}

func comparisonByID(ctx context.Context, db rowQuerier, id string) (CandidateComparison, error) {
	var run CandidateComparison
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT result FROM review_candidate_comparisons WHERE id=?`, id).Scan(&raw); err != nil {
		return run, err
	}
	err := json.Unmarshal([]byte(raw), &run)
	return run, err
}

func scenarioFindingSide(side string) string {
	if side == "base" {
		return "base"
	}
	return "head"
}

func scenarioFindingKey(f Finding) string {
	f.Side = scenarioFindingSide(f.Side)
	return findingMatch(f)
}

func findScenarioCandidate(j Job, id string) *Candidate {
	for i := range j.Candidates {
		if j.Candidates[i].ID == id {
			return &j.Candidates[i]
		}
	}
	return nil
}

func scenarioRuntimeMatches(policy RepositoryPolicy, original ComparisonRuntime) bool {
	runtime, ok := policy.comparisonRuntime(original.ID)
	return ok && runtime.Name == original.Name && slices.Equal(runtime.Command, original.Command)
}

func exactScenarioJob(j Job) bool {
	return j.Target.valid() && comparisonEligible(j) && (j.equivalentBaseSHA == "" || j.equivalentBaseSHA == j.BaseSHA) &&
		(j.ObservedBaseSHA == "" || j.ObservedBaseSHA == j.BaseSHA)
}

func comparisonPairMatches(j Job, run CandidateComparison) bool {
	return strings.EqualFold(j.account, run.Account) && strings.EqualFold(j.Repository, run.Repository) && j.Number == run.Number &&
		j.BaseSHA == run.BaseSHA && j.HeadSHA == run.HeadSHA && j.MergeBaseSHA == run.MergeBaseSHA
}

func verifiedScenarioDifferential(run CandidateComparison) bool {
	if run.Status != "completed" || run.Outcome != "head_only_failed" {
		return false
	}
	verified := run
	finishComparison(&verified)
	return verified.Status == "completed" && verified.Outcome == "head_only_failed" &&
		verifiedVMAssertion(run.Baseline.VMTestAttempt) && verifiedVMAssertion(run.Head.VMTestAttempt) &&
		run.Baseline.Binding.VolumeID != "" && run.Baseline.Binding.VolumeRevision > 0 && run.Baseline.Binding.ProjectRevision > 0
}
