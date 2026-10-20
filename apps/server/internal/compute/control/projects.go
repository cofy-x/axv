package control

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func project(ctx context.Context, q querier, id string) (compute.Project, error) {
	return decodeRow[compute.Project](q.QueryRowContext(ctx, "SELECT data FROM projects WHERE id=?", id))
}

func (s *Store) Project(ctx context.Context, id string) (compute.Project, error) {
	return project(ctx, s.db, id)
}

func (s *Store) Projects(ctx context.Context) ([]compute.Project, error) {
	return rowsJSON[compute.Project](s.db.QueryContext(ctx, "SELECT data FROM projects ORDER BY id"))
}

func protectProjectVolume(ctx context.Context, q querier, volumeID string) error {
	var exists bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE volume_id=?)", volumeID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return compute.Problem("volume_conflict", "This volume owns a registered VM project and must remain retained. Its project data cannot be deleted by a volume action.")
	}
	return nil
}

func projectRequest(ctx context.Context, q querier, req compute.RegisterProjectRequest) (compute.Project, bool, error) {
	var hash, id string
	err := q.QueryRowContext(ctx, "SELECT fingerprint,project_id FROM project_requests WHERE request_id=?", req.RequestID).Scan(&hash, &id)
	if err == nil {
		if hash != fingerprint(req) {
			return compute.Project{}, false, compute.Problem("idempotency_conflict", "This project requestId was already used with a different payload.")
		}
		p, err := project(ctx, q, id)
		return p, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return compute.Project{}, false, err
	}
	var exists bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM operations WHERE request_id=? UNION ALL SELECT 1 FROM task_requests WHERE request_id=?)", req.RequestID, req.RequestID).Scan(&exists); err != nil {
		return compute.Project{}, false, err
	}
	if exists {
		return compute.Project{}, false, compute.Problem("idempotency_conflict", "This requestId already belongs to a compute operation.")
	}
	return compute.Project{}, false, nil
}

// projectEnvironment runs under a file lease or lifecycle transaction. Active
// operations are rejected before guest I/O, including queued stop/restart work.
func projectEnvironment(ctx context.Context, q querier, volumeID, machineID string, revision int64) (compute.Volume, compute.Machine, error) {
	v, err := volume(ctx, q, volumeID)
	if err != nil {
		return v, compute.Machine{}, err
	}
	if revision < 1 || v.Revision != revision || machineID == "" || v.MachineID != machineID {
		return v, compute.Machine{}, compute.Problem("binding_changed", "The project volume attachment changed. Open an explicit new binding before accessing files.")
	}
	if !v.Retain || v.Status != "ready" || v.PendingAction != "" || v.PendingMachineID != "" || v.PendingSizeGiB != 0 {
		return v, compute.Machine{}, compute.Problem("unavailable", "The project requires a ready retained volume without pending storage changes.")
	}
	m, err := machine(ctx, q, machineID)
	if err != nil {
		return v, m, err
	}
	if m.Spec.VolumeID != v.ID || m.Spec.Provider != v.Provider || m.PendingSpec != nil {
		return v, m, compute.Problem("binding_changed", "The VM no longer has this exact project volume and configuration.")
	}
	if m.Status != "running" || m.Desired != "running" {
		return v, m, compute.Problem("unavailable", "The project's VM is unavailable. Start it explicitly; file operations are never executed on the host.")
	}
	for _, resource := range []string{v.ID, m.ID} {
		var active bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE status IN ('queued','running') AND
 (resource_id=? OR EXISTS(SELECT 1 FROM json_each(operations.data,'$.locks') WHERE value=?)))`, resource, resource).Scan(&active); err != nil {
			return v, m, err
		}
		if active {
			return v, m, compute.Problem("operation_conflict", "A lifecycle operation owns the project volume or VM. Wait for its result before accessing files.")
		}
	}
	return v, m, nil
}

func validateProjectRequest(req compute.RegisterProjectRequest) error {
	for _, value := range []string{req.RequestID, req.VolumeID, req.MachineID, req.Root, req.Name} {
		if !utf8.ValidString(value) {
			return compute.Problem("invalid_request", "Project registration requires valid UTF-8 without replacing submitted characters.")
		}
	}
	if err := compute.ValidateRequestID(req.RequestID); err != nil {
		return err
	}
	if err := compute.ValidateProjectRoot(req.Root); err != nil {
		return err
	}
	if req.Name != "" {
		if err := compute.ValidateResourceName(req.Name); err != nil {
			return err
		}
	}
	if req.VolumeID == "" || req.MachineID == "" || req.VolumeRevision < 1 {
		return compute.Problem("invalid_request", "The original volume ID, volume revision and attached machine ID are required.")
	}
	return nil
}

func (s *Store) registerProject(ctx context.Context, req compute.RegisterProjectRequest, rootIdentity string) (result compute.Project, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var exists bool
		result, exists, err = projectRequest(ctx, tx, req)
		if err != nil || exists {
			return err
		}
		v, _, err := projectEnvironment(ctx, tx, req.VolumeID, req.MachineID, req.VolumeRevision)
		if err != nil {
			return err
		}
		p, err := decodeRow[compute.Project](tx.QueryRowContext(ctx, "SELECT data FROM projects WHERE volume_id=? AND root=?", v.ID, req.Root))
		if err == nil {
			if p.RootIdentity != rootIdentity {
				return compute.Problem("project_changed", "The registered project directory was replaced. The existing project was not rebound.")
			}
		} else if compute.ErrorCode(err) != "not_found" {
			return err
		} else {
			name := req.Name
			if name == "" {
				name = path.Base(req.Root)
				if req.Root == "" {
					name = v.Name
				}
			}
			if err := compute.ValidateResourceName(name); err != nil {
				return err
			}
			p = compute.Project{ID: compute.NewID("project"), HostID: s.HostID, Kind: "vm", Name: name, VolumeID: v.ID, Root: req.Root, RootIdentity: rootIdentity, Revision: 1, CreatedAt: time.Now().UTC()}
			if _, err := tx.ExecContext(ctx, "INSERT INTO projects(id,volume_id,root,data) VALUES(?,?,?,?)", p.ID, p.VolumeID, p.Root, encode(p)); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO project_requests(request_id,fingerprint,project_id) VALUES(?,?,?)", req.RequestID, fingerprint(req), p.ID)
		result = p
		return err
	})
	return
}

func (s *Service) Projects(ctx context.Context) ([]compute.Project, error) {
	return s.store.Projects(ctx)
}

func (s *Service) Project(ctx context.Context, id string) (compute.Project, error) {
	return s.store.Project(ctx, id)
}

func (s *Service) requireProjectHost(hostID string) error {
	if strings.TrimSpace(hostID) == "" || hostID != s.store.HostID {
		return compute.Problem("binding_changed", "The original AXV host identity is required for VM project access.")
	}
	return nil
}
