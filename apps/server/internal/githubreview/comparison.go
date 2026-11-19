package githubreview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// A base branch can advance without changing the three-dot diff. Keep the raw
// base of the original review and cache only equivalence verified for this head.
func (s *Service) observeComparison(ctx context.Context, account string, target Target, now time.Time) error {
	var base, merge, equivalent, observed string
	err := s.store.db.QueryRowContext(ctx, `SELECT base_sha,merge_base_sha,equivalent_base_sha,observed_base_sha FROM review_jobs
 WHERE account=? AND repository=? AND number=? AND head_sha=? AND (policy IS NOT NULL OR status IN ('analyzed','clean','published'))`,
		account, target.Repository, target.Number, target.HeadSHA).Scan(&base, &merge, &equivalent, &observed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if (target.BaseSHA == base || target.BaseSHA == equivalent) && observed == "" {
		return nil
	}
	reader, ok := s.github.(interface {
		MergeBase(context.Context, string, Target) (string, error)
	})
	same := target.BaseSHA == base
	if !same && ok {
		if merge == "" {
			old := target
			old.BaseSHA = base
			merge, err = reader.MergeBase(ctx, account, old)
		}
		if err == nil {
			var latest string
			latest, err = reader.MergeBase(ctx, account, target)
			same = err == nil && commitSHA.MatchString(merge) && latest == merge
		}
	}
	if same {
		// Restore only completed work. An interrupted review or publication
		// needs an explicit retry even if its comparison later matches again.
		_, err = s.store.db.ExecContext(ctx, `UPDATE review_jobs SET merge_base_sha=?,equivalent_base_sha=?,
 status=CASE WHEN status='stale' AND comparison_saved_status<>'' THEN comparison_saved_status ELSE status END,
 error=CASE WHEN status='stale' AND comparison_saved_status<>'' THEN '' ELSE error END, comparison_saved_status='',
 observed_base_sha=CASE WHEN status<>'stale' OR comparison_saved_status<>'' THEN '' ELSE observed_base_sha END
 WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=? AND observed_base_sha=?`, merge, target.BaseSHA, account, target.Repository, target.Number, target.HeadSHA, base, observed)
		return err
	}
	reason := "The comparison base changed. Review this head again against the current target."
	if err != nil || !ok {
		reason = "The target changed and its comparison base could not be verified. Review this head again."
	}
	_, saveErr := s.store.db.ExecContext(s.ctx, `UPDATE review_jobs SET comparison_saved_status=CASE WHEN status IN ('clean','analyzed','published') THEN status ELSE comparison_saved_status END,status='stale',error=?,updated_at=?,observed_base_sha=?,equivalent_base_sha='',
 merge_base_sha=CASE WHEN merge_base_sha='' THEN ? ELSE merge_base_sha END
 WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=? AND (observed_base_sha<>? OR status<>'stale')`,
		reason, now.UnixMilli(), target.BaseSHA, merge, account, target.Repository, target.Number, target.HeadSHA, base, target.BaseSHA)
	return errors.Join(err, saveErr)
}

func (g GitHubCLI) MergeBase(ctx context.Context, account string, t Target) (string, error) {
	if !t.valid() {
		return "", ErrInvalid
	}
	token, err := g.Token(ctx, account)
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("repos/%s/compare/%s...%s?per_page=1", t.Repository, t.BaseSHA, t.HeadSHA)
	var pair struct {
		Base  string
		Merge string
	}
	if err = g.api(ctx, token, &pair, nil, endpoint, "--jq", `{base:.base_commit.sha,merge:.merge_base_commit.sha}`); err != nil {
		return "", err
	}
	if pair.Base != t.BaseSHA || !commitSHA.MatchString(pair.Merge) {
		return "", errors.New("GitHub returned an inconsistent comparison base.")
	}
	return pair.Merge, nil
}
