package projectfiles

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/hostdir"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type API struct {
	store *storage.Store
}

// Register composes this feature independently from coding-agent lifecycle.
func Register(mux *http.ServeMux, store *storage.Store) {
	a := &API{store: store}
	register := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if !localaccess.Request(r, r.Method != http.MethodGet && r.Method != http.MethodHead) {
				fail(w, problem("host_forbidden", "Project files require a same-origin connection from this computer."))
				return
			}
			handler(w, r)
		})
	}
	register("GET /api/projects", a.projects)
	register("POST /api/projects", a.registerProject)
	register("GET /api/projects/{id}/files", a.files)
	register("GET /api/projects/{id}/file", a.file)
	register("GET /api/projects/{id}/download", a.download)
	register("POST /api/projects/{id}/import", a.importFile)
	register("POST /api/projects/{id}/export", a.exportSnapshot)
	register("GET /api/documents/{id}/links", a.links)
	register("GET /api/documents/{id}/origins", a.origins)
	register("POST /api/documents/{id}/links", a.addLink)
	register("GET /api/document-links", a.related)
	register("DELETE /api/document-links/{id}", a.removeLink)
	register("GET /api/documents/{id}/snapshots", a.snapshots)
	register("POST /api/documents/{id}/snapshots", a.createSnapshot)
	register("GET /api/document-snapshots/{id}", a.snapshot)
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("project files response: %v", err)
	}
}
func fail(w http.ResponseWriter, err error) {
	var e *storage.Error
	status := 500
	code, message := "internal_error", "The document operation could not be completed."
	if errors.As(err, &e) {
		code, message = e.Code, e.Message
		switch e.Code {
		case "not_found":
			status = 404
		case "host_forbidden", "file_forbidden":
			status = 403
		case "invalid_name", "invalid_parent", "invalid_link", "invalid_project", "invalid_path", "invalid_cursor", "invalid_directory", "invalid_request", "not_file", "not_text":
			status = 400
		case "project_changed", "revision_conflict", "unavailable", "idempotency_conflict", "name_conflict", "source_changed", "transfer_unresolved":
			status = 409
		case "file_too_large", "text_too_large":
			status = 413
		case "unsupported":
			status = 501
		case "project_unavailable", "file_unavailable":
			status = 503
		}
	}
	if status == 500 {
		log.Printf("project files operation failed: %v", err)
		code, message = "internal_error", "The document operation could not be completed."
	}
	write(w, status, map[string]string{"code": code, "error": message})
}
func reply(w http.ResponseWriter, value any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 200, value)
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	typ, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if typ != "application/json" {
		write(w, 415, map[string]string{"code": "invalid_content_type", "error": "Use application/json."})
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		fail(w, problem("invalid_request", "Send one valid JSON object."))
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		fail(w, problem("invalid_request", "Send one valid JSON object."))
		return false
	}
	return true
}
func query(r *http.Request, names ...string) error {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return problem("invalid_request", "Invalid request query.")
	}
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	for key, values := range q {
		if !allowed[key] || len(values) != 1 {
			return problem("invalid_request", "Use one value per supported query parameter.")
		}
	}
	return nil
}

func (a *API) projects(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.Projects(r.Context())
	reply(w, map[string]any{"projects": items}, err)
}
func (a *API) registerProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		RootPath string `json:"rootPath"`
	}
	if !decode(w, r, &in) {
		return
	}
	canonical, err := hostdir.CanonicalDirectory(in.RootPath)
	if err != nil {
		fail(w, problem("invalid_project", "Choose an existing readable absolute project directory."))
		return
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		fail(w, fileError(err))
		return
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		fail(w, err)
		return
	}
	identity, err := rootIdentity(info)
	if err != nil {
		fail(w, err)
		return
	}
	if in.Name == "" {
		in.Name = filepath.Base(canonical)
	}
	p, err := a.store.RegisterProject(r.Context(), in.Name, canonical, identity)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 201, map[string]any{"project": p})
}
func (a *API) project(ctx context.Context, projectID string) (storage.ProjectRef, *os.Root, error) {
	p, err := a.store.Project(ctx, projectID)
	if err != nil {
		return p, nil, err
	}
	root, err := openProject(p)
	return p, root, err
}
func (a *API) files(w http.ResponseWriter, r *http.Request) {
	if err := query(r, "path", "cursor", "limit"); err != nil {
		fail(w, err)
		return
	}
	q := r.URL.Query()
	dir, err := relative(q.Get("path"), true)
	if err != nil {
		fail(w, err)
		return
	}
	limit := 100
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 200 {
			fail(w, problem("invalid_request", "Directory limits must be between 1 and 200."))
			return
		}
	}
	p, root, err := a.project(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	defer root.Close()
	entries, next, truncated, err := list(root, dir, q.Get("cursor"), limit)
	reply(w, map[string]any{"project": p, "entries": entries, "nextCursor": next, "truncated": truncated}, err)
}
func (a *API) fileBytes(r *http.Request, max int64) (storage.ProjectRef, string, []byte, error) {
	var p storage.ProjectRef
	if err := query(r, "path"); err != nil {
		return p, "", nil, err
	}
	name, err := relative(r.URL.Query().Get("path"), false)
	if err != nil {
		return p, "", nil, err
	}
	p, root, err := a.project(r.Context(), r.PathValue("id"))
	if err != nil {
		return p, name, nil, err
	}
	defer root.Close()
	data, err := read(root, name, max)
	return p, name, data, err
}
func (a *API) file(w http.ResponseWriter, r *http.Request) {
	p, name, data, err := a.fileBytes(r, storage.MaxTextSize)
	if err != nil {
		fail(w, err)
		return
	}
	if !text(data) {
		fail(w, problem("not_text", "This file is not previewable text. Download the original file instead."))
		return
	}
	write(w, 200, map[string]any{"projectId": p.ID, "path": name, "name": filepath.Base(name), "content": string(data), "sha256": digest(data), "size": len(data)})
}
func (a *API) download(w http.ResponseWriter, r *http.Request) {
	_, name, data, err := a.fileBytes(r, storage.MaxFileSize)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(name)}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if _, err = w.Write(data); err != nil {
		log.Printf("project download response: %v", err)
	}
}

func (a *API) links(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.DocumentLinks(r.Context(), storage.DocumentLink{EntryID: r.PathValue("id")})
	reply(w, map[string]any{"links": items}, err)
}

func (a *API) origins(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.DocumentOrigins(r.Context(), r.PathValue("id"))
	reply(w, map[string]any{"origins": items}, err)
}
func (a *API) addLink(w http.ResponseWriter, r *http.Request) {
	var in storage.DocumentLink
	if !decode(w, r, &in) {
		return
	}
	if in.ID != "" || in.EntryID != "" || in.CreatedAt != "" {
		fail(w, problem("invalid_link", "Link identity is assigned by AXV."))
		return
	}
	l, err := a.store.AddDocumentLink(r.Context(), r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 201, map[string]any{"link": l})
}
func (a *API) removeLink(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]bool{"ok": true}, a.store.DeleteDocumentLink(r.Context(), r.PathValue("id")))
}
func (a *API) related(w http.ResponseWriter, r *http.Request) {
	if err := query(r, "projectId", "hostId", "machineId", "conversationId"); err != nil {
		fail(w, err)
		return
	}
	q := r.URL.Query()
	f := storage.DocumentLink{ProjectID: q.Get("projectId"), HostID: q.Get("hostId"), MachineID: q.Get("machineId"), ConversationID: q.Get("conversationId")}
	count := 0
	if f.ProjectID != "" {
		count++
	}
	if f.ConversationID != "" {
		count++
	}
	if f.HostID != "" || f.MachineID != "" {
		if f.HostID == "" || f.MachineID == "" {
			fail(w, problem("invalid_link", "Machine lookup requires its original host identity."))
			return
		}
		count++
	}
	if count != 1 {
		fail(w, problem("invalid_link", "Choose one project, conversation or host and machine."))
		return
	}
	items, err := a.store.DocumentLinks(r.Context(), f)
	reply(w, map[string]any{"links": items}, err)
}
func (a *API) snapshots(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.Snapshots(r.Context(), r.PathValue("id"))
	reply(w, map[string]any{"snapshots": items}, err)
}
func (a *API) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	d, err := a.store.CreateSnapshot(r.Context(), r.PathValue("id"), in.Revision)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 201, map[string]any{"snapshot": d})
}
func (a *API) snapshot(w http.ResponseWriter, r *http.Request) {
	d, err := a.store.Snapshot(r.Context(), r.PathValue("id"))
	reply(w, map[string]any{"snapshot": d}, err)
}

func validRequestID(value string) bool {
	return len(value) >= 8 && len(value) <= 128 && utf8Request(value)
}
func utf8Request(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n") && strings.TrimSpace(value) == value
}
