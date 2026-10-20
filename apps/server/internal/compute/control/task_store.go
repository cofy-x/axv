package control

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/diskspace"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func taskRecord(ctx context.Context, q querier, id string) (compute.ProjectTask, error) {
	return decodeRow[compute.ProjectTask](q.QueryRowContext(ctx, "SELECT data FROM project_tasks WHERE id=?", id))
}

func attemptRecord(ctx context.Context, q querier, id string) (compute.TaskAttempt, error) {
	return decodeRow[compute.TaskAttempt](q.QueryRowContext(ctx, "SELECT data FROM task_attempts WHERE id=?", id))
}

func writeAttempt(ctx context.Context, tx *sql.Tx, a compute.TaskAttempt) error {
	_, err := tx.ExecContext(ctx, "UPDATE task_attempts SET status=?,log_bytes=?,cached_bytes=?,updated_at=?,data=? WHERE id=?", a.Status, a.LogBytes, a.CachedBytes, time.Now().UTC().Format(time.RFC3339Nano), encode(a), a.ID)
	return err
}

func (s *Store) taskResult(ctx context.Context, projectID, id, attemptID string) (compute.ProjectTaskResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	defer tx.Rollback()
	t, err := taskRecord(ctx, tx, id)
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	if t.ProjectID != projectID {
		return compute.ProjectTaskResult{}, compute.Problem("not_found", "Project task not found.")
	}
	if attemptID == "" {
		attemptID = t.CurrentAttemptID
	}
	a, err := attemptRecord(ctx, tx, attemptID)
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	if a.TaskID != t.ID {
		return compute.ProjectTaskResult{}, compute.Problem("not_found", "Task attempt not found.")
	}
	attempts, err := rowsJSON[compute.TaskAttempt](tx.QueryContext(ctx, "SELECT data FROM task_attempts WHERE task_id=? ORDER BY json_extract(data,'$.number')", id))
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	return compute.ProjectTaskResult{Task: t, Attempt: a, Attempts: attempts}, tx.Commit()
}

func taskReceipt(ctx context.Context, q querier, projectID, requestID, hash string) (string, string, bool, error) {
	var p, saved, t, a string
	err := q.QueryRowContext(ctx, "SELECT project_id,fingerprint,task_id,attempt_id FROM task_requests WHERE request_id=?", requestID).Scan(&p, &saved, &t, &a)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if p != projectID || (hash != "" && saved != hash) {
		return "", "", false, compute.Problem("idempotency_conflict", "This task request ID belongs to a different project or payload.")
	}
	return t, a, true, nil
}

func (s *Store) taskByRequest(ctx context.Context, projectID, requestID, hash string) (compute.ProjectTaskResult, bool, error) {
	t, a, exists, err := taskReceipt(ctx, s.db, projectID, requestID, hash)
	if err != nil || !exists {
		return compute.ProjectTaskResult{}, exists, err
	}
	result, err := s.taskResult(ctx, projectID, t, a)
	return result, true, err
}

// admitTask reserves bounded lifetime metadata/log space. Receipts are retained;
// quota exhaustion is visible instead of silently pruning recoverable history.
func (s *Store) admitTask(ctx context.Context, projectID, taskID, requestID, hash string, binding compute.ProjectBinding, spec compute.TaskSpec) (compute.ProjectTaskResult, error) {
	var savedTask, savedAttempt string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var exists bool
		var err error
		savedTask, savedAttempt, exists, err = taskReceipt(ctx, tx, projectID, requestID, hash)
		if err != nil || exists {
			return err
		}
		var reused bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM operations WHERE request_id=? UNION ALL SELECT 1 FROM project_requests WHERE request_id=?)", requestID, requestID).Scan(&reused); err != nil {
			return err
		}
		if reused {
			return compute.Problem("idempotency_conflict", "This request ID already belongs to another AXV operation.")
		}
		p, err := project(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if p.VolumeID != binding.VolumeID || p.Revision != binding.ProjectRevision || p.ID != binding.ProjectID || p.HostID != binding.HostID {
			return compute.Problem("binding_changed", "The original project binding changed.")
		}
		_, m, err := projectEnvironment(ctx, tx, binding.VolumeID, binding.MachineID, binding.VolumeRevision)
		if err != nil {
			return err
		}
		if spec.Limits.CPUs > m.Spec.Resources.CPUs || spec.Limits.MemoryMiB > m.Spec.Resources.MemoryMiB {
			return compute.Problem("invalid_request", "Task CPU and memory limits must fit their VM allocation.")
		}
		var count, tasks int
		var reserved int64
		if err := tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(SUM(reserved_bytes),0) FROM task_attempts").Scan(&count, &reserved); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM project_tasks").Scan(&tasks); err != nil {
			return err
		}
		var metadata int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(length(CAST(data AS BLOB))),0) FROM project_tasks").Scan(&metadata); err != nil {
			return err
		}
		// Root, final reason and synchronization error can each expand to 24 KiB
		// under JSON escaping. Reserve all three plus bounded identities/status
		// overhead. List responses duplicate the current attempt; keeping this
		// budget below 8 MiB fits the 16 MiB client cap.
		metadata += int64(count+1) * (96 << 10)
		if taskID == "" {
			metadata += int64(len(encode(spec))) + 2048
		}
		if metadata > 8<<20 {
			return compute.Problem("capacity_exceeded", "The task metadata reservation is full (8 MiB). Existing receipts were preserved.")
		}
		if count >= 1024 || (taskID == "" && tasks >= 256) || reserved+spec.Limits.MaxLogBytes > 256<<20 {
			return compute.Problem("capacity_exceeded", "The task history or reserved log quota is full (256 tasks, 1024 attempts, 256 MiB logs). Preserve the existing history before an explicit store reset.")
		}
		if err := diskspace.Require(s.directory, spec.Limits.MaxLogBytes*2); err != nil {
			return compute.Problem("disk_full", "Task log storage cannot be reserved: %v", err)
		}
		now := time.Now().UTC()
		a := compute.TaskAttempt{ID: compute.NewID("attempt"), TaskID: taskID, Number: 1, Binding: binding, Root: p.Root, RootIdentity: p.RootIdentity, RequestID: requestID, Status: "accepted", CreatedAt: now}
		var t compute.ProjectTask
		if taskID == "" {
			t = compute.ProjectTask{ID: compute.NewID("task"), HostID: s.HostID, ProjectID: projectID, RequestID: requestID, Spec: spec, CurrentAttemptID: a.ID, CreatedAt: now}
			a.TaskID = t.ID
			if _, err := tx.ExecContext(ctx, "INSERT INTO project_tasks(id,project_id,data) VALUES(?,?,?)", t.ID, projectID, encode(t)); err != nil {
				return err
			}
		} else {
			t, err = taskRecord(ctx, tx, taskID)
			if err != nil {
				return err
			}
			if t.ProjectID != projectID {
				return compute.Problem("not_found", "Project task not found.")
			}
			previous, err := attemptRecord(ctx, tx, t.CurrentAttemptID)
			if err != nil {
				return err
			}
			if !compute.TaskFinal(previous.Status) {
				return compute.Problem("task_conflict", "The original attempt is active or uncertain. Reconcile it before retrying.")
			}
			a.Number = previous.Number + 1
			t.CurrentAttemptID = a.ID
			if _, err := tx.ExecContext(ctx, "UPDATE project_tasks SET data=? WHERE id=?", encode(t), t.ID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO task_attempts(id,task_id,status,reserved_bytes,updated_at,data) VALUES(?,?,?,?,?,?)", a.ID, t.ID, a.Status, spec.Limits.MaxLogBytes, now.Format(time.RFC3339Nano), encode(a)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO task_requests(request_id,project_id,fingerprint,task_id,attempt_id) VALUES(?,?,?,?,?)", requestID, projectID, hash, t.ID, a.ID); err != nil {
			return err
		}
		savedTask, savedAttempt = t.ID, a.ID
		return nil
	})
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	return s.taskResult(ctx, projectID, savedTask, savedAttempt)
}

func (s *Store) claimTask(ctx context.Context, id string) (compute.TaskAttempt, bool, error) {
	var a compute.TaskAttempt
	claimed := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		a, err = attemptRecord(ctx, tx, id)
		if err != nil || a.Status != "accepted" {
			return err
		}
		if a.Cancel {
			now := time.Now().UTC()
			a.Status, a.FinishedAt = "cancelled", &now
			a.Reason = "Cancelled before guest dispatch."
		} else {
			a.Status = "dispatching"
			claimed = true
		}
		return writeAttempt(ctx, tx, a)
	})
	return a, claimed, err
}

func (s *Store) saveTaskFailure(ctx context.Context, id, status, reason string) error {
	// Transport/provider errors are untrusted input too. Preserve useful text
	// while keeping durable metadata and plain-text interfaces bounded.
	reason = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(reason, "�"))
	if len(reason) > 4096 {
		reason = reason[:4096]
		for !utf8.ValidString(reason) {
			reason = reason[:len(reason)-1]
		}
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		a, err := attemptRecord(ctx, tx, id)
		if err != nil {
			return err
		}
		if compute.TaskFinal(a.Status) {
			a.SyncError = reason
			return writeAttempt(ctx, tx, a)
		}
		a.Status, a.Reason = status, reason
		if compute.TaskFinal(status) {
			now := time.Now().UTC()
			a.FinishedAt = &now
		}
		return writeAttempt(ctx, tx, a)
	})
}

func (s *Store) saveTaskExecution(ctx context.Context, id string, e protocol.TaskExecution, chunk *protocol.TaskLog) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		a, err := attemptRecord(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := validateFinalTaskLog(a, e); err != nil {
			return err
		}
		// A verified terminal result is immutable; later stale observations or
		// cancellation acknowledgements cannot resurrect it.
		if !compute.TaskFinal(a.Status) {
			a.Status, a.Reason, a.ExitCode = e.Status, e.Reason, e.ExitCode
			if a.Status == "accepted" {
				a.Status = "dispatching"
			}
			a.StartedAt, a.FinishedAt = e.StartedAt, e.FinishedAt
			if a.Cancel && !compute.TaskFinal(a.Status) {
				a.Status = "cancelling"
			}
		}
		a.LogBytes, a.LogLimited = e.LogBytes, e.LogLimited
		a.GuestFinal = a.GuestFinal || compute.TaskFinal(e.Status)
		a.SyncError = ""
		if chunk != nil {
			if err := validateTaskLog(a, e, *chunk); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE task_attempts SET log=CAST(log || ? AS BLOB) WHERE id=?", append([]byte{}, chunk.Data...), a.ID); err != nil {
				return err
			}
			a.CachedBytes = chunk.NextOffset
		}
		return writeAttempt(ctx, tx, a)
	})
}

func (s *Store) cancelTask(ctx context.Context, projectID, taskID, attemptID string, b compute.ProjectBinding) (compute.ProjectTaskResult, error) {
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		t, err := taskRecord(ctx, tx, taskID)
		if err != nil {
			return err
		}
		a, err := attemptRecord(ctx, tx, attemptID)
		if err != nil {
			return err
		}
		if t.ProjectID != projectID || a.TaskID != taskID {
			return compute.Problem("not_found", "Task attempt not found.")
		}
		if a.Binding != b {
			return compute.Problem("binding_changed", "Cancellation requires the original attempt binding; it was not redirected.")
		}
		if compute.TaskFinal(a.Status) {
			return nil
		}
		a.Cancel = true
		if a.Status != "accepted" {
			a.Status = "cancelling"
		}
		return writeAttempt(ctx, tx, a)
	})
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	return s.taskResult(ctx, projectID, taskID, attemptID)
}
