package githubreview

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func randomNotificationStoreID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (s *store) notificationStoreID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT store_id FROM review_notification_store WHERE singleton=1").Scan(&id)
	return id, err
}

func recordReviewNotification(ctx context.Context, tx *sql.Tx, job Job, observedAt time.Time) error {
	if job.Status != "analyzed" || len(job.Findings) == 0 || job.evidenceKey == "" {
		return nil
	}
	eventID := reviewDigest("review-notification-v1", job.account, job.Repository, job.Number, job.BaseSHA, job.HeadSHA, job.evidenceKey)[:32]
	_, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO review_notification_events
			(event_id, account, repository, number, base_sha, head_sha, observed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, eventID, job.account, job.Repository, job.Number, job.BaseSHA, job.HeadSHA, observedAt.UTC().UnixMilli())
	return err
}

// NotificationSources exposes content-free GitHub Review finding observations
// for the shared workstation notification intake.
func (s *Service) NotificationSources(ctx context.Context, cursors map[string]uint64) ([]notificationevent.Batch, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	storeID, err := s.store.notificationStoreID(ctx)
	if err != nil {
		return nil, err
	}
	sourceID := "github-review:" + storeID
	after := cursors[sourceID]
	var maxSeq uint64
	if err := s.store.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM review_notification_events").Scan(&maxSeq); err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `
		SELECT seq, event_id, repository, number, observed_at
		FROM review_notification_events
		WHERE seq > ?
		ORDER BY seq ASC
		LIMIT 256
	`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []notificationevent.Event{}
	var first, next uint64
	next = after
	if after > maxSeq {
		// Never move the cursor backwards if the DB was truncated unexpectedly.
		next = maxSeq
	}
	for rows.Next() {
		var seq uint64
		var eventID, repository string
		var number int
		var observedAt int64
		if err := rows.Scan(&seq, &eventID, &repository, &number, &observedAt); err != nil {
			return nil, err
		}
		if first == 0 {
			first = seq
		}
		next = seq
		events = append(events, notificationevent.Event{
			ID:         eventID,
			Sequence:   seq,
			ObservedAt: time.UnixMilli(observedAt).UTC(),
			State:      "findings",
			Target: notificationevent.Target{
				Kind:        "github_review",
				StoreID:     storeID,
				EventID:     eventID,
				Repository:  repository,
				PullRequest: number,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		next = maxSeq
	}
	return []notificationevent.Batch{{
		SourceID: sourceID,
		StoreID:  storeID,
		First:    first,
		Next:     next,
		Events:   events,
	}}, nil
}

// ResolveNotification checks whether the recorded GitHub Review finding event
// still exists and whether that exact review revision remains current.
func (s *Service) ResolveNotification(ctx context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
	resolution := notificationevent.Resolution{Target: target}
	if s == nil || s.store == nil || target.Kind != "github_review" || target.EventID == "" {
		return resolution, nil
	}
	storeID, err := s.store.notificationStoreID(ctx)
	if err != nil {
		return resolution, err
	}
	if target.StoreID != storeID {
		return resolution, nil
	}
	var account, repository, baseSHA, headSHA string
	var number int
	err = s.store.db.QueryRowContext(ctx, `
		SELECT account, repository, number, base_sha, head_sha
		FROM review_notification_events
		WHERE event_id = ?
	`, target.EventID).Scan(&account, &repository, &number, &baseSHA, &headSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return resolution, nil
	}
	if err != nil {
		return resolution, err
	}
	job, err := readExactJob(ctx, s.store.db, account, Target{
		Repository: repository,
		Number:     number,
		BaseSHA:    baseSHA,
		HeadSHA:    headSHA,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return resolution, nil
	}
	if err != nil {
		return resolution, err
	}
	resolution.Available = true
	resolution.Current = job.Status == "analyzed"
	resolution.State = "findings"
	return resolution, nil
}
