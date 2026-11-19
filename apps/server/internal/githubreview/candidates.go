package githubreview

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var candidateIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var errCandidateConflict = errors.New("This candidate, its evidence, or its review changed. Reload the review before saving.")

type EvidenceItem struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Status string `json:"status"`
	SHA    string `json:"sha,omitempty"`
	Detail string `json:"detail,omitempty"`
	URL    string `json:"url,omitempty"`
}

type CandidateOccurrence struct {
	Comparisons   []CandidateComparison `json:"comparisons,omitempty"`
	OriginID      string                `json:"originId,omitempty"`
	OriginHeadSHA string                `json:"originHeadSha,omitempty"`
	OriginBaseSHA string                `json:"originBaseSha,omitempty"`
	reviewKey     string
	ID            string `json:"id"`
	TrackID       string `json:"trackId"`
	HeadSHA       string `json:"headSha"`
	BaseSHA       string `json:"baseSha"`
	Finding
	Status   string         `json:"status"`
	Note     string         `json:"note"`
	Revision int            `json:"revision"`
	Evidence []EvidenceItem `json:"evidence"`
	order    int64
}
type Candidate struct {
	CandidateOccurrence
	Index   int                   `json:"index"`
	History []CandidateOccurrence `json:"history"`
}

type candidateDecision struct {
	Account string
	Target
	CandidateID string
	Revision    int
	Status      string
	Note        string
	EvidenceIDs []string
	PreviousID  string
}

func reviewDigest(values ...any) string {
	data, _ := json.Marshal(values)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func candidateID(j Job, index int) string {
	return reviewDigest(strings.ToLower(j.account), strings.ToLower(j.Repository), j.Number, j.HeadSHA, j.BaseSHA, j.evidenceKey, index, j.Findings[index])
}
func findingMatch(f Finding) string {
	return reviewDigest(f.Path, f.Side, strings.ToLower(strings.Join(strings.Fields(f.Title), " ")))
}

func indexCandidates(ctx context.Context, tx *sql.Tx, j Job) error {
	if len(j.Findings) == 0 {
		return nil
	}
	var order int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM review_jobs WHERE account=? AND repository=? AND number=? AND base_sha=? AND head_sha=?`, j.account, j.Repository, j.Number, j.BaseSHA, j.HeadSHA).Scan(&order); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, f := range j.Findings {
		counts[findingMatch(f)]++
	}
	for i, f := range j.Findings {
		id := candidateID(j, i)
		track := id
		key := findingMatch(f)
		// Different line numbers are expected after a push. Only match a unique
		// title at the same path and side with a single historical issue.
		if counts[key] == 1 && f.Path != "" && f.Title != "" {
			var old sql.NullString
			if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN count(DISTINCT track_id)=1 THEN min(track_id) END
             FROM review_candidates WHERE account=? AND repository=? AND number=? AND match_key=? AND
              (job_order<? OR (job_order=? AND (base_sha<>? OR review_key<>?)))`,
				j.account, j.Repository, j.Number, key, order, order, j.BaseSHA, j.evidenceKey).Scan(&old); err != nil {
				return err
			}
			if old.Valid {
				track = old.String
			}
		}
		data, err := json.Marshal(f)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO review_candidates(id,account,repository,number,base_sha,head_sha,review_key,job_order,match_key,track_id,finding)
        VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, id, j.account, j.Repository, j.Number, j.BaseSHA, j.HeadSHA, j.evidenceKey, order, key, track, string(data))
		if err != nil {
			return err
		}
	}
	return nil
}

func candidateOccurrences(ctx context.Context, tx *sql.Tx, j Job) ([]CandidateOccurrence, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.track_id,c.base_sha,c.head_sha,c.finding,CASE WHEN m.resolved=1 THEN 'resolved' ELSE c.status END,c.note,c.revision,c.evidence,c.job_order,
      c.review_key,COALESCE(m.origin_id,''),COALESCE(origin.head_sha,''),COALESCE(origin.base_sha,''),
      (SELECT json_group_array(json_remove(v.result,'$.assertion','$.baseline.log','$.head.log','$.baseline.command','$.head.command') ORDER BY v.rowid) FROM review_candidate_comparisons v WHERE v.candidate_id=c.id)
      FROM review_candidates c LEFT JOIN review_candidate_followups m ON m.id=c.id LEFT JOIN review_candidates origin ON origin.id=m.origin_id
      WHERE c.account=? AND c.repository=? AND c.number=? ORDER BY c.job_order DESC,c.rowid DESC`, j.account, j.Repository, j.Number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CandidateOccurrence
	for rows.Next() {
		var c CandidateOccurrence
		var finding, evidence, comparisons string
		if err := rows.Scan(&c.ID, &c.TrackID, &c.BaseSHA, &c.HeadSHA, &finding, &c.Status, &c.Note, &c.Revision, &evidence, &c.order, &c.reviewKey, &c.OriginID, &c.OriginHeadSHA, &c.OriginBaseSHA, &comparisons); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(finding), &c.Finding); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidence), &c.Evidence); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(comparisons), &c.Comparisons); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadCandidateView(ctx context.Context, tx *sql.Tx, j *Job) error {
	occurrences, err := candidateOccurrences(ctx, tx, *j)
	if err != nil {
		return err
	}
	if len(occurrences) == 0 {
		return nil
	}
	var order int64
	if err := tx.QueryRowContext(ctx, "SELECT rowid FROM review_jobs WHERE account=? AND repository=? AND number=? AND head_sha=?", j.account, j.Repository, j.Number, j.HeadSHA).Scan(&order); err != nil {
		return err
	}
	positions := make(map[string]CandidateOccurrence, len(occurrences))
	for _, c := range occurrences {
		positions[c.ID] = c
	}
	withHistory := func(c CandidateOccurrence, index int) Candidate {
		candidate := Candidate{CandidateOccurrence: c, Index: index, History: []CandidateOccurrence{}}
		for _, past := range occurrences {
			if past.ID != c.ID && past.TrackID == c.TrackID && past.order <= order {
				candidate.History = append(candidate.History, past)
			}
		}
		return candidate
	}
	for i := range j.Findings {
		c, ok := positions[candidateID(*j, i)]
		if !ok {
			return errors.New("Missing local candidate occurrence")
		}
		j.Candidates = append(j.Candidates, withHistory(c, i))
	}
	seenTracks := map[string]bool{}
	for _, c := range occurrences {
		current := c.order == order && c.HeadSHA == j.HeadSHA && c.BaseSHA == j.BaseSHA && c.reviewKey == j.evidenceKey
		if current && c.OriginID != "" {
			j.Followups = append(j.Followups, withHistory(c, -1))
		}
		if !current && c.order <= order && !seenTracks[c.TrackID] {
			j.PreviousCandidates = append(j.PreviousCandidates, c)
			seenTracks[c.TrackID] = true
		}
	}
	if len(j.Candidates) > 0 || len(j.Followups) > 0 {
		j.EvidenceOptions = candidateEvidence(*j)
	}
	return nil
}

func candidateEvidence(j Job) []EvidenceItem {
	var out []EvidenceItem
	add := func(kind, key string, item EvidenceItem) {
		item.Kind = kind
		// Preserve the exact observation if a CI provider reruns or replaces it.
		item.ID = reviewDigest(strings.ToLower(j.account), strings.ToLower(j.Repository), j.Number, j.BaseSHA, j.HeadSHA, j.evidenceKey, key, item)
		out = append(out, item)
	}
	if j.Evidence != nil {
		for _, r := range j.Evidence.Checks.Runs {
			if r.Status != "completed" {
				continue
			}
			add("check", r.Name+" "+r.URL, EvidenceItem{Label: r.Name, Status: r.Conclusion, SHA: r.ExecutionSHA, Detail: r.Scope, URL: r.URL})
		}
		for _, a := range j.Evidence.Tests {
			if a.Status != "passed" && a.Status != "failed" && a.Status != "error" {
				continue
			}
			item := EvidenceItem{Label: fmt.Sprintf("Quick test · attempt %d", a.Attempt), Status: a.Status, SHA: a.CheckoutSHA, Detail: strings.Join(a.Command, " ")}
			if a.TestName != "" {
				item.Label = fmt.Sprintf("%s · attempt %d", a.TestName, a.Attempt)
			}
			add("test", fmt.Sprintf("%d/%s/%s", a.Attempt, a.TaskID, a.AttemptID), item)
		}
	}
	return out
}

func validCandidateNote(note string) bool {
	return len(note) <= 4096 && strings.IndexFunc(note, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	}) < 0
}

func (s *store) saveCandidate(ctx context.Context, r candidateDecision) error {
	if !r.Target.valid() || !githubAccountPattern.MatchString(r.Account) || !candidateIDPattern.MatchString(r.CandidateID) || r.Revision < 1 || !validCandidateNote(r.Note) ||
		r.EvidenceIDs == nil || len(r.EvidenceIDs) > 16 || (r.Status != "pending" && r.Status != "confirmed" && r.Status != "false_positive" && r.Status != "resolved") ||
		(r.PreviousID != "" && !candidateIDPattern.MatchString(r.PreviousID)) {
		return ErrInvalid
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
	if !strings.EqualFold(settings.account, r.Account) {
		return errCandidateConflict
	}
	j, err := readJob(ctx, tx, settings.account, r.Repository, r.Number, r.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return errCandidateConflict
	}
	if err != nil {
		return err
	}
	if j.BaseSHA != r.BaseSHA || (j.Status != "analyzed" && j.Status != "clean" && j.Status != "published" && j.Status != "publishing") {
		return errCandidateConflict
	}
	if err := loadCandidateView(ctx, tx, &j); err != nil {
		return err
	}
	var selected *Candidate
	current := append(j.Candidates, j.Followups...)
	for i := range current {
		if current[i].ID == r.CandidateID {
			selected = &current[i]
			break
		}
	}
	if selected == nil || selected.Revision != r.Revision {
		return errCandidateConflict
	}
	if r.Status == "resolved" && selected.OriginID == "" {
		return ErrInvalid
	}
	available := map[string]EvidenceItem{}
	for _, set := range [][]EvidenceItem{selected.Evidence, j.EvidenceOptions} {
		for _, v := range set {
			available[v.ID] = v
		}
	}
	links := make([]EvidenceItem, 0, len(r.EvidenceIDs))
	for _, id := range r.EvidenceIDs {
		v, ok := available[id]
		if !ok {
			return errCandidateConflict
		}
		links = append(links, v)
		delete(available, id)
	}
	track := selected.TrackID
	if r.PreviousID != "" {
		found := false
		for _, older := range j.PreviousCandidates {
			if older.ID == r.PreviousID {
				track = older.TrackID
				found = true
				break
			}
		}
		if !found {
			return errCandidateConflict
		}
		for _, other := range current {
			if other.ID != selected.ID && other.TrackID == track {
				return errCandidateConflict
			}
		}
	}
	encoded, err := json.Marshal(links)
	if err != nil {
		return err
	}
	status := r.Status
	if selected.OriginID != "" {
		if r.PreviousID != "" {
			return ErrInvalid
		}
		if _, err := tx.ExecContext(ctx, "UPDATE review_candidate_followups SET resolved=? WHERE id=?", r.Status == "resolved", selected.ID); err != nil {
			return err
		}
		if r.Status == "resolved" {
			status = "pending"
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE review_candidates SET status=?,note=?,evidence=?,track_id=?,revision=revision+1 WHERE id=? AND revision=?`, status, strings.TrimSpace(r.Note), string(encoded), track, r.CandidateID, r.Revision)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errCandidateConflict
	}
	return tx.Commit()
}

// Only a local reviewer can carry a previously reported issue into a reviewed
// revision where the model did not report it. Leave the model's findings intact.
func (s *store) followCandidate(ctx context.Context, r candidateDecision) error {
	if !r.Target.valid() || !githubAccountPattern.MatchString(r.Account) || !candidateIDPattern.MatchString(r.PreviousID) {
		return ErrInvalid
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
	if !strings.EqualFold(settings.account, r.Account) {
		return errCandidateConflict
	}
	job, err := readJob(ctx, tx, settings.account, r.Repository, r.Number, r.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return errCandidateConflict
	}
	if err != nil {
		return err
	}
	if job.BaseSHA != r.BaseSHA || (job.Status != "analyzed" && job.Status != "clean" && job.Status != "published") {
		return errCandidateConflict
	}
	if err := loadCandidateView(ctx, tx, &job); err != nil {
		return err
	}
	var prior *CandidateOccurrence
	for i := range job.PreviousCandidates {
		if job.PreviousCandidates[i].ID == r.PreviousID {
			prior = &job.PreviousCandidates[i]
			break
		}
	}
	if prior == nil {
		return errCandidateConflict
	}
	for _, model := range job.Candidates {
		if model.TrackID == prior.TrackID {
			return errCandidateConflict
		}
	}
	for _, manual := range job.Followups {
		if manual.TrackID == prior.TrackID {
			return nil
		}
	}
	id := reviewDigest("followup", strings.ToLower(settings.account), strings.ToLower(job.Repository), job.Number, job.BaseSHA, job.HeadSHA, job.evidenceKey, prior.TrackID)
	entry, err := json.Marshal(prior.Finding)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO review_candidates(id,account,repository,number,base_sha,head_sha,review_key,job_order,match_key,track_id,finding)
         SELECT ?,account,repository,number,base_sha,head_sha,COALESCE(evidence_key,''),rowid,?,?,? FROM review_jobs
         WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=?`, id, findingMatch(prior.Finding), prior.TrackID, string(entry), settings.account, job.Repository, job.Number, job.HeadSHA, job.BaseSHA)
	if err != nil {
		return err
	}
	origin := prior.ID
	if prior.OriginID != "" {
		origin = prior.OriginID
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO review_candidate_followups(id,origin_id) VALUES(?,?)", id, origin); err != nil {
		return err
	}
	return tx.Commit()
}
