package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type volumeChange struct {
	Target compute.Volume  `json:"target"`
	Source *compute.Volume `json:"source,omitempty"`
}

func stoppedVolumeMachine(ctx context.Context, tx *sql.Tx, id string, revision int64) (compute.Machine, error) {
	m, err := machine(ctx, tx, id)
	if err != nil {
		return m, err
	}
	if revision != 0 && revision != m.Revision {
		return m, compute.Problem("revision_conflict", "The VM changed. Refresh its attachment settings.")
	}
	if m.Status != "stopped" || m.Desired != "stopped" || m.PendingSpec != nil {
		return m, compute.Problem("volume_conflict", "Stop the VM and finish its configuration before changing or copying attached storage.")
	}
	return m, nil
}

func (s *Store) CreateVolume(ctx context.Context, c compute.Config, req compute.CreateVolumeRequest) (result compute.VolumeResult, err error) {
	if err = compute.ValidateRequestID(req.RequestID); err != nil {
		return
	}
	if err = compute.ValidateResourceName(req.Name); err != nil {
		return
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		op, exists, err := replay(ctx, tx, req.RequestID, fingerprint(req))
		if err != nil {
			return err
		}
		if exists {
			result.Operation = op.Operation
			result.Volume, err = volume(ctx, tx, op.VolumeID)
			return err
		}
		v := compute.Volume{ID: compute.NewID("vol"), Name: req.Name, Revision: 1, Provider: req.Provider, SizeGiB: req.SizeGiB, Retain: req.Retain, Status: "creating", CreatedAt: time.Now().UTC()}
		change := volumeChange{Target: v}
		dependencies := []string{}
		action := "create-volume"
		if req.SourceVolumeID != "" {
			source, err := volume(ctx, tx, req.SourceVolumeID)
			if err != nil {
				return err
			}
			if source.Status != "ready" || source.PendingSizeGiB != 0 {
				return compute.Problem("volume_conflict", "The source volume must be ready before copying it.")
			}
			if source.MachineID != "" {
				if _, err = stoppedVolumeMachine(ctx, tx, source.MachineID, 0); err != nil {
					return err
				}
			}
			if v.Provider == "" {
				v.Provider = source.Provider
			}
			if v.SizeGiB == 0 {
				v.SizeGiB = source.SizeGiB
			}
			if v.Provider != source.Provider || v.SizeGiB < source.SizeGiB {
				return compute.Problem("invalid_request", "A volume copy must use the source provider and cannot be smaller.")
			}
			change.Source, change.Target = &source, v
			v.TransientGiB = source.SizeGiB
			dependencies = append(dependencies, source.ID, source.MachineID)
			action, v.Status = "copy-volume", "copying"
		}
		if v.Provider != "lima" || v.SizeGiB < 1 || v.SizeGiB > c.Capacity.DiskGiB {
			return compute.Problem("invalid_request", "Select the Lima provider and a volume size within host capacity.")
		}
		if err = admit(ctx, tx, c.Capacity, compute.Resources{DiskGiB: v.SizeGiB + v.TransientGiB}, false); err != nil {
			return err
		}
		change.Target = v
		op = newOperation(req.RequestID, "", action)
		op.VolumeID, op.ResourceKind, op.Payload = v.ID, "volume", json.RawMessage(encode(change))
		if err = insertOperation(ctx, tx, op, fingerprint(req), v.ID, dependencies...); err != nil {
			return err
		}
		if err = writeVolume(ctx, tx, v); err != nil {
			return err
		}
		result = compute.VolumeResult{Volume: v, Operation: op.Operation}
		return nil
	})
	return
}

func (s *Store) VolumeAction(ctx context.Context, c compute.Config, id string, req compute.VolumeRequest) (result compute.VolumeResult, err error) {
	if err = compute.ValidateRequestID(req.RequestID); err != nil {
		return
	}
	hash := fingerprint(struct {
		ID      string
		Request compute.VolumeRequest
	}{id, req})
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		op, exists, err := replay(ctx, tx, req.RequestID, hash)
		if err != nil {
			return err
		}
		if exists {
			result.Operation = op.Operation
			result.Volume, err = volume(ctx, tx, id)
			return err
		}
		v, err := volume(ctx, tx, id)
		if err != nil {
			return err
		}
		if v.Revision != req.Revision {
			return compute.Problem("revision_conflict", "The volume changed. Refresh before editing it.")
		}
		if v.Status != "ready" && v.Status != "failed" {
			return compute.Problem("volume_conflict", "Finish the current volume operation first.")
		}
		if v.Status == "failed" && (v.PendingAction != req.Action+"-volume" || (req.Action == "attach" && v.PendingMachineID != req.MachineID)) {
			return compute.Problem("volume_conflict", "Retry the failed operation with the same attachment target, or delete an incomplete detached volume before creating a new one.")
		}
		target := v
		machineID := v.MachineID
		switch req.Action {
		case "rename":
			if err = compute.ValidateResourceName(req.Name); err != nil {
				return err
			}
			target.Name = req.Name
		case "retain":
			target.Retain = req.Retain
			if machineID != "" {
				m, err := machine(ctx, tx, machineID)
				if err != nil {
					return err
				}
				if m.Spec.Kind == "development" && !target.Retain {
					return compute.Problem("volume_conflict", "Development VMs require retained project storage.")
				}
			}
		case "resize":
			if req.SizeGiB < max(v.SizeGiB, v.PendingSizeGiB) || req.SizeGiB > c.Capacity.DiskGiB {
				return compute.Problem("invalid_request", "Volumes can grow within host capacity; shrinking is unavailable.")
			}
			if machineID != "" {
				if _, err = stoppedVolumeMachine(ctx, tx, machineID, req.MachineRevision); err != nil {
					return err
				}
			}
			if err = admit(ctx, tx, c.Capacity, compute.Resources{DiskGiB: req.SizeGiB - max(v.SizeGiB, v.PendingSizeGiB)}, false); err != nil {
				return err
			}
			target.SizeGiB, v.PendingSizeGiB = req.SizeGiB, req.SizeGiB
		case "attach":
			if v.MachineID != "" || v.PendingSizeGiB != 0 || req.MachineRevision < 1 {
				return compute.Problem("volume_conflict", "Choose a detached, ready volume and the current VM revision.")
			}
			m, err := stoppedVolumeMachine(ctx, tx, req.MachineID, req.MachineRevision)
			if err != nil {
				return err
			}
			if m.Spec.Provider != v.Provider || m.Spec.VolumeID != "" {
				return compute.Problem("volume_conflict", "Detach the VM's current project volume before attaching a compatible one.")
			}
			if m.Spec.Kind == "development" && !v.Retain {
				return compute.Problem("volume_conflict", "Retain this volume before attaching it to a development VM.")
			}
			machineID, target.MachineID = m.ID, m.ID
		case "detach":
			if machineID == "" || req.MachineRevision < 1 {
				return compute.Problem("volume_conflict", "Choose an attached volume and the current VM revision.")
			}
			if _, err = stoppedVolumeMachine(ctx, tx, machineID, req.MachineRevision); err != nil {
				return err
			}
			target.MachineID = ""
		default:
			return compute.Problem("invalid_request", "Volume action must be rename, retain, resize, attach or detach.")
		}
		op = newOperation(req.RequestID, machineID, req.Action+"-volume")
		op.ResourceKind, op.VolumeID, op.Payload = "volume", id, json.RawMessage(encode(volumeChange{Target: target}))
		if err = insertOperation(ctx, tx, op, hash, id, machineID); err != nil {
			return err
		}
		v.Status, v.Reason = "updating", ""
		v.PendingAction, v.PendingMachineID = op.Action, machineID
		v.Revision++
		if err = writeVolume(ctx, tx, v); err != nil {
			return err
		}
		result = compute.VolumeResult{Volume: v, Operation: op.Operation}
		return nil
	})
	return
}

func (s *Service) performVolume(ctx context.Context, op operationRecord) (compute.Observation, error) {
	v, err := s.store.Volume(ctx, op.VolumeID)
	if err != nil {
		return compute.Observation{}, err
	}
	driver := s.drivers[v.Provider]
	if driver == nil {
		return compute.Observation{}, compute.Problem("unsupported", "The volume provider is unavailable.")
	}
	if op.Action == "delete-volume" {
		return compute.Observation{}, driver.DeleteVolume(ctx, v)
	}
	d, ok := driver.(compute.VolumeDriver)
	if !ok {
		return compute.Observation{}, compute.Problem("unsupported", "This provider does not support volume management.")
	}
	var change volumeChange
	if err = json.Unmarshal(op.Payload, &change); err != nil {
		return compute.Observation{}, err
	}
	if change.Target.ID != v.ID {
		return compute.Observation{}, compute.Problem("invalid_request", "Volume operation identity mismatch.")
	}
	if err = s.phase(ctx, op, op.Action); err != nil {
		return compute.Observation{}, err
	}
	switch op.Action {
	case "create-volume":
		err = d.EnsureVolume(ctx, change.Target)
	case "copy-volume":
		if change.Source == nil {
			return compute.Observation{}, compute.Problem("invalid_request", "The volume copy has no source.")
		}
		err = d.CopyVolume(ctx, *change.Source, change.Target)
	case "resize-volume":
		err = d.ResizeVolume(ctx, v, change.Target.SizeGiB)
	case "attach-volume", "detach-volume":
		m, machineErr := s.store.Machine(ctx, op.MachineID)
		if machineErr != nil {
			return compute.Observation{}, machineErr
		}
		attachment := change.Target
		if op.Action == "detach-volume" {
			attachment = compute.Volume{}
		}
		err = d.AttachVolume(ctx, m, attachment)
	case "rename-volume", "retain-volume":
		// Metadata changes need no provider mutation.
	default:
		err = compute.Problem("invalid_request", "Unknown volume operation.")
	}
	return compute.Observation{Volume: &change.Target}, err
}

func (s *Store) finishVolume(ctx context.Context, tx *sql.Tx, op operationRecord, observed compute.Observation, operationErr error) error {
	v, err := volume(ctx, tx, op.VolumeID)
	if err != nil {
		return err
	}
	if op.Action == "delete-volume" && operationErr == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM volumes WHERE id=? AND machine_id=''", v.ID)
		return err
	}
	if operationErr != nil {
		v.Status, v.Reason = "failed", operationErr.Error()
		v.PendingAction, v.PendingMachineID = op.Action, op.MachineID
		v.Revision++
		return writeVolume(ctx, tx, v)
	}
	if observed.Volume == nil {
		return compute.Problem("invalid_request", "The volume operation did not return its applied state.")
	}
	applied := *observed.Volume
	applied.Revision, applied.Status, applied.Reason, applied.PendingSizeGiB = v.Revision+1, "ready", "", 0
	applied.TransientGiB, applied.PendingAction, applied.PendingMachineID = 0, "", ""
	if err = writeVolume(ctx, tx, applied); err != nil {
		return err
	}
	if op.MachineID != "" && (op.Action == "attach-volume" || op.Action == "detach-volume" || op.Action == "resize-volume" || op.Action == "retain-volume") {
		m, err := machine(ctx, tx, op.MachineID)
		if err != nil {
			return err
		}
		if op.Action == "detach-volume" {
			m.Spec.VolumeID = ""
		} else {
			m.Spec.VolumeID, m.Spec.ProjectGiB, m.Spec.RetainVolume = applied.ID, applied.SizeGiB, applied.Retain
		}
		m.Revision++
		m.UpdatedAt = time.Now().UTC()
		return writeMachine(ctx, tx, m)
	}
	return nil
}
