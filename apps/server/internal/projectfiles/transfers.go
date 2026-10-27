package projectfiles

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

// ImportRequest captures the reviewed source and explicit copy destination.
type ImportRequest struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	ParentID  string `json:"parentId,omitempty"`
	SHA256    string `json:"sha256"`
}

// ExportRequest binds an immutable snapshot to a new project-relative path.
type ExportRequest struct {
	RequestID  string `json:"requestId"`
	SnapshotID string `json:"snapshotId"`
	Path       string `json:"path"`
}

func payloadHash(kind, projectID string, payload any) string {
	data, _ := json.Marshal(struct {
		Kind      string
		ProjectID string
		Payload   any
	}{kind, projectID, payload})
	return digest(data)
}

func replayReceipt(result json.RawMessage) (json.RawMessage, error) {
	var failure struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if json.Unmarshal(result, &failure) == nil && failure.Code != "" {
		return nil, problem(failure.Code, failure.Error)
	}
	return result, nil
}

// Import copies reviewed bytes into a document and commits its receipt with
// the document, origin and link. Replay never rereads a mutable source.
func (s *Service) Import(ctx context.Context, projectID string, in ImportRequest) (json.RawMessage, error) {
	if in.ParentID == "" {
		in.ParentID = "documents"
	}
	name, err := relative(in.Path, false)
	if err != nil {
		return nil, err
	}
	in.Path = name
	if !validRequestID(in.RequestID) || len(in.SHA256) != 64 || strings.Trim(in.SHA256, "0123456789abcdef") != "" {
		return nil, problem("invalid_request", "Use a request ID and the reviewed file's SHA-256.")
	}
	t := storage.DocumentTransfer{RequestID: in.RequestID, Kind: "import", ProjectID: projectID, Path: name, SHA256: in.SHA256}
	t.PayloadHash = payloadHash(t.Kind, t.ProjectID, in)
	old, found, err := s.store.DocumentTransfer(ctx, t.RequestID, t.PayloadHash)
	if err != nil {
		return nil, err
	}
	if found {
		return replayReceipt(old.Result)
	}
	_, root, err := s.project(ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := read(root, name, storage.MaxFileSize)
	if err != nil {
		return nil, err
	}
	if digest(data) != in.SHA256 {
		return nil, problem("source_changed", "The project file changed since it was reviewed. Read it again before importing.")
	}
	return s.store.ImportProjectFile(ctx, t, in.ParentID, path.Base(name), data)
}

// Export reserves the original parent identity, publishes without replacement
// and commits an immutable receipt before cleaning up the recovery anchor.
// Replaying a terminal failure returns its original domain error.
func (s *Service) Export(ctx context.Context, projectID string, in ExportRequest) (json.RawMessage, error) {
	name, err := relative(in.Path, false)
	if err != nil {
		return nil, err
	}
	in.Path = name
	if !validRequestID(in.RequestID) || in.SnapshotID == "" {
		return nil, problem("invalid_request", "Choose a snapshot and a request ID.")
	}
	t := storage.DocumentTransfer{RequestID: in.RequestID, Kind: "export", ProjectID: projectID, Path: name}
	t.PayloadHash = payloadHash(t.Kind, t.ProjectID, in)
	release, err := s.lockExports(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	old, found, err := s.store.DocumentTransfer(ctx, t.RequestID, t.PayloadHash)
	if err != nil {
		return nil, err
	}
	if found && len(old.Result) > 0 {
		s.cleanupExport(ctx, old)
		return replayReceipt(old.Result)
	}
	d, err := s.store.Snapshot(ctx, in.SnapshotID)
	if err != nil {
		return nil, err
	}
	t.SHA256 = d.SHA256
	_, root, err := s.project(ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	parent, identity, err := exportParent(root, name)
	if err != nil {
		if found {
			err = problem("transfer_unresolved", "The reserved export destination directory is unavailable. Restore it before retrying this request.")
		}
		return nil, err
	}
	_ = parent.Close()
	t.ParentIdentity = identity
	t, err = s.store.ReserveExport(ctx, t)
	if err != nil {
		return nil, err
	}
	if len(t.Result) > 0 {
		s.cleanupExport(ctx, t)
		return replayReceipt(t.Result)
	}
	if err = publish(root, t, []byte(d.Content)); err != nil {
		var failure *storage.Error
		if errors.As(err, &failure) && failure.Code == "name_conflict" {
			result, _ := json.Marshal(map[string]string{"code": failure.Code, "error": failure.Message})
			if commitErr := s.store.CompleteExport(ctx, t, result); commitErr != nil {
				return nil, commitErr
			}
			s.cleanupExport(ctx, t)
		}
		return nil, err
	}
	result, _ := json.Marshal(map[string]any{"path": name, "sha256": d.SHA256, "snapshotId": d.ID, "projectId": t.ProjectID})
	if err = s.store.CompleteExport(ctx, t, result); err != nil {
		return nil, err
	}
	// A completed receipt no longer needs its hard-linked recovery anchor.
	s.cleanupExport(ctx, t)
	return result, nil
}

func (s *Service) lockExports(ctx context.Context) (func(), error) {
	path, err := s.store.DocumentTransferLockPath(ctx)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, problem("file_unavailable", "The private export coordination file could not be verified.")
		}
	} else if !os.IsNotExist(err) {
		return nil, fileError(err)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if err := wait.Err(); err != nil {
			return nil, problem("transfer_unresolved", "Another export is still being published. Retry this request unchanged.")
		}
		lock, err := filelock.TryLock(path)
		if err == nil {
			return func() { _ = lock.Close() }, nil
		}
		if !exportLockContended(err) {
			return nil, fileError(err)
		}
		select {
		case <-wait.Done():
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A committed receipt remains authoritative even if its project is currently
// offline. When available, replay also finishes interrupted anchor cleanup.
func (s *Service) cleanupExport(ctx context.Context, transfer storage.DocumentTransfer) {
	if transfer.StagePath == "" || !strings.HasPrefix(transfer.StagePath, ".axv-export-") || strings.ContainsAny(transfer.StagePath, "/\\") {
		return
	}
	_, root, err := s.project(ctx, transfer.ProjectID)
	if err != nil {
		return
	}
	defer root.Close()
	parent, identity, err := exportParent(root, transfer.Path)
	if err != nil {
		return
	}
	_ = parent.Close()
	if identity != transfer.ParentIdentity {
		return
	}
	data, err := read(root, transfer.StagePath, storage.MaxTextSize)
	if err == nil && digest(data) == transfer.SHA256 {
		_ = root.Remove(transfer.StagePath)
	}
}

func exportParent(root *os.Root, filename string) (*os.Root, string, error) {
	parent, err := root.OpenRoot(path.Dir(filename))
	if err != nil {
		return nil, "", fileError(err)
	}
	info, err := parent.Stat(".")
	if err != nil {
		_ = parent.Close()
		return nil, "", fileError(err)
	}
	identity, err := rootIdentity(info)
	if err != nil {
		_ = parent.Close()
		return nil, "", err
	}
	return parent, identity, nil
}

func verifyExportParent(root *os.Root, t storage.DocumentTransfer) error {
	parent, identity, err := exportParent(root, t.Path)
	if err != nil {
		return problem("transfer_unresolved", "The export destination directory is unavailable. Restore its original identity before retrying this request.")
	}
	_ = parent.Close()
	if t.ParentIdentity == "" || identity != t.ParentIdentity {
		return problem("transfer_unresolved", "The export destination directory was replaced. No export was redirected into its replacement.")
	}
	return nil
}

// publish never overwrites a destination. Retaining the staging hard link until
// the receipt commits distinguishes our interrupted publication from a same-
// content file that belonged to the user before this request.
func publish(root *os.Root, t storage.DocumentTransfer, data []byte) error {
	if !strings.HasPrefix(t.StagePath, ".axv-export-") || strings.ContainsAny(t.StagePath, "/\\\x00") {
		return problem("transfer_unresolved", "The reserved export recovery name could not be verified.")
	}
	parent, identity, err := exportParent(root, t.Path)
	if err != nil {
		return problem("transfer_unresolved", "The reserved export destination directory is unavailable. Restore it before retrying this request.")
	}
	defer parent.Close()
	if t.ParentIdentity == "" || identity != t.ParentIdentity {
		return problem("transfer_unresolved", "The export destination directory was replaced. No export was redirected into its replacement.")
	}
	sourceDirectory, err := root.Open(".")
	if err != nil {
		return fileError(err)
	}
	defer sourceDirectory.Close()
	destinationDirectory, err := parent.Open(".")
	if err != nil {
		return fileError(err)
	}
	defer destinationDirectory.Close()
	f, err := root.OpenFile(t.StagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return problem("transfer_unresolved", "The transfer staging write did not complete. Its request and recovery file were preserved.")
		}
	} else if !errors.Is(err, os.ErrExist) {
		return fileError(err)
	}
	staged, err := read(root, t.StagePath, storage.MaxTextSize)
	if err != nil || digest(staged) != t.SHA256 {
		return problem("transfer_unresolved", "The transfer recovery file could not be verified. No destination was replaced.")
	}
	if err = linkDirectories(sourceDirectory, t.StagePath, destinationDirectory, path.Base(t.Path)); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fileError(err)
		}
		source, sourceErr := root.Lstat(t.StagePath)
		destination, destErr := parent.Lstat(path.Base(t.Path))
		if sourceErr != nil || destErr != nil || !destination.Mode().IsRegular() || !os.SameFile(source, destination) {
			if err := verifyExportParent(root, t); err != nil {
				return err
			}
			return problem("name_conflict", "A file already exists at this destination. Choose another name; it was not overwritten.")
		}
	}
	// Host tools can modify project files outside AXV. Do not acknowledge a
	// successful export when the published bytes no longer match its snapshot.
	published, err := read(parent, path.Base(t.Path), storage.MaxTextSize)
	if err != nil || digest(published) != t.SHA256 {
		return problem("transfer_unresolved", "The published file changed before confirmation. No overwrite was attempted; review it before retrying.")
	}
	// Commit directory entries before acknowledging publication to SQLite.
	for _, f := range []*os.File{sourceDirectory, destinationDirectory} {
		err = f.Sync()
		if err != nil {
			return problem("transfer_unresolved", "The exported file exists but publication durability is unconfirmed. Retry this request unchanged.")
		}
	}
	return verifyExportParent(root, t)
}

func validRequestID(value string) bool {
	return len(value) >= 8 && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n") && strings.TrimSpace(value) == value
}
