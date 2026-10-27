package projectfiles

import (
	"context"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/hostdir"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

// Service owns registered host-project access and explicit document exchange.
// It keeps filesystem publication and receipt recovery independent of HTTP.
type Service struct {
	store *storage.Store
}

// New uses the desktop store for registered identities and transfer receipts.
func New(store *storage.Store) *Service { return &Service{store: store} }

// Listing is one page of the current directory, not a frozen snapshot.
type Listing struct {
	Project    storage.ProjectRef `json:"project"`
	Entries    []Entry            `json:"entries"`
	NextCursor string             `json:"nextCursor"`
	Truncated  bool               `json:"truncated"`
}

// File contains bounded text read from the original registered project root.
type File struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	SHA256    string `json:"sha256"`
	Size      int    `json:"size"`
}

// Register records a canonical directory and its current filesystem identity.
// Explicit registration of a replacement directory preserves older references.
func (s *Service) Register(ctx context.Context, name, rootPath string) (storage.ProjectRef, error) {
	canonical, err := hostdir.CanonicalDirectory(rootPath)
	if err != nil {
		return storage.ProjectRef{}, problem("invalid_project", "Choose an existing readable absolute project directory.")
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return storage.ProjectRef{}, fileError(err)
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return storage.ProjectRef{}, err
	}
	identity, err := rootIdentity(info)
	if err != nil {
		return storage.ProjectRef{}, err
	}
	if name == "" {
		name = filepath.Base(canonical)
	}
	return s.store.RegisterProject(ctx, name, canonical, identity)
}

func (s *Service) project(ctx context.Context, projectID string) (storage.ProjectRef, *os.Root, error) {
	p, err := s.store.Project(ctx, projectID)
	if err != nil {
		return p, nil, err
	}
	root, err := openProject(p)
	return p, root, err
}

// List validates the relative directory and lists up to limit entries (1–200).
func (s *Service) List(ctx context.Context, projectID, directory, cursor string, limit int) (Listing, error) {
	dir, err := relative(directory, true)
	if err != nil {
		return Listing{}, err
	}
	if limit < 1 || limit > 200 {
		return Listing{}, problem("invalid_request", "Directory limits must be between 1 and 200.")
	}
	p, root, err := s.project(ctx, projectID)
	if err != nil {
		return Listing{}, err
	}
	defer root.Close()
	entries, next, truncated, err := list(root, dir, cursor, limit)
	return Listing{Project: p, Entries: entries, NextCursor: next, Truncated: truncated}, err
}

func (s *Service) fileBytes(ctx context.Context, projectID, filename string, max int64) (storage.ProjectRef, string, []byte, error) {
	name, err := relative(filename, false)
	if err != nil {
		return storage.ProjectRef{}, "", nil, err
	}
	p, root, err := s.project(ctx, projectID)
	if err != nil {
		return p, name, nil, err
	}
	defer root.Close()
	data, err := read(root, name, max)
	return p, name, data, err
}

// Read accepts only bounded UTF-8 text from a regular project file.
func (s *Service) Read(ctx context.Context, projectID, filename string) (File, error) {
	p, name, data, err := s.fileBytes(ctx, projectID, filename, storage.MaxTextSize)
	if err != nil {
		return File{}, err
	}
	if !text(data) {
		return File{}, problem("not_text", "This file is not previewable text. Download the original file instead.")
	}
	return File{ProjectID: p.ID, Path: name, Name: filepath.Base(name), Content: string(data), SHA256: digest(data), Size: len(data)}, nil
}

// Download returns the name and original bytes of a bounded regular file.
func (s *Service) Download(ctx context.Context, projectID, filename string) (string, []byte, error) {
	_, name, data, err := s.fileBytes(ctx, projectID, filename, storage.MaxFileSize)
	return filepath.Base(name), data, err
}
