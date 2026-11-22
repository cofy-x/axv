package githubreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type CandidateAutoValidation struct {
	State        string                         `json:"state"`
	Reason       string                         `json:"reason"`
	ScenarioID   string                         `json:"scenarioId,omitempty"`
	ScenarioName string                         `json:"scenarioName,omitempty"`
	ComparisonID string                         `json:"comparisonId,omitempty"`
	Reused       bool                           `json:"reused,omitempty"`
	Suggestion   *CandidateValidationSuggestion `json:"suggestion,omitempty"`
}

type CandidateValidationSuggestion struct {
	Status string `json:"status"`
}

func candidateScenario(j Job, c Candidate, policy RepositoryPolicy, approved []approvedScenario) (*approvedScenario, string) {
	if !policy.AutoCandidateValidation || c.OriginID != "" || (c.Status != "pending" && c.Status != "confirmed") {
		return nil, ""
	}
	var selected *approvedScenario
	for i := range approved {
		v := &approved[i]
		if !claimsScenario(c.Finding, *v) || (c.Status == "confirmed" && v.source.CandidateID != c.ID) {
			continue
		}
		if selected != nil {
			return nil, "More than one approved scenario matches this candidate. Choose the intended assertion."
		}
		selected = v
	}
	if selected == nil {
		if c.Status == "pending" {
			if c.VerificationScenarioID != "" {
				return nil, "The suggested scenario is unavailable for this repository, path or diff side. Choose an approved test for this finding."
			}
			return nil, "No approved scenario matches this candidate's path, title and diff side. Choose a test and confirm its assertion."
		}
		return nil, ""
	}
	for _, other := range approved {
		if other.ID != selected.ID && other.matchKey == selected.matchKey {
			return nil, "More than one approved scenario matches this candidate. Choose the intended assertion."
		}
	}
	for _, other := range j.Candidates {
		if other.ID != c.ID && claimsScenario(other.Finding, *selected) {
			return nil, "More than one candidate claims this approved scenario. Choose which one the test addresses."
		}
	}
	if !exactScenarioJob(j) || !commitSHA.MatchString(commentDraftAnchor(j, c.Finding)) {
		return nil, "The comparison or candidate location is unverified. Review the latest commits first."
	}
	if !scenarioRuntimeMatches(policy, selected.source.Runtime) || !validComparisonAssertion(selected.source.Assertion) || !verifiedScenarioDifferential(selected.source) {
		return nil, "The saved runtime or approved receipt changed. Select a verified scenario using the current runtime."
	}
	return selected, ""
}

func claimsScenario(f Finding, scenario approvedScenario) bool {
	if f.VerificationScenarioID != "" {
		side := f.Side
		if side == "" {
			side = "head"
		}
		return f.VerificationScenarioID == scenario.ID && f.Path == scenario.Path && side == scenario.Side
	}
	return scenarioFindingKey(f) == scenario.matchKey
}

// Use the same explicit-reference or legacy-title filter for both the
// scheduled candidate and other claimants from the same immutable review.
func scenarioClaimSQL(alias string) string {
	ref := "COALESCE(json_extract(" + alias + ".finding,'$.verificationScenarioId'),'')"
	side := "COALESCE(NULLIF(json_extract(" + alias + ".finding,'$.side'),''),'head')"
	return "((" + ref + "='' AND " + alias + ".match_key IN (?3,?4)) OR (" + ref + "<>'' AND " + ref + "=?6 AND json_extract(" + alias + ".finding,'$.path')=?7 AND " + side + "=?8))"
}

func annotateCandidateValidations(jobs []Job, cfg *Config, byRepo map[string][]approvedScenario, vmReady func(string) bool) {
	for _, repository := range cfg.Repositories {
		approved := byRepo[strings.ToLower(repository)]
		if len(approved) == 0 {
			continue
		}
		policy := cfg.policy(repository)
		for _, scenario := range approved {
			policy.ComparisonScenarios = append(policy.ComparisonScenarios, scenario.ComparisonScenario)
		}
		cfg.RepositoryPolicies[repository] = policy
	}
	annotateReviewJobs(jobs, *cfg, byRepo, vmReady)
}

func annotateReviewJobs(jobs []Job, cfg Config, byRepo map[string][]approvedScenario, vmReady func(string) bool) {
	for i := range jobs {
		j := &jobs[i]
		policy := cfg.policy(j.Repository)
		approved := byRepo[strings.ToLower(j.Repository)]
		ready := vmReady != nil && vmReady(j.Repository)
		for n := range j.Candidates {
			c := &j.Candidates[n]
			if c.AutoValidation != nil {
				switch {
				case (c.AutoValidation.State == "queued" || c.AutoValidation.State == "running") && !policy.AutoCandidateValidation:
					c.AutoValidation.State = "needs_review"
					c.AutoValidation.Reason = "Automatic validation is disabled. Review the retained comparison before deciding whether to run again."
				case (c.AutoValidation.State == "queued" || c.AutoValidation.State == "running") && c.Status != "pending":
					c.AutoValidation.State = "needs_review"
					c.AutoValidation.Reason = "The candidate's assessment changed. Review the retained comparison before deciding whether to run again."
				case c.AutoValidation.State == "verified" && policy.AutoCandidateValidation:
					scenario, reason := candidateScenario(*j, *c, policy, approved)
					if scenario == nil && reason != "" {
						c.AutoValidation.State = "needs_review"
						c.AutoValidation.Reason = reason
						c.AutoValidation.Suggestion = nil
					}
				}
				continue
			}
			if j.Status == "stale" {
				continue
			}
			scenario, reason := candidateScenario(*j, *c, policy, approved)
			if scenario == nil {
				if reason != "" {
					c.AutoValidation = &CandidateAutoValidation{State: "needs_review", Reason: reason}
				}
				continue
			}
			v := &CandidateAutoValidation{State: "queued", Reason: "Waiting to verify the current commits and VM binding.", ScenarioID: scenario.ID, ScenarioName: scenario.Name}
			if !ready {
				v.State, v.Reason = "needs_review", "Connect the dedicated review VM before using the approved scenario."
			} else if slices.ContainsFunc(c.Comparisons, func(run CandidateComparison) bool { return run.Status == "running" || run.Status == "queued" }) {
				v.State, v.Reason = "running", "The existing comparison is still running. Automatic validation will look at its original receipt when it finishes."
			}
			c.AutoValidation = v
		}
	}
}

type scenarioBindingProvider interface {
	currentReviewBinding(context.Context) (compute.ProjectBinding, error)
}

func (v ComputeVMTests) currentReviewBinding(ctx context.Context) (compute.ProjectBinding, error) {
	if !v.comparisonReady() {
		return compute.ProjectBinding{}, errComparisonVM
	}
	client, err := v.Gateway.ConnectHost(ctx, v.HostID)
	if err != nil {
		return compute.ProjectBinding{}, err
	}
	defer client.Close()
	return v.readProjectBinding(ctx, client)
}

type candidateValidationWork struct {
	job       Job
	candidate Candidate
	scenario  approvedScenario
	policy    savedSettings
}

func (s *store) nextCandidateValidation(ctx context.Context, vmReady func(string) bool) (candidateValidationWork, error) {
	var work candidateValidationWork
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return work, err
	}
	defer tx.Rollback()
	cfg, err := readSettings(ctx, tx)
	if err != nil {
		return work, err
	}
	if cfg.account == "" {
		return work, sql.ErrNoRows
	}
	byRepo, err := approvedComparisonScenarios(ctx, tx, cfg.account)
	if err != nil {
		return work, err
	}
	for _, repo := range cfg.Repositories {
		policy := cfg.policy(repo)
		approved := byRepo[strings.ToLower(repo)]
		if !policy.AutoCandidateValidation || !vmReady(repo) {
			continue
		}
		for _, scenario := range approved {
			var count int
			for _, other := range approved {
				if other.matchKey == scenario.matchKey {
					count++
				}
			}
			if count != 1 || !scenarioRuntimeMatches(policy, scenario.source.Runtime) {
				continue
			}
			legacyKey := scenario.matchKey
			if scenario.Side == "head" {
				legacyKey = findingMatch(Finding{Path: scenario.Path, Title: scenario.Title})
			}
			var id, head string
			var number int
			err := tx.QueryRowContext(ctx, `SELECT c.id,c.head_sha,c.number FROM review_candidates c
				JOIN review_jobs j ON j.account=c.account AND j.repository=c.repository AND j.number=c.number AND j.head_sha=c.head_sha AND j.base_sha=c.base_sha AND j.evidence_key=c.review_key
				LEFT JOIN review_candidate_validations av ON av.candidate_id=c.id
				LEFT JOIN review_candidate_comparisons run ON run.id=av.comparison_id
				WHERE c.account=?1 AND c.repository=?2 AND `+scenarioClaimSQL("c")+` AND (c.status='pending' OR (c.status='confirmed' AND c.id=?5))
				AND j.status IN ('analyzed','clean','published') AND j.merge_base_sha<>''
				AND j.equivalent_base_sha IN ('',j.base_sha) AND j.observed_base_sha IN ('',j.base_sha)
				AND NOT EXISTS (SELECT 1 FROM review_candidate_followups f WHERE f.id=c.id)
				AND NOT EXISTS (SELECT 1 FROM review_candidates other WHERE other.id<>c.id AND other.account=c.account AND other.repository=c.repository AND other.number=c.number AND other.head_sha=c.head_sha AND other.base_sha=c.base_sha AND other.review_key=c.review_key AND `+scenarioClaimSQL("other")+` AND NOT EXISTS (SELECT 1 FROM review_candidate_followups f WHERE f.id=other.id))
				AND (av.candidate_id IS NULL OR (av.state='queued' AND run.status IN ('completed','error','canceled')))
				AND NOT EXISTS (SELECT 1 FROM review_candidate_comparisons running WHERE running.candidate_id=c.id AND running.status IN ('queued','running'))
				ORDER BY (av.candidate_id IS NOT NULL) DESC,j.updated_at,c.rowid LIMIT 1`, cfg.account, repo, scenario.matchKey, legacyKey, scenario.source.CandidateID, scenario.ID, scenario.Path, scenario.Side).Scan(&id, &head, &number)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return work, err
			}
			j, err := readJob(ctx, tx, cfg.account, repo, number, head)
			if err != nil {
				return work, err
			}
			if err := loadCandidateView(ctx, tx, &j); err != nil {
				return work, err
			}
			c := findScenarioCandidate(j, id)
			if c == nil {
				continue
			}
			selected, _ := candidateScenario(j, *c, policy, approved)
			if selected != nil && selected.ID == scenario.ID {
				return candidateValidationWork{j, *c, *selected, cfg}, nil
			}
		}
	}
	return work, sql.ErrNoRows
}

func (s *Service) runCandidateValidations() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for s.ctx.Err() == nil {
		err := s.advanceCandidateValidation(s.ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrRetryStale) && !errors.Is(err, errComparisonBusy) && s.ctx.Err() == nil {
			s.recordError(err)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) advanceCandidateValidation(ctx context.Context) error {
	if _, ok := s.vm.(scenarioBindingProvider); !ok {
		return nil
	}
	if vm, ok := s.vm.(ComputeVMTests); ok && !vm.comparisonReady() && len(vm.RepositoryTargets) == 0 {
		return nil
	}
	w, err := s.store.nextCandidateValidation(ctx, s.scenarioVMReady)
	if err != nil || w.candidate.ID == "" {
		return err
	}
	check, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	current, err := s.github.Current(check, w.job.Target, w.job.account)
	if err != nil {
		return s.finishCandidateValidation(ctx, w, "needs_review", "GitHub could not confirm the current commits. Check PR access and refresh its review.", CandidateComparison{}, false)
	}
	if current.BaseSHA != w.job.BaseSHA || !matchesJob(w.policy.Config, w.job, current) {
		return s.finishCandidateValidation(ctx, w, "needs_review", "The PR changed or is no longer reviewable. Review the latest commits before using this scenario.", CandidateComparison{}, false)
	}
	vm := s.vmForRepository(w.job.Repository).(scenarioBindingProvider)
	bind, err := vm.currentReviewBinding(check)
	if err != nil {
		return s.finishCandidateValidation(ctx, w, "needs_review", "The dedicated VM binding could not be verified. Check the machine, project and guest before retrying.", CandidateComparison{}, false)
	}
	validation := w.candidate.AutoValidation
	if validation != nil && validation.ComparisonID != "" {
		run, err := comparisonByID(ctx, s.store.db, validation.ComparisonID)
		if err != nil {
			return err
		}
		if w.reusable(run, bind) {
			return s.finishCandidateValidation(ctx, w, "verified", "The approved assertion passed on the common ancestor and failed on the PR head. Both original checkouts and the VM binding were verified.", run, false)
		}
		return s.finishCandidateValidation(ctx, w, "needs_review", validationReviewReason(run), run, false)
	}
	for i := len(w.candidate.Comparisons) - 1; i >= 0; i-- {
		if w.candidate.Comparisons[i].Status != "completed" {
			continue
		}
		run, err := comparisonByID(ctx, s.store.db, w.candidate.Comparisons[i].ID)
		if err != nil {
			return err
		}
		if w.reusable(run, bind) {
			return s.finishCandidateValidation(ctx, w, "verified", "Reused a verified common-ancestor and PR-head receipt for the approved assertion and current VM binding.", run, true)
		}
		if w.sameExecution(run, bind) {
			return s.finishCandidateValidation(ctx, w, "needs_review", validationReviewReason(run), run, false)
		}
	}
	if w.reusable(w.scenario.source, bind) {
		return s.finishCandidateValidation(ctx, w, "verified", "Reused the approved receipt for these exact commits, runtime and current VM binding.", w.scenario.source, true)
	}
	if w.candidate.Status != "pending" {
		return s.finishCandidateValidation(ctx, w, "needs_review", "The original receipt does not match the current VM binding. Decide whether to rerun the confirmed scenario.", CandidateComparison{}, false)
	}
	key := reviewDigest("automatic-comparison", w.scenario.ID, w.candidate.ID)
	id := strings.Join([]string{key[:8], key[8:12], key[12:16], key[16:20], key[20:32]}, "-")
	run := newCandidateComparison(w.job, w.candidate.ID, id, w.scenario.source.Runtime, w.scenario.source.Assertion, time.Now().UTC())
	run.ScenarioID = w.scenario.ID
	bindVMTestAttempt(&run.Baseline.VMTestAttempt, bind, comparisonLegKey(&run, "baseline"))
	return s.finishCandidateValidation(ctx, w, "queued", "Queued the approved assertion for both commits on the verified VM binding.", run, false)
}

func (w candidateValidationWork) sameExecution(run CandidateComparison, bind compute.ProjectBinding) bool {
	return comparisonPairMatches(w.job, run) && sourceMatchesRun(w.scenario.source, run) &&
		run.Baseline.Binding != nil && *run.Baseline.Binding == bind && run.Head.Binding != nil && *run.Head.Binding == bind
}

func (w candidateValidationWork) reusable(run CandidateComparison, bind compute.ProjectBinding) bool {
	return w.sameExecution(run, bind) && verifiedScenarioDifferential(run)
}

func validationReviewReason(run CandidateComparison) string {
	switch run.Outcome {
	case "both_passed":
		return "The assertion passed on both commits. Decide whether it exercises the reported behavior."
	case "both_failed":
		return "The assertion failed on both commits. Check the assertion and determine whether the behavior predates the PR."
	case "baseline_only_failed":
		return "Only the common ancestor failed. Check whether the assertion represents the reported regression."
	default:
		return "A verified PR-head-only failure is missing. Inspect the original checkouts, execution exits and VM receipts before deciding."
	}
}

func (s *Service) finishCandidateValidation(ctx context.Context, w candidateValidationWork, state, reason string, run CandidateComparison, reused bool) error {
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cfg, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	if cfg.Revision != w.policy.Revision || !strings.EqualFold(cfg.account, w.job.account) || !cfg.watches(w.job.Repository) {
		return ErrRetryStale
	}
	j, err := readJob(ctx, tx, cfg.account, w.job.Repository, w.job.Number, w.job.HeadSHA)
	if err != nil {
		return err
	}
	if !exactScenarioJob(j) || j.BaseSHA != w.job.BaseSHA || j.evidenceKey != w.job.evidenceKey {
		return ErrRetryStale
	}
	if err := loadCandidateView(ctx, tx, &j); err != nil {
		return err
	}
	c := findScenarioCandidate(j, w.candidate.ID)
	if c == nil || c.Revision != w.candidate.Revision || c.Status != w.candidate.Status || !sameValidation(c.AutoValidation, w.candidate.AutoValidation) {
		return ErrRetryStale
	}
	byRepo, err := approvedComparisonScenarios(ctx, tx, cfg.account)
	if err != nil {
		return err
	}
	selected, _ := candidateScenario(j, *c, cfg.policy(j.Repository), byRepo[strings.ToLower(j.Repository)])
	if selected == nil || selected.ID != w.scenario.ID {
		return ErrRetryStale
	}
	if state == "queued" {
		if err := insertCandidateComparison(ctx, tx, run); err != nil {
			return err
		}
	}
	if state == "verified" && c.VerificationScenarioID != "" && selected.matchKey != scenarioFindingKey(c.Finding) {
		// A head-only failure proves the approved assertion, not the model's
		// interpretation of a differently titled finding in the same file.
		state = "needs_review"
		reason = "The approved assertion failed only on the PR head. Review the attached comparison before deciding whether it proves this finding."
	}
	if run.ID == "" && c.AutoValidation != nil {
		run.ID = c.AutoValidation.ComparisonID
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO review_candidate_validations(candidate_id,scenario_id,comparison_id,state,reason,reused,updated_at)
		VALUES(?,?,NULLIF(?,''),?,?,?,?) ON CONFLICT(candidate_id) DO UPDATE SET comparison_id=excluded.comparison_id,state=excluded.state,reason=excluded.reason,reused=excluded.reused,updated_at=excluded.updated_at`,
		c.ID, selected.ID, run.ID, state, reason, reused, now.UnixMilli())
	if err != nil {
		return err
	}
	if state == "verified" && c.CommentDraft == nil {
		body := fmt.Sprintf("I reproduced the reported behavior using the approved %s scenario. The assertion passed on the common ancestor `%s` and failed on PR head `%s` (exit %d). AXV verified both actual checkouts and ran both sides on the same VM.",
			selected.Name, run.Baseline.CheckoutSHA, run.Head.CheckoutSHA, *run.Head.AssertionExitCode)
		if _, err := insertCandidateCommentDraft(ctx, tx, c.ID, body, commentDraftAnchor(j, c.Finding), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func sameValidation(a, b *CandidateAutoValidation) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.State == b.State && a.ScenarioID == b.ScenarioID && a.ComparisonID == b.ComparisonID
}

func automaticComparisonAllowed(ctx context.Context, tx *sql.Tx, cfg savedSettings, j Job, run CandidateComparison) (bool, error) {
	if !exactScenarioJob(j) {
		return false, nil
	}
	if err := loadCandidateView(ctx, tx, &j); err != nil {
		return false, err
	}
	c := findScenarioCandidate(j, run.CandidateID)
	if c == nil || c.Status != "pending" || c.AutoValidation == nil || c.AutoValidation.ComparisonID != run.ID || c.AutoValidation.ScenarioID != run.ScenarioID {
		return false, nil
	}
	approved, err := approvedComparisonScenarios(ctx, tx, cfg.account)
	if err != nil {
		return false, err
	}
	scenario, _ := candidateScenario(j, *c, cfg.policy(j.Repository), approved[strings.ToLower(j.Repository)])
	return scenario != nil && scenario.ID == run.ScenarioID && sourceMatchesRun(scenario.source, run), nil
}

func sourceMatchesRun(source, run CandidateComparison) bool {
	return source.Assertion == run.Assertion && source.Runtime.ID == run.Runtime.ID && source.Runtime.Name == run.Runtime.Name && slices.Equal(source.Runtime.Command, run.Runtime.Command)
}

func includedValidationComparison(jsonSummary string, c *CandidateOccurrence) error {
	if jsonSummary == "" || c.AutoValidation == nil || c.AutoValidation.ComparisonID == "" || slices.ContainsFunc(c.Comparisons, func(run CandidateComparison) bool { return run.ID == c.AutoValidation.ComparisonID }) {
		return nil
	}
	var run CandidateComparison
	if err := json.Unmarshal([]byte(jsonSummary), &run); err != nil {
		return err
	}
	c.Comparisons = append(c.Comparisons, run)
	return nil
}
