package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const columns = `id,parent_id,name,kind,mime,size,revision,created_at,updated_at,trashed_at,editable,previewable,system,trash_batch`

func scan(row interface{ Scan(...any) error }) (Entry, error) {
	var e Entry
	err := row.Scan(&e.ID, &e.ParentID, &e.Name, &e.Kind, &e.MIME, &e.Size, &e.Revision, &e.CreatedAt, &e.UpdatedAt, &e.TrashedAt, &e.Editable, &e.Previewable, &e.System, &e.TrashBatch)
	return e, err
}

func get(ctx context.Context, q queryer, id string) (Entry, error) {
	e, err := scan(q.QueryRowContext(ctx, "SELECT "+columns+" FROM entries WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = problem("not_found", "The item no longer exists.")
	}
	return e, err
}

func list(ctx context.Context, q queryer) ([]Entry, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+columns+" FROM entries ORDER BY name COLLATE NOCASE,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *Store) List(ctx context.Context) ([]Entry, error) { return list(ctx, s.db) }

func (s *Store) Get(ctx context.Context, id string) (Entry, []byte, error) {
	var e Entry
	var content []byte
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		e, err = get(ctx, tx, id)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT content FROM entries WHERE id=?", id).Scan(&content)
	})
	return e, content, err
}

func validName(name string) error {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, "/\\") || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return problem("invalid_name", "Use a name of 1–255 bytes without slashes or control characters.")
	}
	return nil
}

func parent(ctx context.Context, tx *sql.Tx, id string) error {
	e, err := get(ctx, tx, id)
	if err != nil {
		return err
	}
	if e.Kind != "folder" || e.TrashedAt != "" {
		return problem("invalid_parent", "Choose an available folder.")
	}
	return nil
}

func uniqueName(ctx context.Context, tx *sql.Tx, parentID, name, exceptID string, rename bool) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	original := name
	for n := 2; n < 100000; n++ {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM entries WHERE parent_id=? AND name=? COLLATE NOCASE AND trashed_at='' AND id<>?", parentID, name, exceptID).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return name, nil
		}
		if !rename {
			return "", problem("name_conflict", "An item with that name already exists in this folder.")
		}
		ext := path.Ext(original)
		base := strings.TrimSuffix(original, ext)
		suffix := fmt.Sprintf(" (%d)%s", n, ext)
		for len(base)+len(suffix) > 255 && len(base) > 0 {
			_, size := utf8.DecodeLastRuneInString(base)
			base = base[:len(base)-size]
		}
		name = base + suffix
	}
	return "", problem("name_conflict", "Too many items have that name.")
}

func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func classify(data []byte) (string, bool, bool) {
	mime := http.DetectContentType(data)
	preview := mime == "image/png" || mime == "image/jpeg" || mime == "image/webp"
	text := utf8.Valid(data) && !bytes.ContainsRune(data, 0) && strings.IndexFunc(string(data), func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\f' }) < 0
	if text {
		mime = "text/plain; charset=utf-8"
	}
	return mime, text && len(data) <= MaxTextSize, preview
}

func makeEntry(parentID, name, kind string, data []byte) Entry {
	e := Entry{ID: id(), ParentID: parentID, Name: name, Kind: kind, Revision: 1, CreatedAt: now(), UpdatedAt: now()}
	if kind == "file" {
		e.MIME, e.Editable, e.Previewable = classify(data)
		e.Size = len(data)
	}
	return e
}

func insert(ctx context.Context, tx *sql.Tx, e Entry, data []byte) error {
	if data == nil {
		data = []byte{}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO entries(id,parent_id,name,kind,mime,content,size,revision,created_at,updated_at,trashed_at,trash_batch,editable,previewable,system) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, e.ID, e.ParentID, e.Name, e.Kind, e.MIME, data, e.Size, e.Revision, e.CreatedAt, e.UpdatedAt, e.TrashedAt, e.TrashBatch, e.Editable, e.Previewable, e.System)
	return err
}

func (s *Store) Create(ctx context.Context, in CreateInput) (Entry, error) {
	if in.Kind != "folder" && in.Kind != "file" {
		return Entry{}, problem("invalid_kind", "Choose file or folder.")
	}
	if in.Kind == "folder" && in.Content != "" {
		return Entry{}, problem("invalid_content", "Folders cannot contain text.")
	}
	if len(in.Content) > MaxTextSize || !utf8.ValidString(in.Content) {
		return Entry{}, problem("text_too_large", "Editable text must be valid UTF-8 and no larger than 1 MiB.")
	}
	return s.create(ctx, in.ParentID, in.Name, in.Kind, []byte(in.Content), false)
}

func (s *Store) Import(ctx context.Context, parentID, name string, data []byte) (Entry, error) {
	if len(data) > MaxFileSize {
		return Entry{}, problem("file_too_large", "Files must be no larger than 10 MiB.")
	}
	return s.create(ctx, parentID, name, "file", data, true)
}

func (s *Store) ImportMany(ctx context.Context, parentID string, files []ImportFile) ([]Entry, error) {
	entries := []Entry{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := parent(ctx, tx, parentID); err != nil {
			return err
		}
		for _, file := range files {
			if len(file.Data) > MaxFileSize {
				return problem("file_too_large", "Files must be no larger than 10 MiB.")
			}
			name, err := uniqueName(ctx, tx, parentID, file.Name, "", true)
			if err != nil {
				return err
			}
			e := makeEntry(parentID, name, "file", file.Data)
			if err = insert(ctx, tx, e, file.Data); err != nil {
				return err
			}
			entries = append(entries, e)
		}
		return nil
	})
	return entries, err
}

func (s *Store) create(ctx context.Context, parentID, name, kind string, data []byte, rename bool) (Entry, error) {
	var e Entry
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := parent(ctx, tx, parentID); err != nil {
			return err
		}
		resolved, err := uniqueName(ctx, tx, parentID, name, "", rename)
		if err != nil {
			return err
		}
		e = makeEntry(parentID, resolved, kind, data)
		return insert(ctx, tx, e, data)
	})
	return e, err
}

func (s *Store) Update(ctx context.Context, id string, in UpdateInput) (Entry, error) {
	var e Entry
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		e, err = get(ctx, tx, id)
		if err != nil {
			return err
		}
		if e.System || e.TrashedAt != "" {
			return problem("unavailable", "This item cannot be changed.")
		}
		if in.Revision == nil || *in.Revision != e.Revision {
			return problem("revision_conflict", "This item changed elsewhere. Refresh its information or save a copy.")
		}
		if in.Name != nil {
			e.Name = *in.Name
		}
		if in.ParentID != nil {
			if err = parent(ctx, tx, *in.ParentID); err != nil {
				return err
			}
			for next := *in.ParentID; next != ""; {
				if next == id {
					return problem("folder_cycle", "A folder cannot be moved inside itself.")
				}
				p, err := get(ctx, tx, next)
				if err != nil {
					return err
				}
				next = p.ParentID
			}
			e.ParentID = *in.ParentID
		}
		if e.Name, err = uniqueName(ctx, tx, e.ParentID, e.Name, id, false); err != nil {
			return err
		}
		if in.Content != nil {
			if !e.Editable || e.Kind != "file" {
				return problem("not_editable", "This file cannot be edited as text.")
			}
			if len(*in.Content) > MaxTextSize || !utf8.ValidString(*in.Content) {
				return problem("text_too_large", "Editable text must be valid UTF-8 and no larger than 1 MiB.")
			}
			mime, editable, preview := classify([]byte(*in.Content))
			if !editable {
				return problem("invalid_text", "Text cannot contain binary control characters.")
			}
			e.Size = len(*in.Content)
			e.MIME = mime
			e.Editable = editable
			e.Previewable = preview
			if _, err = tx.ExecContext(ctx, "UPDATE entries SET content=? WHERE id=?", []byte(*in.Content), id); err != nil {
				return err
			}
		}
		e.Revision++
		e.UpdatedAt = now()
		_, err = tx.ExecContext(ctx, `UPDATE entries SET parent_id=?,name=?,mime=?,size=?,editable=?,previewable=?,revision=?,updated_at=? WHERE id=?`, e.ParentID, e.Name, e.MIME, e.Size, e.Editable, e.Previewable, e.Revision, e.UpdatedAt, id)
		return err
	})
	return e, err
}

func (s *Store) Duplicate(ctx context.Context, itemID, parentID string) (Entry, error) {
	var result Entry
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		source, err := get(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if source.TrashedAt != "" || source.System {
			return problem("unavailable", "This item cannot be duplicated.")
		}
		if parentID == "" {
			parentID = source.ParentID
		}
		if err = parent(ctx, tx, parentID); err != nil {
			return err
		}
		entries, err := list(ctx, tx)
		if err != nil {
			return err
		}
		var copyTree func(Entry, string) (Entry, error)
		copyTree = func(e Entry, destination string) (Entry, error) {
			name, err := uniqueName(ctx, tx, destination, e.Name, "", true)
			if err != nil {
				return Entry{}, err
			}
			var data []byte
			if err = tx.QueryRowContext(ctx, "SELECT content FROM entries WHERE id=?", e.ID).Scan(&data); err != nil {
				return Entry{}, err
			}
			copy := makeEntry(destination, name, e.Kind, data)
			if err = insert(ctx, tx, copy, data); err != nil {
				return Entry{}, err
			}
			for _, child := range entries {
				if child.ParentID == e.ID && child.TrashedAt == "" {
					if _, err = copyTree(child, copy.ID); err != nil {
						return Entry{}, err
					}
				}
			}
			return copy, nil
		}
		result, err = copyTree(source, parentID)
		return err
	})
	return result, err
}
