package projectfiles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type importRequest struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	ParentID  string `json:"parentId,omitempty"`
	SHA256    string `json:"sha256"`
}
type exportRequest struct {
	RequestID  string `json:"requestId"`
	SnapshotID string `json:"snapshotId"`
	Path       string `json:"path"`
}

func receipt(w http.ResponseWriter, result json.RawMessage) {
	var failure struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if json.Unmarshal(result, &failure) == nil && failure.Code != "" {
		fail(w, problem(failure.Code, failure.Error))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(result)
}
func payloadHash(kind, projectID string, payload any) string {
	data, _ := json.Marshal(struct {
		Kind      string
		ProjectID string
		Payload   any
	}{kind, projectID, payload})
	return digest(data)
}

func (a *API) importFile(w http.ResponseWriter, r *http.Request) {
	var in importRequest
	if !decode(w, r, &in) {
		return
	}
	if in.ParentID == "" {
		in.ParentID = "documents"
	}
	name, err := relative(in.Path, false)
	if err != nil {
		fail(w, err)
		return
	}
	in.Path = name
	if !validRequestID(in.RequestID) || len(in.SHA256) != 64 || strings.Trim(in.SHA256, "0123456789abcdef") != "" {
		fail(w, problem("invalid_request", "Use a request ID and the reviewed file's SHA-256."))
		return
	}
	t := storage.DocumentTransfer{RequestID: in.RequestID, Kind: "import", ProjectID: r.PathValue("id"), Path: name, SHA256: in.SHA256}
	t.PayloadHash = payloadHash(t.Kind, t.ProjectID, in)
	old, found, err := a.store.DocumentTransfer(r.Context(), t.RequestID, t.PayloadHash)
	if err != nil {
		fail(w, err)
		return
	}
	if found {
		receipt(w, old.Result)
		return
	}
	_, root, err := a.project(r.Context(), t.ProjectID)
	if err != nil {
		fail(w, err)
		return
	}
	defer root.Close()
	data, err := read(root, name, storage.MaxFileSize)
	if err != nil {
		fail(w, err)
		return
	}
	if digest(data) != in.SHA256 {
		fail(w, problem("source_changed", "The project file changed since it was reviewed. Read it again before importing."))
		return
	}
	result, err := a.store.ImportProjectFile(r.Context(), t, in.ParentID, path.Base(name), data)
	if err != nil {
		fail(w, err)
		return
	}
	receipt(w, result)
}

func (a *API) exportSnapshot(w http.ResponseWriter, r *http.Request) {
	var in exportRequest
	if !decode(w, r, &in) {
		return
	}
	name, err := relative(in.Path, false)
	if err != nil {
		fail(w, err)
		return
	}
	in.Path = name
	if !validRequestID(in.RequestID) || in.SnapshotID == "" {
		fail(w, problem("invalid_request", "Choose a snapshot and a request ID."))
		return
	}
	t := storage.DocumentTransfer{RequestID: in.RequestID, Kind: "export", ProjectID: r.PathValue("id"), Path: name}
	t.PayloadHash = payloadHash(t.Kind, t.ProjectID, in)
	release, err := a.lockExports(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	defer release()
	old, found, err := a.store.DocumentTransfer(r.Context(), t.RequestID, t.PayloadHash)
	if err != nil {
		fail(w, err)
		return
	}
	if found && len(old.Result) > 0 {
		a.cleanupExport(r.Context(), old)
		receipt(w, old.Result)
		return
	}
	d, err := a.store.Snapshot(r.Context(), in.SnapshotID)
	if err != nil {
		fail(w, err)
		return
	}
	t.SHA256 = d.SHA256
	_, root, err := a.project(r.Context(), t.ProjectID)
	if err != nil {
		fail(w, err)
		return
	}
	defer root.Close()
	parent, identity, err := exportParent(root, name)
	if err != nil {
		if found {
			err = problem("transfer_unresolved", "The reserved export destination directory is unavailable. Restore it before retrying this request.")
		}
		fail(w, err)
		return
	}
	_ = parent.Close()
	t.ParentIdentity = identity
	t, err = a.store.ReserveExport(r.Context(), t)
	if err != nil {
		fail(w, err)
		return
	}
	if len(t.Result) > 0 {
		a.cleanupExport(r.Context(), t)
		receipt(w, t.Result)
		return
	}
	if err = publish(root, t, []byte(d.Content)); err != nil {
		var failure *storage.Error
		if errors.As(err, &failure) && failure.Code == "name_conflict" {
			result, _ := json.Marshal(map[string]string{"code": failure.Code, "error": failure.Message})
			if commitErr := a.store.CompleteExport(r.Context(), t, result); commitErr != nil {
				fail(w, commitErr)
				return
			}
			a.cleanupExport(r.Context(), t)
		}
		fail(w, err)
		return
	}
	result, _ := json.Marshal(map[string]any{"path": name, "sha256": d.SHA256, "snapshotId": d.ID, "projectId": t.ProjectID})
	if err = a.store.CompleteExport(r.Context(), t, result); err != nil {
		fail(w, err)
		return
	}
	// A completed receipt no longer needs its hard-linked recovery anchor.
	a.cleanupExport(r.Context(), t)
	receipt(w, result)
}

func (a *API) lockExports(ctx context.Context) (func(), error) {
	path, err := a.store.DocumentTransferLockPath(ctx)
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
func (a *API) cleanupExport(ctx context.Context, transfer storage.DocumentTransfer) {
	if transfer.StagePath == "" || !strings.HasPrefix(transfer.StagePath, ".axv-export-") || strings.ContainsAny(transfer.StagePath, "/\\") {
		return
	}
	_, root, err := a.project(ctx, transfer.ProjectID)
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
