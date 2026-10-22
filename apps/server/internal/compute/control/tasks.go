package control

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func validateTaskBinding(projectID string, b compute.ProjectBinding) error {
	for _, value := range []string{projectID, b.HostID, b.ProjectID, b.VolumeID, b.MachineID, b.BootID, b.InstanceID} {
		if !utf8.ValidString(value) || len(value) > 128 {
			return compute.Problem("invalid_request", "Task identities require valid UTF-8 and at most 128 bytes.")
		}
	}
	if b.ProjectID != projectID || b.HostID == "" || b.VolumeID == "" || b.MachineID == "" || b.BootID == "" || b.InstanceID == "" || b.ProjectRevision < 1 || b.VolumeRevision < 1 {
		return compute.Problem("invalid_request", "A complete original project environment binding is required.")
	}
	return nil
}

// Existing receipts and exact cancellation must remain reachable when project
// files are unavailable. Admission separately requires full project readiness.
func (s *Service) taskObservationGuest(ctx context.Context, m compute.Machine) (compute.Driver, protocol.Health, error) {
	var health protocol.Health
	if m.HostID != s.store.HostID {
		return nil, health, compute.Problem("binding_changed", "The original task VM belongs to another host.")
	}
	driver := s.drivers[m.Spec.Provider]
	if driver == nil {
		return nil, health, compute.Problem("unsupported", "The original task provider is unavailable.")
	}
	image, err := s.resolveImage(ctx, m.Spec.ImageID, m.Spec.Provider)
	if err != nil {
		return nil, health, err
	}
	if image.Guest == nil || image.Guest.Compatible() != nil || !guestimage.Has(image.Guest.Capabilities, "project-tasks") {
		return nil, health, compute.Problem("unsupported", "The original image does not declare compatible project tasks.")
	}
	health, err = compute.Health(ctx, m, driver)
	if err != nil || health.BootID == "" || health.InstanceID == "" || len(health.BootID) > 128 || len(health.InstanceID) > 128 {
		return nil, health, compute.Problem("guest_unavailable", "The original task executor identity could not be verified.")
	}
	return driver, health, nil
}

func (s *Service) taskEnvironment(ctx context.Context, projectID string, b compute.ProjectBinding) (compute.Project, compute.Machine, compute.Driver, error) {
	var m compute.Machine
	if err := s.requireProjectHost(b.HostID); err != nil {
		return compute.Project{}, m, nil, err
	}
	if err := validateTaskBinding(projectID, b); err != nil {
		return compute.Project{}, m, nil, err
	}
	p, err := s.store.Project(ctx, projectID)
	if err != nil {
		return p, m, nil, err
	}
	if p.HostID != b.HostID || p.VolumeID != b.VolumeID || p.Revision != b.ProjectRevision {
		return p, m, nil, compute.Problem("binding_changed", "The project no longer matches the original task binding.")
	}
	_, m, err = projectEnvironment(ctx, s.store.db, b.VolumeID, b.MachineID, b.VolumeRevision)
	if err != nil {
		return p, m, nil, err
	}
	d, h, err := s.projectGuest(ctx, m)
	if err != nil {
		return p, m, d, err
	}
	image, err := s.resolveImage(ctx, m.Spec.ImageID, m.Spec.Provider)
	if err != nil {
		return p, m, d, err
	}
	if image.Guest == nil || !guestimage.Has(image.Guest.Capabilities, "project-tasks") || !guestimage.Has(h.Capabilities, "project-tasks") {
		reason := "The selected image and running guest must both support project-tasks with working task storage and resource controls."
		if h.Reason != "" {
			reason += " " + h.Reason
		}
		return p, m, d, compute.Problem("unsupported", "%s Rebuild/import a capable image explicitly if its task support is missing.", reason)
	}
	if h.BootID != b.BootID || h.InstanceID != b.InstanceID {
		return p, m, d, compute.Problem("binding_changed", "The original VM boot or guest executor changed; this task was not redirected.")
	}
	return p, m, d, nil
}

func (s *Service) SubmitProjectTask(ctx context.Context, projectID string, r compute.SubmitProjectTaskRequest) (compute.ProjectTaskResult, error) {
	return s.submitProjectTask(ctx, projectID, r, false)
}

func (s *Service) submitProjectTask(ctx context.Context, projectID string, r compute.SubmitProjectTaskRequest, initialization bool) (compute.ProjectTaskResult, error) {
	if err := s.requireProjectHost(r.Binding.HostID); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	if err := compute.ValidateRequestID(r.RequestID); err != nil || !utf8.ValidString(r.RequestID) {
		if err == nil {
			err = compute.Problem("invalid_request", "Task request IDs require valid UTF-8.")
		}
		return compute.ProjectTaskResult{}, err
	}
	r.Spec = compute.NormalizeTaskSpec(r.Spec)
	validate := compute.ValidateTaskSpec
	if initialization {
		validate = compute.ValidateInitializationTaskSpec
	}
	if err := validate(r.Spec); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	if err := validateTaskBinding(projectID, r.Binding); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	operation := "submit"
	if initialization {
		operation = "initialize"
	}
	hash := fingerprint([]any{operation, projectID, r})
	if result, exists, err := s.store.taskByRequest(ctx, projectID, r.RequestID, hash); exists || err != nil {
		return result, err
	}
	release, err := s.store.projectLeases.acquire(ctx, false, r.Binding.MachineID, r.Binding.VolumeID)
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	defer release()
	_, machine, _, err := s.taskEnvironment(ctx, projectID, r.Binding)
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	if initialization {
		if err = s.RequireGuest(ctx, machine, "project-initialization"); err != nil {
			return compute.ProjectTaskResult{}, err
		}
	}
	return s.store.admitTask(ctx, projectID, "", r.RequestID, hash, r.Binding, r.Spec)
}

func (s *Service) RetryProjectTask(ctx context.Context, projectID, id string, r compute.RetryProjectTaskRequest) (compute.ProjectTaskResult, error) {
	if err := s.requireProjectHost(r.Binding.HostID); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	if err := compute.ValidateRequestID(r.RequestID); err != nil || !utf8.ValidString(r.RequestID) {
		if err == nil {
			err = compute.Problem("invalid_request", "Task request IDs require valid UTF-8.")
		}
		return compute.ProjectTaskResult{}, err
	}
	if err := validateTaskBinding(projectID, r.Binding); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	hash := fingerprint([]any{"retry", projectID, id, r})
	if result, exists, err := s.store.taskByRequest(ctx, projectID, r.RequestID, hash); exists || err != nil {
		return result, err
	}
	result, err := s.store.taskResult(ctx, projectID, id, "")
	if err != nil {
		return result, err
	}
	if result.Task.Spec.Initialization != nil {
		return compute.ProjectTaskResult{}, compute.Problem("task_conflict", "Create a separate initialization request after reviewing the existing destination; initialization attempts cannot be retried as ordinary commands.")
	}
	release, err := s.store.projectLeases.acquire(ctx, false, r.Binding.MachineID, r.Binding.VolumeID)
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	defer release()
	if _, _, _, err = s.taskEnvironment(ctx, projectID, r.Binding); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	return s.store.admitTask(ctx, projectID, id, r.RequestID, hash, r.Binding, result.Task.Spec)
}

func (s *Service) ProjectTask(ctx context.Context, projectID, id, hostID string) (compute.ProjectTaskResult, error) {
	if err := s.requireProjectHost(hostID); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	return s.store.taskResult(ctx, projectID, id, "")
}

func (s *Service) ProjectTaskByRequest(ctx context.Context, projectID, requestID, hostID string) (compute.ProjectTaskResult, error) {
	if err := s.requireProjectHost(hostID); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	result, exists, err := s.store.taskByRequest(ctx, projectID, requestID, "")
	if err == nil && !exists {
		err = compute.Problem("not_found", "Task request receipt not found.")
	}
	return result, err
}

func (s *Service) ProjectTasks(ctx context.Context, projectID, hostID string) ([]compute.ProjectTaskResult, error) {
	if err := s.requireProjectHost(hostID); err != nil {
		return nil, err
	}
	if _, err := s.store.Project(ctx, projectID); err != nil {
		return nil, err
	}
	tasks, err := rowsJSON[compute.ProjectTask](s.store.db.QueryContext(ctx, "SELECT data FROM project_tasks WHERE project_id=? ORDER BY json_extract(data,'$.createdAt') DESC", projectID))
	if err != nil {
		return nil, err
	}
	result := make([]compute.ProjectTaskResult, 0, len(tasks))
	for _, task := range tasks {
		item, err := s.store.taskResult(ctx, projectID, task.ID, "")
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Service) CancelProjectTaskAttempt(ctx context.Context, projectID, id, attemptID string, r compute.CancelProjectTaskRequest) (compute.ProjectTaskResult, error) {
	if err := s.requireProjectHost(r.Binding.HostID); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	if err := validateTaskBinding(projectID, r.Binding); err != nil {
		return compute.ProjectTaskResult{}, err
	}
	return s.store.cancelTask(ctx, projectID, id, attemptID, r.Binding)
}

func (s *Service) ProjectTaskLogs(ctx context.Context, projectID, id, attemptID, hostID string, offset int64, limit int) (compute.ProjectTaskLog, error) {
	var result compute.ProjectTaskLog
	if err := s.requireProjectHost(hostID); err != nil {
		return result, err
	}
	if limit == 0 {
		limit = 65536
	}
	if offset < 0 || limit < 1 || limit > 65536 {
		return result, compute.Problem("invalid_request", "Use a nonnegative log offset and limit 1–65536.")
	}
	err := s.store.transaction(ctx, func(tx *sql.Tx) error {
		t, err := taskRecord(ctx, tx, id)
		if err != nil {
			return err
		}
		a, err := attemptRecord(ctx, tx, attemptID)
		if err != nil {
			return err
		}
		if t.ProjectID != projectID || a.TaskID != id {
			return compute.Problem("not_found", "Task attempt not found.")
		}
		if offset > a.CachedBytes {
			return compute.Problem("invalid_request", "The requested offset is beyond the persisted task log.")
		}
		data := []byte{}
		if err := tx.QueryRowContext(ctx, "SELECT substr(log,?,?) FROM task_attempts WHERE id=?", offset+1, limit, attemptID).Scan(&data); err != nil {
			return err
		}
		if data == nil {
			data = []byte{}
		}
		result = compute.ProjectTaskLog{TaskID: id, AttemptID: attemptID, Status: a.Status, TaskLog: protocol.TaskLog{Offset: offset, Data: data, NextOffset: offset + int64(len(data)), LogBytes: a.LogBytes, LogLimited: a.LogLimited}}
		return nil
	})
	return result, err
}

func taskIdentity(a compute.TaskAttempt) protocol.TaskIdentity {
	return protocol.TaskIdentity{TaskID: a.TaskID, AttemptID: a.ID, ProjectFileScope: protocol.ProjectFileScope{Root: a.Root, RootIdentity: a.RootIdentity, BootID: a.Binding.BootID, InstanceID: a.Binding.InstanceID}}
}

func validateTaskExecution(a compute.TaskAttempt, t compute.ProjectTask, e protocol.TaskExecution) error {
	if e.TaskIdentity != taskIdentity(a) || fingerprint(e.Spec) != fingerprint(t.Spec) {
		return compute.Problem("guest_identity_mismatch", "The guest task receipt does not match the original attempt.")
	}
	valid := compute.TaskFinal(e.Status) || e.Status == "accepted" || e.Status == "running"
	if !valid || e.LogBytes < a.LogBytes || e.LogBytes > t.Spec.Limits.MaxLogBytes || e.LogBytes < a.CachedBytes || (a.LogLimited && !e.LogLimited) || len(e.Reason) > 4096 || !utf8.ValidString(e.Reason) || stringsContainControl(e.Reason) || (compute.TaskFinal(e.Status) && e.FinishedAt == nil) {
		return compute.Problem("guest_identity_mismatch", "The guest returned invalid task state or log bounds.")
	}
	if e.CreatedRoot != nil {
		if t.Spec.Initialization == nil || e.CreatedRoot.Root != path.Join(a.Root, t.Spec.Initialization.Directory) || e.CreatedRoot.RootIdentity == "" || len(e.CreatedRoot.RootIdentity) > 128 || !utf8.ValidString(e.CreatedRoot.RootIdentity) || stringsContainControl(e.CreatedRoot.RootIdentity) {
			return compute.Problem("guest_identity_mismatch", "The guest initialization root receipt does not match this task.")
		}
	}
	if t.Spec.Initialization != nil && e.Status == "succeeded" && e.CreatedRoot == nil {
		return compute.Problem("guest_identity_mismatch", "A successful initialization requires its created root receipt.")
	}
	if a.CreatedRoot != nil && (e.CreatedRoot == nil || *a.CreatedRoot != *e.CreatedRoot) {
		return compute.Problem("guest_identity_mismatch", "The guest changed the initialization's created directory identity.")
	}
	return validateFinalTaskLog(a, e)
}

func validateFinalTaskLog(a compute.TaskAttempt, e protocol.TaskExecution) error {
	// A host interruption only records the last observed output size. Freeze
	// that boundary after an actual guest terminal receipt confirms its end.
	if a.GuestFinal && (!compute.TaskFinal(e.Status) || e.LogBytes != a.LogBytes || e.LogLimited != a.LogLimited) {
		return compute.Problem("guest_identity_mismatch", "The guest changed a previously confirmed final task log.")
	}
	return nil
}

func validateTaskLog(a compute.TaskAttempt, e protocol.TaskExecution, chunk protocol.TaskLog) error {
	// Inspection fixes a known durable prefix. Output may grow before the log
	// request arrives while execution is active, but final logs are immutable.
	// Every byte requested from either known prefix must exist.
	expected := min(int64(65536), e.LogBytes-a.CachedBytes)
	finalChanged := compute.TaskFinal(e.Status) && (chunk.LogBytes != e.LogBytes || chunk.LogLimited != e.LogLimited)
	if expected <= 0 || int64(len(chunk.Data)) != expected || chunk.Offset != a.CachedBytes || chunk.NextOffset != chunk.Offset+expected || chunk.LogBytes < e.LogBytes || (e.LogLimited && !chunk.LogLimited) || finalChanged {
		return compute.Problem("guest_identity_mismatch", "The guest returned an incomplete or inconsistent task log prefix.")
	}
	return nil
}

func stringsContainControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Dispatch is claimed durably once. Recovery only queries the original guest
// receipt; the VM lifecycle queue's replay policy never applies to commands.
func (s *Service) dispatchTask(ctx context.Context, a compute.TaskAttempt, t compute.ProjectTask) error {
	release, err := s.store.projectLeases.acquire(ctx, false, a.Binding.MachineID, a.Binding.VolumeID)
	if err != nil {
		return err
	}
	defer release()
	_, m, d, err := s.taskEnvironment(ctx, t.ProjectID, a.Binding)
	if err != nil {
		return s.store.saveTaskFailure(ctx, a.ID, "failed", fmt.Sprintf("Task was not dispatched: %v", err))
	}
	var execution protocol.TaskExecution
	err = taskJSON(ctx, m, d, "submit", protocol.TaskSubmit{ProjectFileScope: taskIdentity(a).ProjectFileScope, TaskID: t.ID, AttemptID: a.ID, Spec: t.Spec}, &execution)
	if err != nil {
		status := "failed"
		if compute.ErrorCode(err) == "guest_unavailable" {
			status = "unknown"
		}
		return s.store.saveTaskFailure(ctx, a.ID, status, err.Error())
	}
	if err := validateTaskExecution(a, t, execution); err != nil {
		return s.store.saveTaskFailure(ctx, a.ID, "unknown", err.Error())
	}
	return s.store.saveTaskExecution(ctx, a.ID, execution, nil)
}

func (s *Service) reconcileTask(ctx context.Context, a compute.TaskAttempt) error {
	t, err := taskRecord(ctx, s.store.db, a.TaskID)
	if err != nil {
		return err
	}
	if a.Status == "accepted" {
		claimed, ok, err := s.store.claimTask(ctx, a.ID)
		if err != nil || !ok {
			return err
		}
		return s.dispatchTask(ctx, claimed, t)
	}
	m, err := s.store.Machine(ctx, a.Binding.MachineID)
	if err != nil {
		return s.store.saveTaskFailure(ctx, a.ID, "unknown", "The original VM cannot be inspected; this attempt was not replayed.")
	}
	if m.Status == "stopped" || m.Status == "deleted" || m.Status == "lost" || m.Spec.VolumeID != a.Binding.VolumeID {
		return s.store.saveTaskFailure(ctx, a.ID, "interrupted", "The original VM stopped or its project volume changed before completion could be verified.")
	}
	d, health, err := s.taskObservationGuest(ctx, m)
	if err != nil {
		return s.store.saveTaskFailure(ctx, a.ID, "unknown", "The original guest executor is unavailable; its task was not replayed.")
	}
	if !guestimage.Has(health.Capabilities, "project-tasks") {
		return s.store.saveTaskFailure(ctx, a.ID, "unknown", "The guest task executor or its state/resource controls are unavailable.")
	}
	var execution protocol.TaskExecution
	identity := taskIdentity(a)
	err = taskJSON(ctx, m, d, "inspect", identity, &execution)
	if err != nil {
		if health.BootID != a.Binding.BootID || health.InstanceID != a.Binding.InstanceID {
			return s.store.saveTaskFailure(ctx, a.ID, "interrupted", "The original guest execution instance ended; its result could not be recovered.")
		}
		return s.store.saveTaskFailure(ctx, a.ID, "unknown", "The original task receipt is unavailable; no command was resubmitted.")
	}
	if err := validateTaskExecution(a, t, execution); err != nil {
		return s.store.saveTaskFailure(ctx, a.ID, "unknown", err.Error())
	}
	if a.Cancel && !compute.TaskFinal(execution.Status) {
		if health.BootID != a.Binding.BootID || health.InstanceID != a.Binding.InstanceID {
			return s.store.saveTaskFailure(ctx, a.ID, "interrupted", "The original executor ended; cancellation was not sent to its replacement.")
		}
		if err := taskJSON(ctx, m, d, "cancel", identity, &execution); err != nil {
			return s.store.saveTaskFailure(ctx, a.ID, "unknown", "Cancellation could not be verified against the original attempt; it remains requested.")
		}
		if err := validateTaskExecution(a, t, execution); err != nil {
			return s.store.saveTaskFailure(ctx, a.ID, "unknown", err.Error())
		}
	}
	var chunk *protocol.TaskLog
	if execution.LogBytes > a.CachedBytes {
		chunk = &protocol.TaskLog{}
		limit := int(min(int64(65536), execution.LogBytes-a.CachedBytes))
		if err := taskJSON(ctx, m, d, "logs", protocol.TaskLogRequest{TaskIdentity: identity, Offset: a.CachedBytes, Limit: limit}, chunk); err != nil {
			if err := s.store.saveTaskExecution(ctx, a.ID, execution, nil); err != nil {
				return err
			}
			return s.store.saveTaskFailure(ctx, a.ID, "unknown", "The guest result is recorded but its remaining logs could not be synchronized.")
		}
		logErr := validateTaskLog(a, execution, *chunk)
		if chunk.LogBytes > t.Spec.Limits.MaxLogBytes {
			logErr = compute.Problem("guest_identity_mismatch", "The guest returned an invalid task log size.")
		}
		if logErr != nil {
			// Preserve a valid final receipt even when its log transport fails.
			// The missing tail remains visible and eligible for later recovery.
			if err := s.store.saveTaskExecution(ctx, a.ID, execution, nil); err != nil {
				return err
			}
			return s.store.saveTaskFailure(ctx, a.ID, "unknown", logErr.Error())
		}
	}
	return s.store.saveTaskExecution(ctx, a.ID, execution, chunk)
}

func (s *Service) taskMonitor(ctx context.Context) {
	var workers sync.WaitGroup
	defer workers.Wait()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		items, err := rowsJSON[compute.TaskAttempt](s.store.db.QueryContext(ctx, "SELECT data FROM task_attempts WHERE status NOT IN ('succeeded','failed','cancelled','interrupted') OR cached_bytes<log_bytes ORDER BY updated_at LIMIT 32"))
		if err == nil {
			// Four independent transports keep one unavailable VM from blocking
			// all other task reconciliation. Cancellation stops polling, not work.
			sem := make(chan struct{}, 4)
			done := make(chan struct{}, len(items))
			for _, a := range items {
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				workers.Add(1)
				go func(a compute.TaskAttempt) {
					defer workers.Done()
					defer func() { <-sem; done <- struct{}{} }()
					if err := s.reconcileTask(ctx, a); err != nil && ctx.Err() == nil && s.Logger != nil {
						s.Logger.Error("project_task.reconcile_failed", err, "taskId", a.TaskID, "attemptId", a.ID)
					}
				}(a)
			}
			for range items {
				select {
				case <-done:
				case <-ctx.Done():
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
