package githubreview

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxCommentDraftBytes = 8 << 10

var errCandidateDraftConflict = errors.New("This comment draft or its review changed. Reload the review before saving.")

type CandidateCommentDraft struct {
	Body       string     `json:"body"`
	BaseSHA    string     `json:"baseSha"`
	HeadSHA    string     `json:"headSha"`
	AnchorSHA  string     `json:"anchorSha"`
	Path       string     `json:"path"`
	Line       int        `json:"line"`
	Side       string     `json:"side"`
	Revision   int        `json:"revision"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ReviewedAt *time.Time `json:"reviewedAt,omitempty"`
	Stale      bool       `json:"stale"`
}

type candidateCommentDraftRequest struct {
	Target
	Account     string
	CandidateID string
	Revision    int
	Body        string
}

func canDraftOnJob(j Job) bool {
	return j.Status == "analyzed" || j.Status == "clean" || j.Status == "published"
}

func commentDraftAnchor(j Job, f Finding) string {
	if f.Line < 1 || !validReviewPath(f.Path) {
		return ""
	}
	switch f.Side {
	case "base":
		return j.MergeBaseSHA
	case "", "head":
		return j.HeadSHA
	default:
		return ""
	}
}

func (r candidateCommentDraftRequest) valid() bool {
	return r.Target.valid() && githubAccountPattern.MatchString(r.Account) && candidateIDPattern.MatchString(r.CandidateID) && r.Revision >= 0
}

func (s *store) saveCandidateCommentDraft(ctx context.Context, r candidateCommentDraftRequest, now time.Time) error {
	if !r.valid() {
		return ErrInvalid
	}
	body := strings.ReplaceAll(r.Body, "\r\n", "\n")
	if !utf8.ValidString(body) || len(body) > maxCommentDraftBytes || strings.TrimSpace(body) == "" || strings.IndexFunc(body, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	}) >= 0 {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	anchorSHA, err := currentCandidateDraftAnchor(ctx, tx, r)
	if err != nil {
		return err
	}
	var result sql.Result
	if r.Revision == 0 {
		result, err = insertCandidateCommentDraft(ctx, tx, r.CandidateID, body, anchorSHA, now)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE review_candidate_comment_drafts SET body=?,updated_at=?,revision=revision+1,reviewed_at=NULL
			WHERE candidate_id=? AND revision=? AND anchor_sha=?`, body, now.UnixMilli(), r.CandidateID, r.Revision, anchorSHA)
	}
	return commitCandidateDraft(tx, result, err)
}

func (s *store) setCandidateCommentDraftReviewed(ctx context.Context, r candidateCommentDraftRequest, reviewed bool, now time.Time) error {
	if !r.valid() || r.Revision == 0 {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	anchorSHA, err := currentCandidateDraftAnchor(ctx, tx, r)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE review_candidate_comment_drafts SET reviewed_at=CASE WHEN ? THEN COALESCE(reviewed_at,?) ELSE NULL END
		WHERE candidate_id=? AND revision=? AND anchor_sha=?`, reviewed, now.UnixMilli(), r.CandidateID, r.Revision, anchorSHA)
	return commitCandidateDraft(tx, result, err)
}

func currentCandidateDraftAnchor(ctx context.Context, tx *sql.Tx, r candidateCommentDraftRequest) (string, error) {
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(settings.account, r.Account) {
		return "", errCandidateDraftConflict
	}
	j, err := readJob(ctx, tx, settings.account, r.Repository, r.Number, r.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errCandidateDraftConflict
	}
	if err != nil {
		return "", err
	}
	if j.BaseSHA != r.BaseSHA || !canDraftOnJob(j) {
		return "", errCandidateDraftConflict
	}
	for i, f := range j.Findings {
		if candidateID(j, i) == r.CandidateID {
			if anchorSHA := commentDraftAnchor(j, f); commitSHA.MatchString(anchorSHA) {
				return anchorSHA, nil
			}
			break
		}
	}
	return "", errCandidateDraftConflict
}

func commitCandidateDraft(tx *sql.Tx, result sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errCandidateDraftConflict
	}
	return tx.Commit()
}

func insertCandidateCommentDraft(ctx context.Context, tx *sql.Tx, candidateID, body, anchorSHA string, now time.Time) (sql.Result, error) {
	return tx.ExecContext(ctx, `INSERT INTO review_candidate_comment_drafts(candidate_id,body,anchor_sha,updated_at)
		VALUES(?,?,?,?) ON CONFLICT(candidate_id) DO NOTHING`, candidateID, body, anchorSHA, now.UnixMilli())
}
