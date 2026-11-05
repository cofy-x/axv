package control

import (
	"context"
	"database/sql"
	"math"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

const taskNotificationRetention = 2048
const taskNotificationBatchSize = 256

func appendTaskNotification(ctx context.Context, tx *sql.Tx, attempt compute.TaskAttempt) error {
	if attempt.Status != "unknown" && !compute.TaskFinal(attempt.Status) {
		return nil
	}
	task, err := taskRecord(ctx, tx, attempt.TaskID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_notification_events(id,observed_at,state,project_id,task_id,attempt_id)
VALUES(?,?,?,?,?,?)`, compute.NewID("task-event"), time.Now().UTC().Format(time.RFC3339Nano), attempt.Status, task.ProjectID, task.ID, attempt.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM task_notification_events WHERE sequence NOT IN
(SELECT sequence FROM task_notification_events ORDER BY sequence DESC LIMIT ?)`, taskNotificationRetention)
	return err
}

// NotificationEvents reads one consistent, bounded owner journal snapshot. The
// desktop checkpoints only after durably importing all returned events.
func (s *Service) NotificationEvents(ctx context.Context, after uint64, hostID string) (notificationevent.Batch, error) {
	if err := s.requireProjectHost(hostID); err != nil {
		return notificationevent.Batch{}, err
	}
	if after > math.MaxInt64 {
		return notificationevent.Batch{}, compute.Problem("invalid_request", "The task event cursor is outside its supported range.")
	}
	tx, err := s.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return notificationevent.Batch{}, err
	}
	defer tx.Rollback()
	batch := notificationevent.Batch{SourceID: "compute:" + s.store.HostID, StoreID: s.store.HostID, Events: []notificationevent.Event{}}
	var latest uint64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MIN(sequence),0),COALESCE(MAX(sequence),0) FROM task_notification_events").Scan(&batch.First, &latest); err != nil {
		return notificationevent.Batch{}, err
	}
	batch.Gap = batch.First > 0 && after < batch.First-1
	rows, err := tx.QueryContext(ctx, `SELECT sequence,id,observed_at,state,project_id,task_id,attempt_id
FROM task_notification_events WHERE sequence>? ORDER BY sequence LIMIT ?`, after, taskNotificationBatchSize)
	if err != nil {
		return notificationevent.Batch{}, err
	}
	for rows.Next() {
		var event notificationevent.Event
		var observed string
		if err := rows.Scan(&event.Sequence, &event.ID, &observed, &event.State, &event.Target.ProjectID, &event.Target.TaskID, &event.Target.AttemptID); err != nil {
			rows.Close()
			return notificationevent.Batch{}, err
		}
		if event.ObservedAt, err = time.Parse(time.RFC3339Nano, observed); err != nil {
			rows.Close()
			return notificationevent.Batch{}, err
		}
		event.Target.Kind, event.Target.StoreID, event.Target.HostID, event.Target.EventID = "task", s.store.HostID, s.store.HostID, event.ID
		batch.Events = append(batch.Events, event)
		batch.Next = event.Sequence
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return notificationevent.Batch{}, err
	}
	if err := rows.Close(); err != nil {
		return notificationevent.Batch{}, err
	}
	if len(batch.Events) == 0 {
		batch.Next = latest
	}
	return batch, tx.Commit()
}

// ResolveNotification inspects the original recorded attempt without contacting
// a guest or substituting the task's latest retry.
func (s *Service) ResolveNotification(ctx context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
	result := notificationevent.Resolution{Target: target}
	if target.Kind != "task" || target.StoreID != s.store.HostID || target.HostID != s.store.HostID {
		return result, compute.Problem("binding_changed", "The notification belongs to another task store or host.")
	}
	for _, id := range []string{target.ProjectID, target.TaskID, target.AttemptID} {
		if id == "" || len(id) > 128 || !utf8.ValidString(id) {
			return result, compute.Problem("invalid_request", "The original project, task and attempt identities are required.")
		}
	}
	read, err := s.store.taskResult(ctx, target.ProjectID, target.TaskID, target.AttemptID)
	if compute.ErrorCode(err) == "not_found" {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Available = true
	result.Current = read.Task.CurrentAttemptID == target.AttemptID
	result.State = read.Attempt.Status
	return result, nil
}
