// Package httpapi adapts registered host-project files and document exchange
// to the local desktop API. Filesystem and recovery rules belong to projectfiles.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"

	"github.com/cofy-x/axv/apps/server/internal/localaccess"
	"github.com/cofy-x/axv/apps/server/internal/projectfiles"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type api struct {
	store   *storage.Store
	service *projectfiles.Service
}

// Register composes this feature independently from coding-agent lifecycle.
func Register(mux *http.ServeMux, store *storage.Store) {
	a := &api{store: store, service: projectfiles.New(store)}
	register := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if !localaccess.AllowRequest(r, r.Method != http.MethodGet && r.Method != http.MethodHead) {
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

func problem(code, message string) error { return &storage.Error{Code: code, Message: message} }

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
