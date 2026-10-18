package control

import (
	"context"
	"database/sql"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

// Update records an applied specification separately from an operation's target.
// Failed provider edits remain visible and retain their maximum disk reservation.
func (s *Store) Update(ctx context.Context, c compute.Config, id string, req compute.UpdateRequest) (result compute.Result, err error) {
	req.Spec.Name = compute.NormalizeMachineName(req.Spec.Name)
	if err = compute.ValidateRequestID(req.RequestID); err != nil {
		return
	}
	hash := fingerprint(struct {
		ID      string
		Request compute.UpdateRequest
	}{id, req})
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		op, exists, err := replay(ctx, tx, req.RequestID, hash)
		if err != nil {
			return err
		}
		if exists {
			result.Operation = op.Operation
			result.Machine, err = machine(ctx, tx, op.MachineID)
			return err
		}
		m, err := machine(ctx, tx, id)
		if err != nil {
			return err
		}
		if req.Revision != m.Revision {
			return compute.Problem("revision_conflict", "VM changed. Refresh before saving its configuration.")
		}
		if m.Status == "deleted" || m.Desired == "deleted" {
			return compute.Problem("unavailable", "The VM is being deleted or has been deleted.")
		}
		if req.Spec.Provider != m.Spec.Provider || req.Spec.ImageID != m.Spec.ImageID || req.Spec.VolumeID != m.Spec.VolumeID || req.Spec.ProjectGiB != m.Spec.ProjectGiB {
			return compute.Problem("invalid_request", "Use the dedicated image and volume operations to change storage attachments.")
		}
		if err = c.ValidateRequest(compute.CreateRequest{Spec: req.Spec}); err != nil {
			return err
		}
		if err = checkMachineNames(ctx, tx, compute.Machine{ID: m.ID, Spec: req.Spec}); err != nil {
			return err
		}
		if req.Spec.Resources != m.Spec.Resources || m.PendingSpec != nil {
			if m.Status != "stopped" || m.Desired != "stopped" {
				return compute.Problem("configuration_conflict", "Stop the VM before changing CPU, memory or disk allocation.")
			}
		}
		allocated := m.Spec.Resources.DiskGiB
		if m.PendingSpec != nil {
			allocated = max(allocated, m.PendingSpec.Resources.DiskGiB)
		}
		if req.Spec.Resources.DiskGiB < allocated {
			return compute.Problem("invalid_request", "Disk shrinking is not supported; retry the pending growth if its outcome was uncertain.")
		}
		if err = admit(ctx, tx, c.Capacity, compute.Resources{DiskGiB: req.Spec.Resources.DiskGiB - allocated}, false); err != nil {
			return err
		}
		op = newOperation(req.RequestID, m.ID, "configure")
		op.Configuration = &req.Spec
		op.ResourcesChanged = req.Spec.Resources != m.Spec.Resources || (m.PendingSpec != nil && m.PendingSpec.Resources != m.Spec.Resources)
		op.ExpiresAt = m.ExpiresAt
		if req.Spec.Kind == "development" {
			op.ExpiresAt = nil
		} else if req.RenewLease || m.Spec.Kind != "task" || req.Spec.TTLSeconds != m.Spec.TTLSeconds {
			expires := time.Now().UTC().Add(time.Duration(req.Spec.TTLSeconds) * time.Second)
			op.ExpiresAt = &expires
		}
		if err = insertOperation(ctx, tx, op, hash, id, m.Spec.VolumeID); err != nil {
			return err
		}
		m.PendingSpec = &req.Spec
		m.Revision++
		m.UpdatedAt, m.Reason = time.Now().UTC(), ""
		if err = writeMachine(ctx, tx, m); err != nil {
			return err
		}
		result = compute.Result{Machine: m, Operation: op.Operation}
		return nil
	})
	return
}

func (s *Store) Phase(ctx context.Context, id, phase string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		op, err := decodeRow[operationRecord](tx.QueryRowContext(ctx, "SELECT data FROM operations WHERE id=?", id))
		if err != nil {
			return err
		}
		if op.Status != "running" {
			return compute.Problem("operation_conflict", "The operation is no longer running.")
		}
		op.Phase, op.UpdatedAt = phase, time.Now().UTC()
		return updateOperation(ctx, tx, op)
	})
}
