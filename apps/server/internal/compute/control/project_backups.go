package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func (s *Service) ProjectBackups(ctx context.Context) ([]compute.Volume, error) {
	volumes, err := s.store.Volumes(ctx)
	result := []compute.Volume{}
	for _, v := range volumes {
		if v.IsProjectBackup() {
			result = append(result, v)
		}
	}
	return result, err
}

func (s *Service) BackupProject(ctx context.Context, id string, req compute.BackupProjectRequest) (compute.VolumeResult, error) {
	p, err := s.store.Project(ctx, id)
	if err != nil {
		return compute.VolumeResult{}, err
	}
	if err := compute.ValidateRequestID(req.RequestID); err != nil {
		return compute.VolumeResult{}, err
	}
	if err := compute.ValidateResourceName(req.Name); err != nil {
		return compute.VolumeResult{}, err
	}
	var result compute.VolumeResult
	err = s.store.resourceTransaction(ctx, []string{p.VolumeID, req.MachineID}, func(tx *sql.Tx) error {
		hash := fingerprint(struct {
			ProjectID string
			Request   compute.BackupProjectRequest
		}{id, req})
		op, exists, err := replay(ctx, tx, req.RequestID, hash)
		if err != nil {
			return err
		}
		if exists {
			result.Operation = op.Operation
			result.Volume, err = volume(ctx, tx, op.VolumeID)
			return err
		}
		p, err := project(ctx, tx, id)
		if err != nil {
			return err
		}
		v, err := volume(ctx, tx, p.VolumeID)
		if err != nil {
			return err
		}
		if _, ok := s.drivers[v.Provider].(compute.VolumeCopier); !ok {
			return compute.Problem("unsupported", "This provider does not support offline project backups.")
		}
		if p.Revision != req.ProjectRevision || v.Revision != req.VolumeRevision || req.MachineRevision < 1 {
			return compute.Problem("revision_conflict", "The reviewed project, volume or VM revision changed.")
		}
		if v.IsProjectBackup() || !v.Retain || v.MachineID == "" || v.MachineID != req.MachineID || !copySourceReady(v) {
			return compute.Problem("volume_conflict", "Back up a ready retained project volume attached to the explicitly selected stopped VM.")
		}
		m, err := stoppedVolumeMachine(ctx, tx, req.MachineID, req.MachineRevision)
		if err != nil {
			return err
		}
		if m.Spec.VolumeID != v.ID || m.Spec.Provider != v.Provider {
			return compute.Problem("binding_changed", "The VM no longer owns the reviewed project volume.")
		}
		var active bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_attempts WHERE status NOT IN ('succeeded','failed','cancelled','interrupted') AND json_extract(data,'$.binding.volumeId')=?)`, v.ID).Scan(&active); err != nil {
			return err
		}
		if active {
			return compute.Problem("task_conflict", "Finish or reconcile all active and unknown project tasks before backing up this volume.")
		}
		projects, err := rowsJSON[compute.Project](tx.QueryContext(ctx, "SELECT data FROM projects WHERE volume_id=? ORDER BY id", v.ID))
		if err != nil {
			return err
		}
		manifest := &compute.ProjectVolumeBackup{HostID: s.store.HostID, SourceVolumeID: v.ID, SourceVolumeRevision: v.Revision, SourceMachineID: m.ID, SourceMachineRevision: m.Revision, Projects: projects, CreatedAt: time.Now().UTC()}
		result, err = queueProjectCopy(ctx, tx, s.config, req.RequestID, hash, req.Name, v, manifest, "")
		return err
	})
	return result, err
}

func copySourceReady(v compute.Volume) bool {
	return v.Status == "ready" && v.PendingSizeGiB == 0 && v.PendingAction == "" && v.PendingMachineID == ""
}

func queueProjectCopy(ctx context.Context, tx *sql.Tx, config compute.Config, requestID, hash, name string, source compute.Volume, manifest *compute.ProjectVolumeBackup, restoredFrom string) (compute.VolumeResult, error) {
	v := compute.Volume{ID: compute.NewID("vol"), Name: name, Provider: source.Provider, SizeGiB: source.SizeGiB, TransientGiB: source.SizeGiB, Retain: true, Revision: 1, Status: "copying", CreatedAt: time.Now().UTC(), Backup: manifest, RestoredFrom: restoredFrom}
	if err := admit(ctx, tx, config.Capacity, compute.Resources{DiskGiB: v.SizeGiB + v.TransientGiB}, false); err != nil {
		return compute.VolumeResult{}, err
	}
	op := newOperation(requestID, "", "copy-volume")
	op.VolumeID, op.ResourceKind = v.ID, "volume"
	op.Payload = json.RawMessage(encode(volumeChange{Source: &source, Target: v}))
	if err := insertOperation(ctx, tx, op, hash, v.ID, source.ID, source.MachineID); err != nil {
		return compute.VolumeResult{}, err
	}
	if err := writeVolume(ctx, tx, v); err != nil {
		return compute.VolumeResult{}, err
	}
	return compute.VolumeResult{Volume: v, Operation: op.Operation}, nil
}

func (s *Service) RestoreProjectVolume(ctx context.Context, id string, req compute.RestoreProjectVolumeRequest) (compute.VolumeResult, error) {
	if err := compute.ValidateRequestID(req.RequestID); err != nil {
		return compute.VolumeResult{}, err
	}
	if err := compute.ValidateResourceName(req.Name); err != nil {
		return compute.VolumeResult{}, err
	}
	var result compute.VolumeResult
	err := s.store.resourceTransaction(ctx, []string{id}, func(tx *sql.Tx) error {
		hash := fingerprint(struct {
			BackupID string
			Request  compute.RestoreProjectVolumeRequest
		}{id, req})
		op, exists, err := replay(ctx, tx, req.RequestID, hash)
		if err != nil {
			return err
		}
		if exists {
			result.Operation = op.Operation
			result.Volume, err = volume(ctx, tx, op.VolumeID)
			return err
		}
		v, err := volume(ctx, tx, id)
		if err != nil {
			return err
		}
		if req.Revision < 1 || v.Revision != req.Revision {
			return compute.Problem("revision_conflict", "The selected backup revision changed.")
		}
		if !v.IsProjectBackup() || !copySourceReady(v) || v.MachineID != "" || v.Backup.HostID != s.store.HostID {
			return compute.Problem("volume_conflict", "Select a completed detached backup from this host.")
		}
		if _, ok := s.drivers[v.Provider].(compute.VolumeCopier); !ok {
			return compute.Problem("unsupported", "The backup provider is unavailable.")
		}
		result, err = queueProjectCopy(ctx, tx, s.config, req.RequestID, hash, req.Name, v, v.Backup, v.ID)
		return err
	})
	return result, err
}

func restoredProjectReceipt(ctx context.Context, q querier, requestID, hash string) (compute.Project, bool, error) {
	var saved, id string
	err := q.QueryRowContext(ctx, "SELECT fingerprint,project_id FROM project_requests WHERE request_id=?", requestID).Scan(&saved, &id)
	if errors.Is(err, sql.ErrNoRows) {
		var reused bool
		if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM operations WHERE request_id=? UNION ALL SELECT 1 FROM task_requests WHERE request_id=?)", requestID, requestID).Scan(&reused); err != nil {
			return compute.Project{}, false, err
		}
		if reused {
			return compute.Project{}, false, compute.Problem("idempotency_conflict", "This request ID belongs to another compute operation.")
		}
		return compute.Project{}, false, nil
	}
	if err != nil {
		return compute.Project{}, false, err
	}
	if saved != hash {
		return compute.Project{}, false, compute.Problem("idempotency_conflict", "This request ID belongs to a different project request.")
	}
	p, err := project(ctx, q, id)
	return p, true, err
}

func restoredSource(v compute.Volume, backupID, sourceID string) (compute.Project, error) {
	if v.RestoredFrom != backupID || v.Backup == nil {
		return compute.Project{}, compute.Problem("binding_changed", "The selected volume was not restored from this backup.")
	}
	for _, p := range v.Backup.Projects {
		if p.ID == sourceID {
			return p, nil
		}
	}
	return compute.Project{}, compute.Problem("not_found", "This backup does not contain the selected original project.")
}

// BindRestoredProject is a distinct, explicit publication. It never edits an
// existing project's volume or grants an old execution binding a new target.
func (s *Service) BindRestoredProject(ctx context.Context, backupID string, req compute.BindRestoredProjectRequest) (compute.Project, error) {
	if err := compute.ValidateRequestID(req.RequestID); err != nil {
		return compute.Project{}, err
	}
	if req.Name != "" {
		if err := compute.ValidateResourceName(req.Name); err != nil {
			return compute.Project{}, err
		}
	}
	if req.VolumeID == "" || req.MachineID == "" || req.SourceProjectID == "" || req.VolumeRevision < 1 {
		return compute.Project{}, compute.Problem("invalid_request", "Select the original project and exact restored volume, revision and running VM.")
	}
	hash := fingerprint(struct {
		BackupID string
		Request  compute.BindRestoredProjectRequest
	}{backupID, req})
	if p, found, err := restoredProjectReceipt(ctx, s.store.db, req.RequestID, hash); err != nil || found {
		return p, err
	}
	release, err := s.store.projectLeases.acquire(ctx, false, req.VolumeID, req.MachineID)
	if err != nil {
		return compute.Project{}, err
	}
	defer release()
	v, m, err := projectEnvironment(ctx, s.store.db, req.VolumeID, req.MachineID, req.VolumeRevision)
	if err != nil {
		return compute.Project{}, err
	}
	source, err := restoredSource(v, backupID, req.SourceProjectID)
	if err != nil {
		return compute.Project{}, err
	}
	driver, health, err := s.projectGuest(ctx, m)
	if err != nil {
		return compute.Project{}, err
	}
	image, err := s.resolveImage(ctx, m.Spec.ImageID, m.Spec.Provider)
	if err != nil {
		return compute.Project{}, err
	}
	if image.Guest == nil || !guestimage.Has(image.Guest.Capabilities, "project-restore") || !guestimage.Has(health.Capabilities, "project-restore") {
		return compute.Project{}, compute.Problem("unsupported", "Restored project verification requires project-restore in both the selected image and the running guest. Rebuild/import a capable image explicitly.")
	}
	var root protocol.ProjectFileRoot
	scope := protocol.ProjectFileScope{Root: source.Root, RootIdentity: source.RootIdentity, BootID: health.BootID, InstanceID: health.InstanceID}
	if err := projectJSON(ctx, m, driver, "restore-check", scope, &root); err != nil {
		return compute.Project{}, err
	}
	if root.Root != source.Root || root.RootIdentity != source.RootIdentity {
		return compute.Project{}, compute.Problem("project_changed", "The restored project root differs from its backup identity.")
	}
	var result compute.Project
	err = s.store.transaction(ctx, func(tx *sql.Tx) error {
		p, found, err := restoredProjectReceipt(ctx, tx, req.RequestID, hash)
		if err != nil || found {
			result = p
			return err
		}
		var reused bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM operations WHERE request_id=? UNION ALL SELECT 1 FROM task_requests WHERE request_id=?)", req.RequestID, req.RequestID).Scan(&reused); err != nil {
			return err
		}
		if reused {
			return compute.Problem("idempotency_conflict", "This request ID belongs to another compute operation.")
		}
		current, _, err := projectEnvironment(ctx, tx, req.VolumeID, req.MachineID, req.VolumeRevision)
		if err != nil {
			return err
		}
		if _, err = restoredSource(current, backupID, req.SourceProjectID); err != nil {
			return err
		}
		p, err = decodeRow[compute.Project](tx.QueryRowContext(ctx, "SELECT data FROM projects WHERE volume_id=? AND root=?", current.ID, source.Root))
		if compute.ErrorCode(err) == "not_found" {
			name := req.Name
			if name == "" {
				name = source.Name
			}
			if err := compute.ValidateResourceName(name); err != nil {
				return err
			}
			p = compute.Project{ID: compute.NewID("project"), HostID: s.store.HostID, Kind: "vm", Name: name, VolumeID: current.ID, Root: source.Root, RootIdentity: root.RootIdentity, Revision: 1, CreatedAt: time.Now().UTC()}
			_, err = tx.ExecContext(ctx, "INSERT INTO projects(id,volume_id,root,data) VALUES(?,?,?,?)", p.ID, p.VolumeID, p.Root, encode(p))
		} else if err == nil && p.RootIdentity != root.RootIdentity {
			return compute.Problem("project_changed", "The restored root was replaced after registration.")
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO project_requests(request_id,fingerprint,project_id) VALUES(?,?,?)", req.RequestID, hash, p.ID)
		result = p
		return err
	})
	return result, err
}
