package extensions

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

const ContentPolicy = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; frame-src 'none'; worker-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

const StoreHeader = "X-AXV-Extension-Store"

func (m *Manager) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/extensions", m.guard(false, m.catalog))
	mux.Handle("POST /api/extensions/install", m.guard(true, m.install))
	mux.Handle("PUT /api/extensions/{id}", m.guard(true, m.enable))
	mux.Handle("DELETE /api/extensions/{id}", m.guard(true, m.uninstall))
	mux.Handle("GET /api/extensions/{id}/data", m.guard(false, m.data))
	mux.Handle("DELETE /api/extensions/{id}/data", m.guard(true, m.purge))
	mux.Handle("POST /api/extensions/{id}/sessions", m.guard(false, m.create))
	mux.Handle("POST /api/extensions/{id}/sessions/{session}/rpc", m.guard(false, m.call))
	mux.Handle("DELETE /api/extensions/{id}/sessions/{session}", m.guard(false, m.destroy))
	mux.Handle("GET /api/extensions/{id}/content/{storeId}/{digest}", m.guard(false, m.content))
}

func (m *Manager) guard(localOnly bool, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "application/json")
		requireOrigin := r.Method != http.MethodGet && r.Method != http.MethodHead
		allowed := httpaccess.AllowRequest(r, requireOrigin)
		if localOnly {
			allowed = httpaccess.LocalRequest(r, requireOrigin)
		}
		// Opaque sandbox documents can issue simple GETs without Origin. Fetch
		// Metadata closes that browser path as well as explicit Origin:null.
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			allowed = false
		}
		if !allowed {
			writeError(w, failure("extension_forbidden", "Open this extension operation from the authorized AXV desktop."))
			return
		}
		// The Store pointer and UUID remain fixed for this handler's lifetime.
		// Checking the captured store here therefore also binds the subsequent
		// transaction, without a separate catalog-read/write race.
		if r.URL.Path != "/api/extensions" {
			expected := r.PathValue("storeId")
			if expected == "" {
				values := r.Header.Values(StoreHeader)
				if len(values) != 1 || values[0] == "" {
					writeError(w, failure("invalid_extension_request", "Provide the extension store identity captured by this operation."))
					return
				}
				expected = values[0]
			}
			if expected != m.store.ID() {
				writeError(w, storeChanged())
				return
			}
		}
		next(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "extension_unavailable", "The extension operation could not complete. Try again."
	var e *Error
	if errors.As(err, &e) {
		code, message = e.Code, e.Message
		switch code {
		case "extension_forbidden":
			status = http.StatusForbidden
		case "extension_not_found":
			status = http.StatusNotFound
		case "extension_conflict", "revision_conflict", "extension_session_expired", "extension_store_changed":
			status = http.StatusConflict
		case "extension_session_limit":
			status = http.StatusTooManyRequests
		default:
			status = http.StatusBadRequest
		}
	}
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}

func readJSON(w http.ResponseWriter, r *http.Request, limit int64, into any) error {
	if media := strings.Split(r.Header.Get("Content-Type"), ";")[0]; strings.TrimSpace(media) != "application/json" {
		return failure("invalid_extension_request", "Send an application/json request.")
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return failure("invalid_extension_request", "The extension request exceeds its size limit.")
	}
	if err = decodeJSON(data, into); err != nil {
		return failure("invalid_extension_request", "Provide only the documented JSON fields and one complete value.")
	}
	return nil
}

func (m *Manager) catalog(w http.ResponseWriter, r *http.Request) {
	apps, err := m.store.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"storeId": m.store.ID(), "apps": apps})
}

func (m *Manager) install(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Package            *Package `json:"package"`
		ExpectedGeneration *int64   `json:"expectedGeneration,omitempty"`
	}
	if err := readJSON(w, r, MaxPackageBytes+4096, &input); err != nil {
		writeError(w, err)
		return
	}
	if input.Package == nil {
		writeError(w, failure("invalid_extension_package", "Provide an extension package."))
		return
	}
	app, created, err := m.store.Install(r.Context(), *input.Package, input.ExpectedGeneration)
	if err != nil {
		writeError(w, err)
		return
	}
	m.revokeBefore(app.ID, app.Generation)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]Record{"app": app})
}

func (m *Manager) enable(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled            *bool  `json:"enabled"`
		ExpectedGeneration *int64 `json:"expectedGeneration"`
	}
	if err := readJSON(w, r, 4096, &input); err != nil {
		writeError(w, err)
		return
	}
	if input.Enabled == nil || input.ExpectedGeneration == nil {
		writeError(w, failure("invalid_extension_request", "Provide enabled and expectedGeneration."))
		return
	}
	app, err := m.store.Change(r.Context(), r.PathValue("id"), *input.ExpectedGeneration, *input.Enabled, true)
	if err != nil {
		writeError(w, err)
		return
	}
	m.revokeBefore(app.ID, app.Generation)
	writeJSON(w, http.StatusOK, map[string]Record{"app": app})
}

func queryRevision(r *http.Request, key string) (int64, error) {
	values := r.URL.Query()
	if len(values) != 1 || len(values[key]) != 1 {
		return 0, failure("invalid_extension_request", "Provide exactly one "+key+" query parameter.")
	}
	n, err := strconv.ParseInt(values.Get(key), 10, 64)
	if err != nil || n < 0 {
		return 0, failure("invalid_extension_request", "Provide a valid "+key+".")
	}
	return n, nil
}

func (m *Manager) uninstall(w http.ResponseWriter, r *http.Request) {
	generation, err := queryRevision(r, "generation")
	if err != nil {
		writeError(w, err)
		return
	}
	app, err := m.store.Change(r.Context(), r.PathValue("id"), generation, false, false)
	if err != nil {
		writeError(w, err)
		return
	}
	m.revokeBefore(app.ID, app.Generation)
	writeJSON(w, http.StatusOK, map[string]Record{"app": app})
}

func (m *Manager) data(w http.ResponseWriter, r *http.Request) {
	doc, err := m.store.Data(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}
func (m *Manager) purge(w http.ResponseWriter, r *http.Request) {
	revision, err := queryRevision(r, "revision")
	if err != nil {
		writeError(w, err)
		return
	}
	doc, err := m.store.Purge(r.Context(), r.PathValue("id"), revision)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (m *Manager) create(w http.ResponseWriter, r *http.Request) {
	var input SessionInput
	if err := readJSON(w, r, 4096, &input); err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	key, err := m.createSession(r.Context(), id, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"sessionId": key, "contentUrl": "/api/extensions/" + id + "/content/" + m.store.ID() + "/" + input.Digest})
}

func (m *Manager) call(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Method string `json:"method"`
		Params *struct {
			Value            json.RawMessage `json:"value"`
			ExpectedRevision *int64          `json:"expectedRevision"`
		} `json:"params,omitempty"`
	}
	if err := readJSON(w, r, MaxDataBytes+4096, &input); err != nil {
		writeError(w, err)
		return
	}
	var value json.RawMessage
	var expected *int64
	if input.Params != nil {
		if input.Method != "storage.set" {
			writeError(w, failure("invalid_extension_rpc", "This method does not accept parameters."))
			return
		}
		value, expected = input.Params.Value, input.Params.ExpectedRevision
	}
	doc, err := m.rpc(r.Context(), r.PathValue("id"), r.PathValue("session"), input.Method, value, expected)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (m *Manager) destroy(w http.ResponseWriter, r *http.Request) {
	if err := m.release(r.PathValue("id"), r.PathValue("session")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
func (m *Manager) content(w http.ResponseWriter, r *http.Request) {
	html, err := m.store.Content(r.Context(), r.PathValue("id"), r.PathValue("digest"))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", ContentPolicy)
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	_, _ = io.WriteString(w, html)
}
