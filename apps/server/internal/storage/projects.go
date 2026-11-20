package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
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
	if err := validProjectName(name); err != nil {
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
		p = ProjectRef{ID: id(), Kind: "host", Name: name, RootPath: root, RootIdentity: identity, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		_, err = tx.ExecContext(ctx, "INSERT INTO projects(id,kind,name,root_path,root_identity,created_at) VALUES(?,?,?,?,?,?)", p.ID, p.Kind, p.Name, p.RootPath, p.RootIdentity, p.CreatedAt)
		return err
	})
	return p, err
}

func validProjectName(name string) error {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, "/\\") || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return problem("invalid_name", "Use a name of 1–255 bytes without slashes or control characters.")
	}
	return nil
}
