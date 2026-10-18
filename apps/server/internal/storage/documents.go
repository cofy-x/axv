package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type ProjectRef struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	RootPath     string `json:"rootPath"`
	RootIdentity string `json:"-"`
	CreatedAt    string `json:"createdAt"`
}

func projectScan(row interface{ Scan(...any) error }) (ProjectRef, error) {
	var p ProjectRef
	err := row.Scan(&p.ID, &p.Kind, &p.Name, &p.RootPath, &p.RootIdentity, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = problem("not_found", "The registered project does not exist.")
	}
	return p, err
}

func (s *Store) Projects(ctx context.Context) ([]ProjectRef, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,kind,name,root_path,root_identity,created_at FROM projects ORDER BY name COLLATE NOCASE,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProjectRef{}
	for rows.Next() {
		p, err := projectScan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (s *Store) Project(ctx context.Context, projectID string) (ProjectRef, error) {
	return projectScan(s.db.QueryRowContext(ctx, "SELECT id,kind,name,root_path,root_identity,created_at FROM projects WHERE id=?", projectID))
}

// RegisterProject receives a root identity verified by the host-files owner.
func (s *Store) RegisterProject(ctx context.Context, name, root, identity string) (ProjectRef, error) {
	if err := validName(name); err != nil {
		return ProjectRef{}, err
	}
	if root == "" || identity == "" {
		return ProjectRef{}, problem("invalid_project", "Choose a verified project directory.")
	}
	var p ProjectRef
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = projectScan(tx.QueryRowContext(ctx, "SELECT id,kind,name,root_path,root_identity,created_at FROM projects WHERE root_path=? AND root_identity=?", root, identity))
		if err == nil {
			return nil
		}
		var e *Error
		if !errors.As(err, &e) || e.Code != "not_found" {
			return err
		}
		p = ProjectRef{ID: id(), Kind: "host", Name: name, RootPath: root, RootIdentity: identity, CreatedAt: now()}
		_, err = tx.ExecContext(ctx, "INSERT INTO projects(id,kind,name,root_path,root_identity,created_at) VALUES(?,?,?,?,?,?)", p.ID, p.Kind, p.Name, p.RootPath, p.RootIdentity, p.CreatedAt)
		return err
	})
	return p, err
}

type DocumentSnapshot struct {
	ID         string `json:"id"`
	DatabaseID string `json:"databaseId"`
	EntryID    string `json:"entryId"`
	Revision   int    `json:"revision"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	SHA256     string `json:"sha256"`
	CreatedAt  string `json:"createdAt"`
}

// SnapshotSummary intentionally excludes document bodies from list responses.
type SnapshotSummary struct {
	ID         string `json:"id"`
	DatabaseID string `json:"databaseId"`
	EntryID    string `json:"entryId"`
	Revision   int    `json:"revision"`
	Name       string `json:"name"`
	SHA256     string `json:"sha256"`
	CreatedAt  string `json:"createdAt"`
}

const snapshotColumns = "id,database_id,entry_id,revision,name,content,sha256,created_at"

func snapshotScan(row interface{ Scan(...any) error }) (DocumentSnapshot, error) {
	var d DocumentSnapshot
	err := row.Scan(&d.ID, &d.DatabaseID, &d.EntryID, &d.Revision, &d.Name, &d.Content, &d.SHA256, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = problem("not_found", "The document snapshot does not exist.")
	}
	return d, err
}

// Snapshot returns immutable content, including after the source is removed.
func (s *Store) Snapshot(ctx context.Context, snapshotID string) (DocumentSnapshot, error) {
	return snapshotScan(s.db.QueryRowContext(ctx, "SELECT "+snapshotColumns+" FROM document_snapshots WHERE id=?", snapshotID))
}

func (s *Store) Snapshots(ctx context.Context, entryID string) ([]SnapshotSummary, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,database_id,entry_id,revision,name,sha256,created_at FROM document_snapshots WHERE entry_id=? ORDER BY revision DESC LIMIT 100", entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SnapshotSummary{}
	for rows.Next() {
		var d SnapshotSummary
		err := rows.Scan(&d.ID, &d.DatabaseID, &d.EntryID, &d.Revision, &d.Name, &d.SHA256, &d.CreatedAt)
		if err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	return items, rows.Err()
}

type DocumentOrigin struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	CreatedAt string `json:"createdAt"`
}

func (s *Store) DocumentOrigins(ctx context.Context, entryID string) ([]DocumentOrigin, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT project_id,path,sha256,created_at FROM document_origins WHERE entry_id=?", entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DocumentOrigin{}
	for rows.Next() {
		var o DocumentOrigin
		if err = rows.Scan(&o.ProjectID, &o.Path, &o.SHA256, &o.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, o)
	}
	return items, rows.Err()
}

func (s *Store) CreateSnapshot(ctx context.Context, entryID string, expectedRevision int) (DocumentSnapshot, error) {
	var d DocumentSnapshot
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		e, err := get(ctx, tx, entryID)
		if err != nil {
			return err
		}
		if e.Kind != "file" || !e.Editable || e.TrashedAt != "" {
			return problem("unavailable", "Choose an available text document.")
		}
		if expectedRevision != e.Revision {
			return problem("revision_conflict", "The document changed. Save and review its current revision before creating a snapshot.")
		}
		if err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='databaseId'").Scan(&d.DatabaseID); err != nil {
			return err
		}
		existing, err := snapshotScan(tx.QueryRowContext(ctx, "SELECT "+snapshotColumns+" FROM document_snapshots WHERE database_id=? AND entry_id=? AND revision=?", d.DatabaseID, entryID, e.Revision))
		if err == nil {
			d = existing
			return nil
		}
		var se *Error
		if !errors.As(err, &se) || se.Code != "not_found" {
			return err
		}
		if err = tx.QueryRowContext(ctx, "SELECT content FROM entries WHERE id=?", entryID).Scan(&d.Content); err != nil {
			return err
		}
		d.ID, d.EntryID, d.Revision, d.Name, d.CreatedAt = id(), entryID, e.Revision, e.Name, now()
		digest := sha256.Sum256([]byte(d.Content))
		d.SHA256 = hex.EncodeToString(digest[:])
		_, err = tx.ExecContext(ctx, "INSERT INTO document_snapshots("+snapshotColumns+") VALUES(?,?,?,?,?,?,?,?)", d.ID, d.DatabaseID, d.EntryID, d.Revision, d.Name, d.Content, d.SHA256, d.CreatedAt)
		return err
	})
	return d, err
}

type DocumentLink struct {
	ID             string `json:"id"`
	EntryID        string `json:"entryId"`
	Kind           string `json:"kind"`
	ProjectID      string `json:"projectId,omitempty"`
	HostID         string `json:"hostId,omitempty"`
	MachineID      string `json:"machineId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	CreatedAt      string `json:"createdAt"`
}

const linkColumns = "id,entry_id,kind,project_id,host_id,machine_id,conversation_id,created_at"

func linkScan(row interface{ Scan(...any) error }) (DocumentLink, error) {
	var l DocumentLink
	err := row.Scan(&l.ID, &l.EntryID, &l.Kind, &l.ProjectID, &l.HostID, &l.MachineID, &l.ConversationID, &l.CreatedAt)
	return l, err
}

func validReference(value string) bool {
	return value != "" && len(value) <= 4096 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func (s *Store) AddDocumentLink(ctx context.Context, entryID string, in DocumentLink) (DocumentLink, error) {
	var l DocumentLink
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		e, err := get(ctx, tx, entryID)
		if err != nil {
			return err
		}
		if e.Kind != "file" || e.TrashedAt != "" {
			return problem("unavailable", "Choose an available document.")
		}
		switch in.Kind {
		case "project":
			if !validReference(in.ProjectID) || in.HostID != "" || in.MachineID != "" || in.ConversationID != "" {
				return problem("invalid_link", "Choose exactly one project.")
			}
			if _, err = projectScan(tx.QueryRowContext(ctx, "SELECT id,kind,name,root_path,root_identity,created_at FROM projects WHERE id=?", in.ProjectID)); err != nil {
				return err
			}
		case "machine":
			if !validReference(in.HostID) || !validReference(in.MachineID) || in.ProjectID != "" || in.ConversationID != "" {
				return problem("invalid_link", "Choose a host and machine identity.")
			}
		case "conversation":
			if !validReference(in.ConversationID) || in.ProjectID != "" || in.HostID != "" || in.MachineID != "" {
				return problem("invalid_link", "Choose exactly one conversation.")
			}
			var exists int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM codex_sessions WHERE id=?", in.ConversationID).Scan(&exists); err != nil {
				return err
			}
			if exists != 1 {
				return problem("not_found", "The conversation does not exist.")
			}
		default:
			return problem("invalid_link", "Choose project, machine or conversation.")
		}
		l = in
		l.ID, l.EntryID, l.CreatedAt = id(), entryID, now()
		_, err = tx.ExecContext(ctx, "INSERT INTO document_links("+linkColumns+") VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(entry_id,kind,project_id,host_id,machine_id,conversation_id) DO NOTHING", l.ID, l.EntryID, l.Kind, l.ProjectID, l.HostID, l.MachineID, l.ConversationID, l.CreatedAt)
		if err != nil {
			return err
		}
		l, err = linkScan(tx.QueryRowContext(ctx, "SELECT "+linkColumns+" FROM document_links WHERE entry_id=? AND kind=? AND project_id=? AND host_id=? AND machine_id=? AND conversation_id=?", entryID, in.Kind, in.ProjectID, in.HostID, in.MachineID, in.ConversationID))
		return err
	})
	return l, err
}

func (s *Store) DocumentLinks(ctx context.Context, filter DocumentLink) ([]DocumentLink, error) {
	where, args := "1=1", []any{}
	for _, p := range []struct{ column, value string }{{"entry_id", filter.EntryID}, {"project_id", filter.ProjectID}, {"host_id", filter.HostID}, {"machine_id", filter.MachineID}, {"conversation_id", filter.ConversationID}} {
		if p.value != "" {
			where += " AND " + p.column + "=?"
			args = append(args, p.value)
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+linkColumns+" FROM document_links WHERE "+where+" ORDER BY created_at,id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DocumentLink{}
	for rows.Next() {
		l, err := linkScan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, rows.Err()
}

func (s *Store) DeleteDocumentLink(ctx context.Context, linkID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM document_links WHERE id=?", linkID)
	return err
}

type DocumentTransfer struct {
	RequestID      string
	PayloadHash    string
	Kind           string
	ProjectID      string
	Path           string
	SHA256         string
	StagePath      string
	ParentIdentity string
	Result         json.RawMessage
}

func transfer(ctx context.Context, q queryer, requestID, payloadHash string) (DocumentTransfer, bool, error) {
	var t DocumentTransfer
	var result string
	err := q.QueryRowContext(ctx, "SELECT request_id,payload_hash,kind,project_id,path,sha256,stage_path,parent_identity,result FROM document_transfers WHERE request_id=?", requestID).Scan(&t.RequestID, &t.PayloadHash, &t.Kind, &t.ProjectID, &t.Path, &t.SHA256, &t.StagePath, &t.ParentIdentity, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	if err != nil {
		return t, false, err
	}
	if t.PayloadHash != payloadHash {
		return t, true, problem("idempotency_conflict", "This request ID belongs to another transfer.")
	}
	t.Result = json.RawMessage(result)
	return t, true, nil
}

func (s *Store) DocumentTransfer(ctx context.Context, requestID, payloadHash string) (DocumentTransfer, bool, error) {
	return transfer(ctx, s.db, requestID, payloadHash)
}

// DocumentTransferLockPath keeps export coordination beside the canonical
// desktop database, shared by independent service instances and path aliases.
func (s *Store) DocumentTransferLockPath(ctx context.Context) (string, error) {
	var filename string
	if err := s.db.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&filename); err != nil {
		return "", err
	}
	if filename == "" {
		return "", problem("file_unavailable", "Export coordination requires a file-backed desktop database.")
	}
	canonical, err := filepath.EvalSymlinks(filename)
	if err != nil {
		return "", problem("file_unavailable", "The desktop database location could not be verified for export coordination.")
	}
	return canonical + ".document-transfers.lock", nil
}

func insertTransfer(ctx context.Context, tx *sql.Tx, t DocumentTransfer) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO document_transfers(request_id,payload_hash,kind,project_id,path,sha256,stage_path,parent_identity,result,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)", t.RequestID, t.PayloadHash, t.Kind, t.ProjectID, t.Path, t.SHA256, t.StagePath, t.ParentIdentity, string(t.Result), now())
	return err
}

func (s *Store) ReserveExport(ctx context.Context, t DocumentTransfer) (DocumentTransfer, error) {
	var result DocumentTransfer
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		old, found, err := transfer(ctx, tx, t.RequestID, t.PayloadHash)
		if err != nil {
			return err
		}
		if found {
			result = old
			return nil
		}
		t.StagePath = ".axv-export-" + id()
		result = t
		return insertTransfer(ctx, tx, t)
	})
	return result, err
}

func (s *Store) CompleteExport(ctx context.Context, t DocumentTransfer, result json.RawMessage) error {
	if len(result) == 0 || !json.Valid(result) {
		return problem("transfer_unresolved", "The export result could not be recorded.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		old, found, err := transfer(ctx, tx, t.RequestID, t.PayloadHash)
		if err != nil {
			return err
		}
		if !found || old.Kind != "export" {
			return problem("not_found", "The reserved export request does not exist.")
		}
		if len(old.Result) > 0 {
			if bytes.Equal(old.Result, result) {
				return nil
			}
			return problem("transfer_unresolved", "This export already has a recorded outcome. Retry its original request to recover that result.")
		}
		_, err = tx.ExecContext(ctx, "UPDATE document_transfers SET result=? WHERE request_id=? AND payload_hash=? AND result=''", string(result), t.RequestID, t.PayloadHash)
		return err
	})
}

// ImportProjectFile records a receipt and creates the independent copy atomically.
func (s *Store) ImportProjectFile(ctx context.Context, t DocumentTransfer, parentID, name string, data []byte) (json.RawMessage, error) {
	var result json.RawMessage
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		old, found, err := transfer(ctx, tx, t.RequestID, t.PayloadHash)
		if err != nil {
			return err
		}
		if found {
			result = old.Result
			return nil
		}
		if len(data) > MaxFileSize {
			return problem("file_too_large", "Files must be no larger than 10 MiB.")
		}
		if err = parent(ctx, tx, parentID); err != nil {
			return err
		}
		resolved, err := uniqueName(ctx, tx, parentID, name, "", true)
		if err != nil {
			return err
		}
		e := makeEntry(parentID, resolved, "file", data)
		if err = insert(ctx, tx, e, data); err != nil {
			return err
		}
		result, err = json.Marshal(map[string]any{"entry": e})
		if err != nil {
			return err
		}
		t.Result = result
		if _, err = tx.ExecContext(ctx, "INSERT INTO document_links("+linkColumns+") VALUES(?,?,?,?,?,?,?,?)", id(), e.ID, "project", t.ProjectID, "", "", "", now()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO document_origins(entry_id,project_id,path,sha256,created_at) VALUES(?,?,?,?,?)", e.ID, t.ProjectID, t.Path, t.SHA256, now()); err != nil {
			return err
		}
		return insertTransfer(ctx, tx, t)
	})
	return result, err
}
