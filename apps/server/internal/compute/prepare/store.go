package prepare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/sqlitemigrate"
	_ "modernc.org/sqlite"
)

var errNoChange = errors.New("preparation state unchanged")

type specification struct {
	Plan               Plan              `json:"plan"`
	OriginalCompute    json.RawMessage   `json:"originalCompute"`
	ComputeFingerprint string            `json:"computeFingerprint"`
	ComputeMode        string            `json:"computeMode"`
	Existing           bool              `json:"existing"`
	Imported           bool              `json:"imported"`
	ComputeJSON        json.RawMessage   `json:"computeJson,omitempty"`
	HostJSON           json.RawMessage   `json:"hostJson,omitempty"`
	ExpectedHostID     string            `json:"expectedHostId,omitempty"`
	LimaBinary         string            `json:"limaBinary,omitempty"`
	LimaVersion        string            `json:"limaVersion,omitempty"`
	LimaArtifact       artifact          `json:"limaArtifact"`
	ImageBundle        guestimage.Bundle `json:"imageBundle"`
	ImageSource        string            `json:"imageSource,omitempty"`
	ImageShared        bool              `json:"imageShared"`
	ImageArtifact      artifact          `json:"imageArtifact"`
}

type commitIntent struct {
	ComputeJSON json.RawMessage `json:"compute"`
	HostJSON    json.RawMessage `json:"host,omitempty"`
	HostPath    string          `json:"hostPath,omitempty"`
}

type record struct {
	Job    Job           `json:"job"`
	Spec   specification `json:"spec"`
	Intent *commitIntent `json:"intent,omitempty"`
}

type taskStore struct{ db *sql.DB }

func openStore(dir string) (*taskStore, error) {
	db, err := sql.Open("sqlite", filepath.Join(dir, "preparation.sqlite"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &taskStore{db: db}
	if _, err = db.Exec("PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err == nil {
		err = sqlitemigrate.Apply(context.Background(), db, migrationFiles, databaseApplicationID, nil)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *taskStore) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
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

func readRecord(row *sql.Row) (record, error) {
	var r record
	var data []byte
	err := row.Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return r, problem(KindNotFound, "not_found", "Preparation task was not found.")
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r)
	return r, err
}

func (s *taskStore) record(ctx context.Context, id string) (record, error) {
	return readRecord(s.db.QueryRowContext(ctx, "SELECT data FROM jobs WHERE id=?", id))
}

func writeRecord(ctx context.Context, tx *sql.Tx, r record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE jobs SET status=?,revision=?,data=?,updated_at=? WHERE id=?", r.Job.Status, r.Job.Revision, b, r.Job.UpdatedAt, r.Job.ID)
	return err
}

func (s *taskStore) mutate(ctx context.Context, id string, revision bool, fn func(*record) error) (record, error) {
	var r record
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = readRecord(tx.QueryRowContext(ctx, "SELECT data FROM jobs WHERE id=?", id))
		if err != nil {
			return err
		}
		if err = fn(&r); err != nil {
			if errors.Is(err, errNoChange) {
				return nil
			}
			return err
		}
		if revision {
			r.Job.Revision++
		}
		r.Job.UpdatedAt = now()
		return writeRecord(ctx, tx, r)
	})
	return r, err
}

func (s *taskStore) savePlan(ctx context.Context, p specification) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO plans(id,data,expires_at) VALUES(?,?,?)", p.Plan.ID, b, p.Plan.ExpiresAt)
	return err
}

func (s *taskStore) plan(ctx context.Context, id string) (specification, error) {
	var p specification
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT data FROM plans WHERE id=?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return p, problem(KindNotFound, "plan_not_found", "Review a new preparation plan before continuing.")
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(data, &p)
	return p, err
}

func existingRequest(ctx context.Context, tx *sql.Tx, id, kind, fingerprint string) (*record, error) {
	var savedKind, savedHash, jobID string
	err := tx.QueryRowContext(ctx, "SELECT kind,fingerprint,job_id FROM requests WHERE id=?", id).Scan(&savedKind, &savedHash, &jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if savedKind != kind || savedHash != fingerprint {
		return nil, problem(KindConflict, "request_conflict", "This request ID was already used for different preparation input.")
	}
	r, err := readRecord(tx.QueryRowContext(ctx, "SELECT data FROM jobs WHERE id=?", jobID))
	return &r, err
}

func (s *taskStore) request(ctx context.Context, id, kind, fingerprint string) (*record, error) {
	var found *record
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		found, err = existingRequest(ctx, tx, id, kind, fingerprint)
		return err
	})
	return found, err
}

func (s *taskStore) create(ctx context.Context, r record, fingerprint string) (record, bool, error) {
	created := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		found, err := existingRequest(ctx, tx, r.Job.RequestID, "start", fingerprint)
		if err != nil {
			return err
		}
		if found != nil {
			r = *found
			return nil
		}
		var active string
		err = tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE status IN ('queued','running') LIMIT 1").Scan(&active)
		if err == nil {
			return problem(KindConflict, "preparation_active", "Preparation task %s is already active for this profile.", active)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO jobs(id,request_id,status,revision,data,updated_at) VALUES(?,?,?,?,?,?)", r.Job.ID, r.Job.RequestID, r.Job.Status, r.Job.Revision, b, r.Job.UpdatedAt); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO requests(id,kind,fingerprint,job_id) VALUES(?,?,?,?)", r.Job.RequestID, "start", fingerprint, r.Job.ID); err != nil {
			return err
		}
		created = true
		return nil
	})
	return r, created, err
}

func (s *taskStore) action(ctx context.Context, id, kind string, request ActionRequest, fn func(*record) error) (record, bool, error) {
	var r record
	changed := false
	fingerprint := digestJSON(struct {
		ID      string
		Request ActionRequest
	}{id, request})
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		found, err := existingRequest(ctx, tx, request.RequestID, kind, fingerprint)
		if err != nil {
			return err
		}
		if found != nil {
			r = *found
			return nil
		}
		if kind == "retry" {
			var active string
			activeErr := tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE status IN ('queued','running') AND id != ? LIMIT 1", id).Scan(&active)
			if activeErr == nil {
				return problem(KindConflict, "preparation_active", "Preparation task %s is already active.", active)
			}
			if !errors.Is(activeErr, sql.ErrNoRows) {
				return activeErr
			}
		}
		r, err = readRecord(tx.QueryRowContext(ctx, "SELECT data FROM jobs WHERE id=?", id))
		if err != nil {
			return err
		}
		if r.Job.Revision != request.ExpectedRevision {
			return problem(KindConflict, "revision_conflict", "Preparation changed. Refresh the task before continuing.")
		}
		if err = fn(&r); err != nil {
			return err
		}
		r.Job.Revision++
		r.Job.UpdatedAt = now()
		if err = writeRecord(ctx, tx, r); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO requests(id,kind,fingerprint,job_id) VALUES(?,?,?,?)", request.RequestID, kind, fingerprint, id); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return r, changed, err
}

func (s *taskStore) recent(ctx context.Context) (*Job, *Job, error) {
	var active, latest *Job
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM jobs ORDER BY updated_at DESC LIMIT 100")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		var r record
		if err = rows.Scan(&data); err != nil {
			return nil, nil, err
		}
		if err = json.Unmarshal(data, &r); err != nil {
			return nil, nil, err
		}
		if latest == nil {
			j := r.Job
			latest = &j
		}
		if r.Job.Status == "queued" || r.Job.Status == "running" {
			j := r.Job
			active = &j
		}
	}
	return active, latest, rows.Err()
}

func (s *taskStore) interrupt(ctx context.Context) error {
	var ids []string
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM jobs WHERE status IN ('queued','running')")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, err = s.mutate(ctx, id, true, func(r *record) error {
			r.Job.Status, r.Job.CanRetry, r.Job.CanCancel = "interrupted", true, false
			r.Job.Error = &Failure{Code: "interrupted", Message: "Preparation was interrupted. Retry continues this task after checking completed work.", Retryable: true}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
