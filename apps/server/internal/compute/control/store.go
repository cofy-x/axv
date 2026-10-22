package control

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/ubuntusources"
	_ "modernc.org/sqlite"
)

type Store struct {
	db            *sql.DB
	HostID        string
	directory     string
	projectLeases resourceLeases
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "compute.sqlite"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, directory: dir}
	if _, err = db.Exec("PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err == nil {
		err = s.migrate()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func decodeRow[T any](row *sql.Row) (T, error) {
	var result T
	var raw string
	if err := row.Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return result, compute.Problem("not_found", "Resource not found.")
	} else if err != nil {
		return result, err
	}
	err := json.Unmarshal([]byte(raw), &result)
	return result, err
}

func machine(ctx context.Context, q querier, id string) (compute.Machine, error) {
	return decodeRow[compute.Machine](q.QueryRowContext(ctx, "SELECT data FROM machines WHERE id=?", id))
}

func volume(ctx context.Context, q querier, id string) (compute.Volume, error) {
	return decodeRow[compute.Volume](q.QueryRowContext(ctx, "SELECT data FROM volumes WHERE id=?", id))
}

func (s *Store) Machine(ctx context.Context, id string) (compute.Machine, error) {
	return machine(ctx, s.db, id)
}
func (s *Store) Volume(ctx context.Context, id string) (compute.Volume, error) {
	return volume(ctx, s.db, id)
}
func (s *Store) Operation(ctx context.Context, id string) (operationRecord, error) {
	return decodeRow[operationRecord](s.db.QueryRowContext(ctx, "SELECT data FROM operations WHERE id=?", id))
}

// OperationByRequestID recovers accepted intent even after it leaves the activity list.
func (s *Store) OperationByRequestID(ctx context.Context, requestID string) (operationRecord, error) {
	if err := compute.ValidateRequestID(requestID); err != nil {
		return operationRecord{}, err
	}
	return decodeRow[operationRecord](s.db.QueryRowContext(ctx, "SELECT data FROM operations WHERE request_id=?", requestID))
}

func rowsJSON[T any](rows *sql.Rows, err error) ([]T, error) {
	result := []T{}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var item T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Machines(ctx context.Context) ([]compute.Machine, error) {
	return rowsJSON[compute.Machine](s.db.QueryContext(ctx, "SELECT data FROM machines WHERE status != 'deleted' ORDER BY updated_at DESC"))
}

func (s *Store) Volumes(ctx context.Context) ([]compute.Volume, error) {
	return rowsJSON[compute.Volume](s.db.QueryContext(ctx, "SELECT data FROM volumes ORDER BY id"))
}

func (s *Store) Operations(ctx context.Context) ([]operationRecord, error) {
	return rowsJSON[operationRecord](s.db.QueryContext(ctx, `SELECT data FROM operations
WHERE status IN ('queued','running') OR id IN
 (SELECT id FROM operations WHERE status NOT IN ('queued','running') ORDER BY created_at DESC LIMIT 200)
ORDER BY created_at DESC,id`))
}

func encode(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	} // Only fixed, JSON-compatible domain structs are passed.
	return string(b)
}

func fingerprint(v any) string {
	sum := sha256.Sum256([]byte(encode(v)))
	return hex.EncodeToString(sum[:])
}

func replay(ctx context.Context, tx *sql.Tx, requestID, hash string) (operationRecord, bool, error) {
	var projectRequest bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM project_requests WHERE request_id=? UNION ALL SELECT 1 FROM task_requests WHERE request_id=?)", requestID, requestID).Scan(&projectRequest); err != nil {
		return operationRecord{}, false, err
	}
	if projectRequest {
		return operationRecord{}, false, compute.Problem("idempotency_conflict", "This requestId already belongs to a project registration.")
	}
	var raw, savedHash string
	err := tx.QueryRowContext(ctx, "SELECT data,fingerprint FROM operations WHERE request_id=?", requestID).Scan(&raw, &savedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return operationRecord{}, false, nil
	}
	if err != nil {
		return operationRecord{}, false, err
	}
	if savedHash != hash {
		return operationRecord{}, false, compute.Problem("idempotency_conflict", "This requestId was already used for a different request.")
	}
	var op operationRecord
	err = json.Unmarshal([]byte(raw), &op)
	return op, true, err
}

func checkMachineNames(ctx context.Context, tx *sql.Tx, m compute.Machine) error {
	if m.Status != "deleted" {
		pendingName := m.Spec.Name
		if m.PendingSpec != nil {
			pendingName = m.PendingSpec.Name
		}
		// Current and pending names share one namespace, including failed edits.
		var occupied bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM machines
WHERE status != 'deleted' AND id != ? AND
 (json_extract(data,'$.spec.name') IN (?,?) OR json_extract(data,'$.pendingSpec.name') IN (?,?)))`,
			m.ID, m.Spec.Name, pendingName, m.Spec.Name, pendingName).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return compute.Problem("name_conflict", "Another VM already uses or reserves this name. Choose a different name.")
		}
	}
	return nil
}

func writeMachine(ctx context.Context, tx *sql.Tx, m compute.Machine) error {
	// Enforce claims on every write, including observation and finalization.
	if err := checkMachineNames(ctx, tx, m); err != nil {
		return err
	}
	r := m.Spec.Resources
	if m.PendingSpec != nil {
		r.CPUs = max(r.CPUs, m.PendingSpec.Resources.CPUs)
		r.MemoryMiB = max(r.MemoryMiB, m.PendingSpec.Resources.MemoryMiB)
		r.DiskGiB = max(r.DiskGiB, m.PendingSpec.Resources.DiskGiB)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO machines(id,data,status,desired,reserved,cpus,memory_mib,disk_gib,slot,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data,status=excluded.status,
desired=excluded.desired,reserved=excluded.reserved,cpus=excluded.cpus,memory_mib=excluded.memory_mib,
disk_gib=excluded.disk_gib,updated_at=excluded.updated_at`,
		m.ID, encode(m), m.Status, m.Desired, m.Reserved, r.CPUs, r.MemoryMiB, r.DiskGiB, m.Slot, m.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func writeVolume(ctx context.Context, tx *sql.Tx, v compute.Volume) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO volumes(id,data,size_gib,machine_id,status) VALUES(?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET data=excluded.data,size_gib=excluded.size_gib,machine_id=excluded.machine_id,status=excluded.status`, v.ID, encode(v), max(v.SizeGiB, v.PendingSizeGiB)+v.TransientGiB, v.MachineID, v.Status)
	return err
}

func resourceActive(ctx context.Context, tx *sql.Tx, resource string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE status IN ('queued','running') AND
 (resource_id=? OR EXISTS(SELECT 1 FROM json_each(operations.data,'$.locks') WHERE value=?))`, resource, resource).Scan(&count)
	return count != 0, err
}

func insertOperation(ctx context.Context, tx *sql.Tx, op operationRecord, hash, resource string, dependencies ...string) error {
	op.ResourceID = resource
	seen := map[string]bool{}
	for _, id := range append([]string{resource}, dependencies...) {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		active, err := resourceActive(ctx, tx, id)
		if err != nil {
			return err
		}
		if active {
			return compute.Problem("operation_conflict", "An operation already owns this VM, volume or artifact.")
		}
		op.Locks = append(op.Locks, id)
	}
	if op.ResourceKind == "" {
		if op.MachineID != "" {
			op.ResourceKind = "machine"
		} else if op.VolumeID != "" {
			op.ResourceKind = "volume"
		}
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO operations(id,request_id,resource_id,fingerprint,status,data,created_at) VALUES(?,?,?,?,?,?,?)", op.ID, op.RequestID, resource, hash, op.Status, encode(op), op.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func newOperation(requestID, machineID, action string) operationRecord {
	now := time.Now().UTC()
	return operationRecord{Operation: compute.Operation{ID: compute.NewID("op"), RequestID: requestID, MachineID: machineID, Action: action, Status: "queued", CreatedAt: now, UpdatedAt: now}}
}

func admit(ctx context.Context, tx *sql.Tx, c compute.Capacity, additional compute.Resources, addMachine bool) error {
	var used compute.Resources
	var machines, volumes int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN reserved THEN cpus ELSE 0 END),0),
COALESCE(SUM(CASE WHEN reserved THEN memory_mib ELSE 0 END),0),COALESCE(SUM(disk_gib),0),count(*)
FROM machines WHERE status != 'deleted'`).Scan(&used.CPUs, &used.MemoryMiB, &used.DiskGiB, &machines); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(size_gib),0) FROM volumes").Scan(&volumes); err != nil {
		return err
	}
	var artifacts int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(disk_gib),0) FROM catalog").Scan(&artifacts); err != nil {
		return err
	}
	if (addMachine && machines >= int64(c.Machines)) || used.CPUs+additional.CPUs > c.CPUs || used.MemoryMiB+additional.MemoryMiB > c.MemoryMiB || used.DiskGiB+volumes+artifacts+additional.DiskGiB > c.DiskGiB {
		return compute.Problem("capacity_exceeded", "The request exceeds reserved host capacity. Stop machines or release unused volumes before retrying.")
	}
	return nil
}

func (s *Store) Create(ctx context.Context, config compute.Config, req compute.CreateRequest) (result compute.Result, err error) {
	req.Spec.Name = compute.NormalizeMachineName(req.Spec.Name)
	if err = compute.ValidateRequestID(req.RequestID); err != nil {
		return
	}
	err = s.resourceTransaction(ctx, []string{req.Spec.VolumeID}, func(tx *sql.Tx) error {
		op, exists, err := replay(ctx, tx, req.RequestID, fingerprint(req))
		if err != nil {
			return err
		}
		if exists {
			result.Operation = op.Operation
			result.Machine, err = machine(ctx, tx, op.MachineID)
			return err
		}
		if err := config.ValidateRequest(req); err != nil {
			return err
		}
		if err := checkMachineNames(ctx, tx, compute.Machine{Spec: req.Spec}); err != nil {
			return err
		}
		// The service's catalog snapshot may precede an image deletion. Check
		// availability in the same transaction that publishes its first user.
		image, err := catalog[compute.ImageRecord](ctx, tx, req.Spec.ImageID, "image")
		if err != nil {
			return err
		}
		if image.Status != "configured" && image.Status != "ready" {
			return compute.Problem("image_conflict", "The selected image is no longer available. Refresh the image catalog.")
		}
		now := time.Now().UTC()
		m := compute.Machine{ID: compute.NewID("vm"), HostID: s.HostID, Spec: req.Spec, Revision: 1, Desired: "stopped", Status: "queued", Reserved: req.Start, CreatedAt: now, UpdatedAt: now}
		if req.Start {
			m.Desired = "running"
		}
		if m.Spec.TTLSeconds > 0 {
			expires := now.Add(time.Duration(m.Spec.TTLSeconds) * time.Second)
			m.ExpiresAt = &expires
		}
		v := compute.Volume{ID: compute.NewID("vol"), Name: m.Spec.Name, Revision: 1, Provider: m.Spec.Provider, SizeGiB: m.Spec.ProjectGiB, Retain: m.Spec.RetainVolume, MachineID: m.ID, Status: "ready", CreatedAt: now}
		additional := compute.Resources{DiskGiB: m.Spec.Resources.DiskGiB + v.SizeGiB}
		if m.Reserved {
			additional.CPUs = m.Spec.Resources.CPUs
			additional.MemoryMiB = m.Spec.Resources.MemoryMiB
		}
		if m.Spec.VolumeID != "" {
			v, err = volume(ctx, tx, m.Spec.VolumeID)
			if err != nil {
				return err
			}
			if v.IsProjectBackup() {
				return compute.Problem("volume_conflict", "Backups cannot be attached to VMs. Restore into a new volume first.")
			}
			if v.MachineID != "" || v.Status != "ready" || v.Provider != m.Spec.Provider || v.SizeGiB != m.Spec.ProjectGiB || v.Retain != m.Spec.RetainVolume {
				return compute.Problem("volume_conflict", "The volume must be detached, ready, and match this provider, size and retention policy.")
			}
			v.MachineID = m.ID
			v.Revision++
			additional.DiskGiB = m.Spec.Resources.DiskGiB
		}
		if err = admit(ctx, tx, config.Capacity, additional, true); err != nil {
			return err
		}
		m.Spec.VolumeID = v.ID
		for slot := 1; slot <= 100; slot++ {
			var occupied int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM machines WHERE slot=? AND status!='deleted'", slot).Scan(&occupied); err != nil {
				return err
			}
			if occupied == 0 {
				m.Slot = slot
				break
			}
		}
		if m.Slot == 0 {
			return compute.Problem("capacity_exceeded", "No network slot is available.")
		}
		op = newOperation(req.RequestID, m.ID, "create")
		if err = writeMachine(ctx, tx, m); err != nil {
			return err
		}
		if err = writeVolume(ctx, tx, v); err != nil {
			return err
		}
		if err = insertOperation(ctx, tx, op, fingerprint(req), m.ID, v.ID, m.Spec.ImageID); err != nil {
			return err
		}
		result = compute.Result{Machine: m, Operation: op.Operation}
		return nil
	})
	return
}

func (s *Store) Action(ctx context.Context, c compute.Capacity, id string, req compute.ActionRequest) (result compute.Result, err error) {
	if req.Action == "package-source" {
		if err = ubuntusources.Validate(req.PackageSource); err != nil {
			return result, compute.Problem("invalid_request", "%v", err)
		}
	} else if req.PackageSource != "" {
		return result, compute.Problem("invalid_request", "packageSource is only valid for package-source actions.")
	}
	if err = compute.ValidateRequestID(req.RequestID); err != nil {
		return
	}
	hash := fingerprint(struct {
		ID      string
		Request compute.ActionRequest
	}{id, req})
	err = s.resourceTransaction(ctx, []string{id}, func(tx *sql.Tx) error {
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
			return compute.Problem("revision_conflict", "Machine changed. Refresh before submitting another action.")
		}
		if m.Status == "deleted" {
			return compute.Problem("unavailable", "Machine has been deleted.")
		}
		switch req.Action {
		case "package-source":
			if m.Spec.Provider != "lima" {
				return compute.Problem("unsupported", "Package source maintenance currently supports Lima only; use the explicit offline image builder for Firecracker.")
			}
			if m.Desired != "running" || m.Status != "running" || m.PendingSpec != nil {
				return compute.Problem("unavailable", "Package source maintenance requires a running VM without pending configuration.")
			}
		case "start", "restart":
			if m.PendingSpec != nil {
				return compute.Problem("configuration_conflict", "Apply the pending configuration before starting the VM.")
			}
			if m.ExpiresAt != nil && !time.Now().Before(*m.ExpiresAt) {
				return compute.Problem("unavailable", "The task lease has expired.")
			}
			v, volumeErr := volume(ctx, tx, m.Spec.VolumeID)
			if volumeErr != nil || v.Status != "ready" || v.MachineID != m.ID {
				return compute.Problem("volume_conflict", "Attach a ready project volume before starting this VM.")
			}
			if !m.Reserved {
				if err = admit(ctx, tx, c, compute.Resources{CPUs: m.Spec.Resources.CPUs, MemoryMiB: m.Spec.Resources.MemoryMiB}, false); err != nil {
					return err
				}
			}
			m.Desired, m.Reserved = "running", true
		case "stop", "force-stop":
			m.Desired = "stopped"
		case "delete":
			var pending int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM volumes WHERE json_extract(data,'$.pendingMachineId')=? AND json_extract(data,'$.pendingAction')='attach-volume'", m.ID).Scan(&pending); err != nil {
				return err
			}
			if pending != 0 {
				return compute.Problem("volume_conflict", "Resolve the pending project-volume attachment before deleting this VM.")
			}
			m.Desired = "deleted"
		default:
			return compute.Problem("invalid_request", "Action must be start, restart, stop, force-stop, delete or package-source.")
		}
		op = newOperation(req.RequestID, m.ID, req.Action)
		op.PackageSource = req.PackageSource
		if err = insertOperation(ctx, tx, op, hash, m.ID, m.Spec.VolumeID); err != nil {
			return err
		}
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

func (s *Store) DeleteVolume(ctx context.Context, id, requestID string, revision int64) (op operationRecord, err error) {
	if err = compute.ValidateRequestID(requestID); err != nil {
		return
	}
	hash := fingerprint([]any{"delete-volume", id, revision})
	err = s.resourceTransaction(ctx, []string{id}, func(tx *sql.Tx) error {
		var exists bool
		op, exists, err = replay(ctx, tx, requestID, hash)
		if err != nil || exists {
			return err
		}
		v, err := volume(ctx, tx, id)
		if err != nil {
			return err
		}
		if v.MachineID != "" || v.PendingMachineID != "" {
			return compute.Problem("volume_conflict", "Resolve any pending attachment and detach the project volume before deleting it.")
		}
		if revision != v.Revision {
			return compute.Problem("revision_conflict", "The volume changed. Refresh before deleting it.")
		}
		if err := protectProjectVolume(ctx, tx, id); err != nil {
			return err
		}
		v.Status = "deleting"
		v.Revision++
		op = newOperation(requestID, "", "delete-volume")
		op.VolumeID = id
		if err = insertOperation(ctx, tx, op, hash, id); err != nil {
			return err
		}
		return writeVolume(ctx, tx, v)
	})
	return
}
