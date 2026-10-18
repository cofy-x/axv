package control

import (
	"context"
	"database/sql"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func updateOperation(ctx context.Context, tx *sql.Tx, op operationRecord) error {
	_, err := tx.ExecContext(ctx, "UPDATE operations SET status=?,data=? WHERE id=?", op.Status, encode(op), op.ID)
	return err
}

// Recover is called only while holding the host service's exclusive process lock.
func (s *Store) Recover(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		ops, err := rowsJSON[operationRecord](tx.QueryContext(ctx, "SELECT data FROM operations WHERE status='running'"))
		if err != nil {
			return err
		}
		for _, op := range ops {
			op.Status, op.UpdatedAt = "queued", time.Now().UTC()
			if err = updateOperation(ctx, tx, op); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Claim(ctx context.Context) (op operationRecord, found bool, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		op, err = decodeRow[operationRecord](tx.QueryRowContext(ctx, "SELECT data FROM operations WHERE status='queued' ORDER BY created_at LIMIT 1"))
		if compute.ErrorCode(err) == "not_found" {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		op.Status, op.UpdatedAt = "running", time.Now().UTC()
		if op.Phase == "" {
			op.Phase = "inspecting"
		}
		if err = updateOperation(ctx, tx, op); err != nil {
			return err
		}
		if op.MachineID == "" || (op.ResourceKind != "" && op.ResourceKind != "machine") {
			return nil
		}
		m, err := machine(ctx, tx, op.MachineID)
		if err != nil {
			return err
		}
		switch op.Action {
		case "create":
			m.Status = "provisioning"
		case "start", "restart":
			m.Status = "starting"
		case "stop", "force-stop":
			m.Status = "stopping"
		case "delete":
			m.Status = "deleting"
		}
		m.Ready, m.UpdatedAt = false, time.Now().UTC()
		return writeMachine(ctx, tx, m)
	})
	return
}

func (s *Store) Finish(ctx context.Context, op operationRecord, observed compute.Observation, operationErr error) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		current, err := decodeRow[operationRecord](tx.QueryRowContext(ctx, "SELECT data FROM operations WHERE id=?", op.ID))
		if err != nil {
			return err
		}
		if current.Status == "succeeded" || current.Status == "failed" {
			return nil
		}
		if current.Status != "running" {
			return compute.Problem("operation_conflict", "Only the running operation can publish its result.")
		}
		op = current
		op.UpdatedAt, op.Status = time.Now().UTC(), "succeeded"
		if operationErr != nil {
			op.Status, op.Error = "failed", operationErr.Error()
		} else {
			op.Phase = "complete"
		}
		if err := updateOperation(ctx, tx, op); err != nil {
			return err
		}
		if op.ResourceKind == "image" {
			return s.finishImage(ctx, tx, op, observed, operationErr)
		}
		if op.VolumeID != "" {
			return s.finishVolume(ctx, tx, op, observed, operationErr)
		}
		m, err := machine(ctx, tx, op.MachineID)
		if err != nil {
			return err
		}
		m.Status, m.Ready, m.Reason, m.UpdatedAt = observed.Status, observed.Ready, observed.Reason, time.Now().UTC()
		m.GuestState, m.GuestCapabilities, m.GuestReason = observed.GuestState, observed.GuestCapabilities, observed.GuestReason
		if m.Status == "" {
			m.Status = "unknown"
		}
		if operationErr != nil {
			m.Reason = operationErr.Error()
			if m.Status != "running" && m.Status != "stopped" {
				m.Status = "failed"
			}
		}
		if observed.Status == "stopped" || observed.Status == "absent" || observed.Status == "deleted" {
			m.Reserved = false
		}
		if op.Action == "delete" && operationErr == nil {
			m.Status, m.Ready, m.Reserved = "deleted", false, false
			if m.Spec.VolumeID == "" {
				return writeMachine(ctx, tx, m)
			}
			v, err := volume(ctx, tx, m.Spec.VolumeID)
			if err != nil {
				return err
			}
			if v.Retain {
				v.MachineID = ""
				v.Revision++
				if err = writeVolume(ctx, tx, v); err != nil {
					return err
				}
			} else {
				if _, err = tx.ExecContext(ctx, "DELETE FROM volumes WHERE id=?", v.ID); err != nil {
					return err
				}
			}
		}
		if op.Action == "configure" && operationErr == nil && op.Configuration != nil {
			m.Spec, m.PendingSpec, m.ExpiresAt = *op.Configuration, nil, op.ExpiresAt
			m.Revision++
			if m.Spec.VolumeID == "" {
				return writeMachine(ctx, tx, m)
			}
			v, err := volume(ctx, tx, m.Spec.VolumeID)
			if err != nil {
				return err
			}
			v.Retain = m.Spec.RetainVolume
			v.Revision++
			if err = writeVolume(ctx, tx, v); err != nil {
				return err
			}
		}
		return writeMachine(ctx, tx, m)
	})
}

func (s *Store) Observe(ctx context.Context, before compute.Machine, o compute.Observation) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		active, err := resourceActive(ctx, tx, before.ID)
		if err != nil {
			return err
		}
		if active {
			return nil
		}
		m, err := machine(ctx, tx, before.ID)
		if err != nil {
			return err
		}
		if m.Revision != before.Revision || !m.UpdatedAt.Equal(before.UpdatedAt) || m.Status == "deleted" {
			return nil
		}
		if o.Status == "absent" {
			o.Status, o.Reason = "lost", "Provider resources are missing. No replacement was started."
		}
		m.Status, m.Ready, m.Reason = o.Status, o.Ready, o.Reason
		m.GuestState, m.GuestCapabilities, m.GuestReason = o.GuestState, o.GuestCapabilities, o.GuestReason
		if o.Status == "running" {
			m.Reserved = true
		}
		if o.Status == "stopped" || o.Status == "lost" {
			m.Reserved = false
		}
		return writeMachine(ctx, tx, m)
	})
}
