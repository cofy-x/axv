package guest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

type projectFileError struct {
	status int
	code   string
	text   string
}

func (e *projectFileError) Error() string { return e.text }

func projectProblem(status int, code, text string) error {
	return &projectFileError{status: status, code: code, text: text}
}

func projectFailure(w http.ResponseWriter, err error) {
	var problem *projectFileError
	if errors.As(err, &problem) {
		fail(w, problem.status, problem.code, problem)
		return
	}
	code, status := "file_unavailable", http.StatusServiceUnavailable
	switch {
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		code, status = "disk_full", http.StatusInsufficientStorage
	case errors.Is(err, fs.ErrNotExist):
		code, status = "not_found", http.StatusNotFound
	case errors.Is(err, fs.ErrExist):
		code, status = "file_conflict", http.StatusConflict
	case errors.Is(err, fs.ErrPermission):
		code, status = "file_forbidden", http.StatusForbidden
	}
	fail(w, status, code, fmt.Errorf("project file operation failed: %w", err))
}

// JSON escaping can expand one MiB of text to six MiB. Only writes receive
// this larger envelope; the decoded content has its own one-MiB byte limit.
func decodeProjectFile(w http.ResponseWriter, r *http.Request, target any, write bool) error {
	limit := int64(64 << 10)
	if write {
		limit = 8 << 20
	}
	return decodeProjectJSON(w, r, target, limit, write)
}

func decodeProjectJSON(w http.ResponseWriter, r *http.Request, target any, limit int64, write bool) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return projectProblem(400, "invalid_request", "Project file requests require application/json.")
	}
	if r.URL.RawQuery != "" {
		return projectProblem(400, "invalid_request", "Project file requests do not accept query parameters.")
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return projectProblem(400, "invalid_request", "The project file request body exceeds its limit.")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return projectProblem(400, "invalid_request", "Expected one JSON object.")
	}
	if !jsonunicode.Valid(data) {
		return projectProblem(400, "invalid_request", "Project file requests require valid Unicode without unpaired surrogates.")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return projectProblem(400, "invalid_request", "Invalid project file request: "+err.Error())
	}
	if d.Decode(new(any)) != io.EOF {
		return projectProblem(400, "invalid_request", "Expected one JSON object.")
	}
	if write {
		var explicit struct {
			Content *string `json:"content"`
		}
		if err := json.Unmarshal(data, &explicit); err != nil || explicit.Content == nil {
			return projectProblem(400, "invalid_request", "Supply content explicitly as a JSON string; use an empty string to write an empty file.")
		}
	}
	return nil
}

func projectRelative(value string, allowRoot bool) (string, error) {
	if value == "" && allowRoot {
		return ".", nil
	}
	if value == "." || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00") || !fs.ValidPath(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", projectProblem(400, "invalid_path", "Use a canonical relative project path.")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".axv" || strings.HasPrefix(part, ".axv-") {
			return "", projectProblem(400, "invalid_path", "AXV internal paths are reserved.")
		}
	}
	return value, nil
}

func projectDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func projectText(data []byte) bool {
	return utf8.Valid(data) && strings.IndexFunc(string(data), func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\f'
	}) < 0
}

func projectDirectory(root *os.Root, name string) (*os.Root, fs.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, projectProblem(400, "invalid_directory", "Choose an existing directory; symbolic links are not project roots or selected directories.")
	}
	dir, err := root.OpenRoot(name)
	if err != nil {
		return nil, nil, err
	}
	opened, err := dir.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		dir.Close()
		return nil, nil, projectProblem(409, "project_changed", "The project directory changed while it was opened.")
	}
	return dir, opened, nil
}

func (s *service) openProjectFiles(scope protocol.ProjectFileScope, inspect bool) (*os.Root, fs.FileInfo, string, error) {
	if scope.BootID == "" || scope.InstanceID == "" || (!inspect && scope.RootIdentity == "") {
		return nil, nil, "", projectProblem(400, "invalid_request", "Boot, guest-service and project-root identities are required.")
	}
	if scope.BootID != s.bootID {
		return nil, nil, "", projectProblem(409, "boot_changed", "The VM boot changed. Obtain a new project binding.")
	}
	if scope.InstanceID != s.instanceID {
		return nil, nil, "", projectProblem(409, "instance_changed", "The guest service changed. Obtain a new project binding.")
	}
	if err := s.projectReady(); err != nil {
		return nil, nil, "", projectProblem(503, "project_unavailable", err.Error())
	}
	name, err := projectRelative(scope.Root, true)
	if err != nil {
		return nil, nil, "", err
	}
	volumeInfo, err := os.Lstat(s.config.ProjectRoot)
	if err != nil {
		return nil, nil, "", err
	}
	if !volumeInfo.IsDir() {
		return nil, nil, "", projectProblem(503, "project_unavailable", "The mounted project volume is not a directory.")
	}
	volume, err := os.OpenRoot(s.config.ProjectRoot)
	if err != nil {
		return nil, nil, "", err
	}
	defer volume.Close()
	openedVolume, err := volume.Stat(".")
	if err != nil || !os.SameFile(volumeInfo, openedVolume) {
		return nil, nil, "", projectProblem(409, "project_changed", "The project volume changed while it was opened.")
	}
	root, info, err := projectDirectory(volume, name)
	if err != nil {
		return nil, nil, "", err
	}
	identity, err := projectFileRootIdentity(root, info)
	if err != nil || (scope.RootIdentity != "" && scope.RootIdentity != identity) {
		root.Close()
		if err != nil {
			return nil, nil, "", err
		}
		return nil, nil, "", projectProblem(409, "project_changed", "The project root no longer has its registered identity.")
	}
	return root, info, identity, nil
}

func (s *service) verifyProjectFiles(scope protocol.ProjectFileScope, original fs.FileInfo) error {
	root, info, _, err := s.openProjectFiles(scope, false)
	if err != nil {
		return err
	}
	defer root.Close()
	if !os.SameFile(original, info) {
		return projectProblem(409, "project_changed", "The mounted project directory changed during this request.")
	}
	return nil
}

func (s *service) inspectProjectFiles(w http.ResponseWriter, r *http.Request) {
	var in protocol.ProjectFileInspectRequest
	if err := decodeProjectFile(w, r, &in, false); err != nil {
		projectFailure(w, err)
		return
	}
	root, _, identity, err := s.openProjectFiles(in, true)
	if err != nil {
		projectFailure(w, err)
		return
	}
	defer root.Close()
	respond(w, 200, protocol.ProjectFileRoot{Root: in.Root, RootIdentity: identity})
}

func projectRegular(root *os.Root, name string) (*os.File, fs.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, projectProblem(400, "not_file", "Choose a regular project file; symbolic links and special files cannot be opened.")
	}
	file, err := projectFileOpen(root, name)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		file.Close()
		return nil, nil, projectProblem(409, "file_conflict", "The project file changed while it was opened.")
	}
	return file, opened, nil
}

func projectRead(root *os.Root, name string) (protocol.ProjectFile, fs.FileInfo, error) {
	file, info, err := projectRegular(root, name)
	if err != nil {
		return protocol.ProjectFile{}, nil, err
	}
	defer file.Close()
	if info.Size() > protocol.MaxProjectFileSize {
		return protocol.ProjectFile{}, nil, projectProblem(413, "file_too_large", "Project text files are limited to one MiB.")
	}
	data, err := io.ReadAll(io.LimitReader(file, protocol.MaxProjectFileSize+1))
	if err != nil {
		return protocol.ProjectFile{}, nil, err
	}
	if len(data) > protocol.MaxProjectFileSize {
		return protocol.ProjectFile{}, nil, projectProblem(413, "file_too_large", "Project text files are limited to one MiB.")
	}
	if !projectText(data) {
		return protocol.ProjectFile{}, nil, projectProblem(415, "not_text", "Choose a UTF-8 text file.")
	}
	after, err := file.Stat()
	if err != nil {
		return protocol.ProjectFile{}, nil, err
	}
	if info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return protocol.ProjectFile{}, nil, projectProblem(409, "file_conflict", "The project file changed while it was read.")
	}
	return protocol.ProjectFile{Path: name, Content: string(data), SHA256: projectDigest(data), Size: int64(len(data)), ModifiedAt: after.ModTime().UTC()}, after, nil
}

func (s *service) readProjectFile(w http.ResponseWriter, r *http.Request) {
	var in protocol.ProjectFileReadRequest
	if err := decodeProjectFile(w, r, &in, false); err != nil {
		projectFailure(w, err)
		return
	}
	name, err := projectRelative(in.Path, false)
	if err != nil {
		projectFailure(w, err)
		return
	}
	root, info, _, err := s.openProjectFiles(in.ProjectFileScope, false)
	if err != nil {
		projectFailure(w, err)
		return
	}
	defer root.Close()
	result, _, err := projectRead(root, name)
	if err == nil {
		err = s.verifyProjectFiles(in.ProjectFileScope, info)
	}
	if err != nil {
		projectFailure(w, err)
		return
	}
	respond(w, 200, result)
}

func projectEntry(name string, info fs.FileInfo) protocol.ProjectFileEntry {
	kind := "file"
	if info.IsDir() {
		kind = "folder"
	} else if info.Mode()&os.ModeSymlink != 0 {
		kind = "symlink"
	}
	return protocol.ProjectFileEntry{Name: path.Base(name), Path: name, Kind: kind, Size: info.Size(), ModifiedAt: info.ModTime().UTC()}
}

func (s *service) listProjectFiles(w http.ResponseWriter, r *http.Request) {
	var in protocol.ProjectFileListRequest
	if err := decodeProjectFile(w, r, &in, false); err != nil {
		projectFailure(w, err)
		return
	}
	name, err := projectRelative(in.Path, true)
	if err != nil {
		projectFailure(w, err)
		return
	}
	if in.Limit == 0 {
		in.Limit = 100
	}
	offset := 0
	if in.Cursor != "" {
		offset, err = strconv.Atoi(in.Cursor)
	}
	if err != nil || offset < 0 || offset > 10000 || in.Limit < 1 || in.Limit > 200 {
		projectFailure(w, projectProblem(400, "invalid_request", "Use a page limit of 1–200 and the returned cursor."))
		return
	}
	root, rootInfo, _, err := s.openProjectFiles(in.ProjectFileScope, false)
	if err != nil {
		projectFailure(w, err)
		return
	}
	defer root.Close()
	dir, _, err := projectDirectory(root, name)
	if err != nil {
		projectFailure(w, err)
		return
	}
	defer dir.Close()
	file, err := dir.Open(".")
	if err != nil {
		projectFailure(w, err)
		return
	}
	defer file.Close()
	entries, err := file.ReadDir(10001)
	if err != nil && err != io.EOF {
		projectFailure(w, err)
		return
	}
	result := protocol.ProjectFileListing{Entries: []protocol.ProjectFileEntry{}, Truncated: len(entries) > 10000}
	if result.Truncated {
		entries = entries[:10000]
	}
	for _, entry := range entries {
		if _, err := projectRelative(entry.Name(), false); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil || (!info.IsDir() && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0) {
			continue
		}
		result.Entries = append(result.Entries, projectEntry(path.Join(in.Path, entry.Name()), info))
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Name < result.Entries[j].Name })
	if offset > len(result.Entries) {
		projectFailure(w, projectProblem(400, "invalid_cursor", "The directory changed; refresh the listing."))
		return
	}
	end := min(offset+in.Limit, len(result.Entries))
	if end < len(result.Entries) {
		result.NextCursor = strconv.Itoa(end)
	}
	result.Entries = result.Entries[offset:end]
	if err := s.verifyProjectFiles(in.ProjectFileScope, rootInfo); err != nil {
		projectFailure(w, err)
		return
	}
	respond(w, 200, result)
}

func projectVerifyParent(root *os.Root, name string, original fs.FileInfo) error {
	parent, info, err := projectDirectory(root, path.Dir(name))
	if err != nil {
		return err
	}
	defer parent.Close()
	if !os.SameFile(original, info) {
		return projectProblem(409, "file_conflict", "The destination parent changed during this request.")
	}
	return nil
}

func projectSyncDirectory(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func (s *service) writeProjectFile(w http.ResponseWriter, r *http.Request) {
	var in protocol.ProjectFileWriteRequest
	if err := decodeProjectFile(w, r, &in, true); err != nil {
		projectFailure(w, err)
		return
	}
	if in.ExpectedSHA256 == nil || (*in.ExpectedSHA256 != "" && (len(*in.ExpectedSHA256) != 64 || strings.Trim(*in.ExpectedSHA256, "0123456789abcdef") != "")) {
		projectFailure(w, projectProblem(400, "invalid_request", "Supply expectedSha256 explicitly: an empty string to create or the reviewed file's SHA-256 to replace."))
		return
	}
	if len(in.Content) > protocol.MaxProjectFileSize {
		projectFailure(w, projectProblem(413, "file_too_large", "Project text files are limited to one MiB."))
		return
	}
	if !projectText([]byte(in.Content)) {
		projectFailure(w, projectProblem(415, "not_text", "Write UTF-8 text without binary control characters."))
		return
	}
	name, err := projectRelative(in.Path, false)
	if err != nil {
		projectFailure(w, err)
		return
	}
	s.projectFilesMu.Lock()
	defer s.projectFilesMu.Unlock()
	result, err := s.publishProjectFile(r.Context(), in, name)
	if err != nil {
		projectFailure(w, err)
		return
	}
	respond(w, 200, result)
}

func (s *service) publishProjectFile(ctx context.Context, in protocol.ProjectFileWriteRequest, name string) (protocol.ProjectFile, error) {
	if err := ctx.Err(); err != nil {
		return protocol.ProjectFile{}, err
	}
	root, rootInfo, _, err := s.openProjectFiles(in.ProjectFileScope, false)
	if err != nil {
		return protocol.ProjectFile{}, err
	}
	defer root.Close()
	parent, parentInfo, err := projectDirectory(root, path.Dir(name))
	if err != nil {
		return protocol.ProjectFile{}, err
	}
	defer parent.Close()
	base := path.Base(name)
	mode := fs.FileMode(0644)
	var reviewed fs.FileInfo
	if *in.ExpectedSHA256 == "" {
		if _, err := parent.Lstat(base); err == nil {
			return protocol.ProjectFile{}, projectProblem(409, "file_conflict", "The destination already exists; read it before replacing it.")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return protocol.ProjectFile{}, err
		}
	} else {
		current, info, err := projectRead(parent, base)
		if errors.Is(err, fs.ErrNotExist) {
			return protocol.ProjectFile{}, projectProblem(409, "file_conflict", "The reviewed file no longer exists.")
		}
		if err != nil {
			return protocol.ProjectFile{}, err
		}
		if current.SHA256 != *in.ExpectedSHA256 {
			return protocol.ProjectFile{}, projectProblem(409, "file_conflict", "The project file changed since it was reviewed.")
		}
		reviewed, mode = info, info.Mode().Perm()
	}
	stage := ".axv-project-write-" + rand.Text()
	file, err := parent.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return protocol.ProjectFile{}, err
	}
	defer parent.Remove(stage)
	if _, err = io.WriteString(file, in.Content); err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	var staged fs.FileInfo
	if err == nil {
		staged, err = file.Stat()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return protocol.ProjectFile{}, err
	}
	if err = s.verifyProjectFiles(in.ProjectFileScope, rootInfo); err != nil {
		return protocol.ProjectFile{}, err
	}
	if err = projectVerifyParent(root, name, parentInfo); err != nil {
		return protocol.ProjectFile{}, err
	}
	if err = ctx.Err(); err != nil {
		return protocol.ProjectFile{}, err
	}
	stagedFile, stagedInfo, err := projectRead(parent, stage)
	if err != nil {
		return protocol.ProjectFile{}, err
	}
	if !os.SameFile(staged, stagedInfo) || stagedFile.SHA256 != projectDigest([]byte(in.Content)) {
		return protocol.ProjectFile{}, projectProblem(409, "file_conflict", "The staged project file changed before publication.")
	}
	if reviewed != nil {
		current, info, err := projectRead(parent, base)
		if err != nil {
			return protocol.ProjectFile{}, err
		}
		if !os.SameFile(reviewed, info) || current.SHA256 != *in.ExpectedSHA256 {
			return protocol.ProjectFile{}, projectProblem(409, "file_conflict", "The reviewed file changed before publication.")
		}
		// The final hash/identity check and rename serialize AXV writers. An
		// unrelated guest process can still write between them; this is not a
		// filesystem compare-and-swap primitive.
		err = parent.Rename(stage, base)
	} else {
		// Hard-link publication creates a new entry atomically without replacing
		// a file that appeared after the initial absence check.
		err = parent.Link(stage, base)
	}
	if err != nil {
		return protocol.ProjectFile{}, err
	}
	if reviewed == nil {
		if err = parent.Remove(stage); err != nil {
			return protocol.ProjectFile{}, err
		}
	}
	if err = projectSyncDirectory(parent); err != nil {
		return protocol.ProjectFile{}, err
	}
	if err = s.verifyProjectFiles(in.ProjectFileScope, rootInfo); err != nil {
		return protocol.ProjectFile{}, err
	}
	if err = projectVerifyParent(root, name, parentInfo); err != nil {
		return protocol.ProjectFile{}, err
	}
	result, _, err := projectRead(parent, base)
	if err == nil && result.SHA256 != projectDigest([]byte(in.Content)) {
		err = projectProblem(409, "file_conflict", "The published file changed before confirmation. Read it before retrying.")
	}
	result.Path = name
	return result, err
}

func (s *service) mkdirProjectFile(w http.ResponseWriter, r *http.Request) {
	var in protocol.ProjectFileMkdirRequest
	if err := decodeProjectFile(w, r, &in, false); err != nil {
		projectFailure(w, err)
		return
	}
	name, err := projectRelative(in.Path, false)
	if err != nil {
		projectFailure(w, err)
		return
	}
	s.projectFilesMu.Lock()
	defer s.projectFilesMu.Unlock()
	root, rootInfo, _, err := s.openProjectFiles(in.ProjectFileScope, false)
	if err != nil {
		projectFailure(w, err)
		return
	}
	defer root.Close()
	parent, parentInfo, err := projectDirectory(root, path.Dir(name))
	if err != nil {
		projectFailure(w, err)
		return
	}
	defer parent.Close()
	if err = s.verifyProjectFiles(in.ProjectFileScope, rootInfo); err == nil {
		err = projectVerifyParent(root, name, parentInfo)
	}
	if err == nil {
		err = r.Context().Err()
	}
	if err == nil {
		err = parent.Mkdir(path.Base(name), 0755)
	}
	if err == nil {
		err = projectSyncDirectory(parent)
	}
	if err == nil {
		err = s.verifyProjectFiles(in.ProjectFileScope, rootInfo)
	}
	if err == nil {
		err = projectVerifyParent(root, name, parentInfo)
	}
	if err != nil {
		projectFailure(w, err)
		return
	}
	info, err := parent.Lstat(path.Base(name))
	if err != nil {
		projectFailure(w, err)
		return
	}
	respond(w, 201, projectEntry(name, info))
}
