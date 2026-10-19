// Package httpapi adapts the local Codex service to HTTP and WebSocket clients.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	"github.com/cofy-x/axv/apps/server/internal/hostdir"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type Handler struct {
	manager *codex.Manager
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	wg      sync.WaitGroup
	closed  bool
}

func New(manager *codex.Manager) *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Handler{manager: manager, ctx: ctx, cancel: cancel}
	go func() {
		select {
		case <-manager.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return h
}

// Close interrupts and drains HTTP clients, including incomplete request bodies.
// The service independently owns run operations and persistent processes.
func (h *Handler) Close() {
	h.mu.Lock()
	h.closed = true
	h.cancel()
	h.mu.Unlock()
	h.wg.Wait()
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/host/codex/info", h.handle(h.info))
	mux.HandleFunc("GET /api/host/codex/projects", h.handle(h.projects))
	mux.HandleFunc("GET /api/host/codex/sessions", h.handle(h.list))
	mux.HandleFunc("POST /api/host/codex/sessions", h.handle(h.create))
	mux.HandleFunc("GET /api/host/codex/sessions/{id}", h.handle(h.get))
	mux.HandleFunc("PATCH /api/host/codex/sessions/{id}", h.handle(h.patch))
	mux.HandleFunc("POST /api/host/codex/sessions/{id}/resume", h.handle(h.resume))
	mux.HandleFunc("GET /api/host/codex/sessions/{id}/runs/{run}", h.handle(h.getRun))
	mux.HandleFunc("GET /api/host/codex/sessions/{id}/runs/{run}/context", h.handle(h.contextBatches))
	mux.HandleFunc("POST /api/host/codex/sessions/{id}/runs/{run}/context", h.handle(h.queueContext))
	mux.HandleFunc("DELETE /api/host/codex/sessions/{id}/runs/{run}/context/{batch}", h.handle(h.cancelContext))
	mux.HandleFunc("POST /api/host/codex/sessions/{id}/runs/{run}/end", h.handle(h.end))
	mux.HandleFunc("GET /api/host/codex/sessions/{id}/runs/{run}/terminal", h.handle(h.connect))
}

func (h *Handler) contextBatches(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, false) {
		return
	}
	batches, err := h.manager.ContextBatches(r.Context(), r.PathValue("id"), r.PathValue("run"))
	respond(w, map[string]any{"batches": batches}, err)
}

func (h *Handler) queueContext(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, true) {
		return
	}
	var input struct {
		RequestID   string   `json:"requestId"`
		SnapshotIDs []string `json:"snapshotIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	batch, err := h.manager.QueueContext(r.Context(), r.PathValue("id"), r.PathValue("run"), codex.ContextInput{RequestID: input.RequestID, SnapshotIDs: input.SnapshotIDs})
	respond(w, map[string]any{"batch": batch}, err)
}

func (h *Handler) cancelContext(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, true) {
		return
	}
	batch, err := h.manager.CancelContext(r.Context(), r.PathValue("id"), r.PathValue("run"), r.PathValue("batch"))
	respond(w, map[string]any{"batch": batch}, err)
}

func (h *Handler) handle(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		stopping := h.closed || h.ctx.Err() != nil
		select {
		case <-h.manager.Done():
			stopping = true
		default:
		}
		if stopping {
			h.mu.Unlock()
			respond(w, nil, requestFailure(503, "server_stopping", "AXV is stopping. Try again after restarting it."))
			return
		}
		h.wg.Add(1)
		h.mu.Unlock()
		defer h.wg.Done()
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(h.ctx, cancel)
		controller := http.NewResponseController(w)
		ioDone := make(chan struct{})
		stopIO := context.AfterFunc(ctx, func() {
			defer close(ioDone)
			// Context cancellation alone cannot interrupt partial JSON bodies.
			// Hijacked WebSockets handle cancellation in their bridge.
			_ = controller.SetReadDeadline(time.Now())
			_ = controller.SetWriteDeadline(time.Now())
		})
		defer cancel()
		defer stop()
		defer func() {
			if !stopIO() {
				<-ioDone
			}
		}()
		handler(w, r.WithContext(ctx))
	}
}

func allowed(w http.ResponseWriter, r *http.Request, origin bool) bool {
	if localaccess.AllowRequest(r, origin) {
		return true
	}
	respond(w, nil, requestFailure(403, "host_forbidden", "Host Codex requires a same-origin connection from this computer."))
	return false
}

type requestError struct {
	status        int
	code, message string
}

func (e *requestError) Error() string { return e.message }

func requestFailure(status int, code, message string) error {
	return &requestError{status: status, code: code, message: message}
}

func storageStatus(code string) int {
	switch code {
	case "not_found":
		return http.StatusNotFound
	case "invalid_metadata", "invalid_session":
		return http.StatusBadRequest
	case "metadata_conflict", "request_conflict", "conversation_active", "run_changed", "history_unavailable", "history_identity_conflict", "invalid_run_state":
		return http.StatusConflict
	case "codex_limit":
		return http.StatusServiceUnavailable
	default:
		return 0
	}
}

func respond(w http.ResponseWriter, value any, err error) {
	status := http.StatusOK
	if err != nil {
		var request *requestError
		var service *codex.Error
		var stored *storage.Error
		var directory *hostdir.Error
		switch {
		case errors.As(err, &request):
			status = request.status
			value = map[string]string{"code": request.code, "error": request.message}
		case errors.As(err, &service):
			switch service.Kind {
			case codex.Invalid:
				status = http.StatusBadRequest
			case codex.Conflict:
				status = http.StatusConflict
			default:
				status = http.StatusServiceUnavailable
			}
			value = map[string]string{"code": service.Code, "error": service.Message}
		case errors.As(err, &directory):
			switch directory.Kind {
			case hostdir.Unreadable:
				status = http.StatusForbidden
			case hostdir.HomeUnavailable:
				status = http.StatusServiceUnavailable
			default:
				status = http.StatusBadRequest
			}
			value = map[string]string{"code": directory.Code, "error": directory.Message}
		case errors.As(err, &stored) && storageStatus(stored.Code) != 0:
			status = storageStatus(stored.Code)
			value = map[string]string{"code": stored.Code, "error": stored.Message}
		default:
			log.Printf("Codex operation failed: %v", err)
			status = http.StatusInternalServerError
			value = map[string]string{"code": "internal_error", "error": "The Codex operation could not be completed. Try again."}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	content, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if content != "application/json" {
		respond(w, nil, requestFailure(415, "invalid_content_type", "Use application/json."))
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		respond(w, nil, requestFailure(400, "invalid_json", "Send a valid JSON object."))
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		respond(w, nil, requestFailure(400, "invalid_json", "Send one JSON object."))
		return false
	}
	return true
}

func (h *Handler) info(w http.ResponseWriter, r *http.Request) {
	if allowed(w, r, false) {
		info, err := h.manager.Info(r.Context())
		respond(w, info, err)
	}
}

func (h *Handler) projects(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, false) {
		return
	}
	projects, err := h.manager.Projects(r.Context())
	respond(w, map[string]any{"projects": projects}, err)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, false) {
		return
	}
	sessions, err := h.manager.List(r.Context())
	respond(w, map[string]any{"sessions": sessions}, err)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, false) {
		return
	}
	session, err := h.manager.Get(r.Context(), r.PathValue("id"))
	respond(w, map[string]any{"session": session}, err)
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, false) {
		return
	}
	session, err := h.manager.GetRun(r.Context(), r.PathValue("id"), r.PathValue("run"))
	respond(w, map[string]any{"session": session}, err)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, true) {
		return
	}
	var input struct {
		RequestID   string `json:"requestId"`
		ProjectPath string `json:"projectPath,omitempty"`
		Cols        *int   `json:"cols,omitempty"`
		Rows        *int   `json:"rows,omitempty"`
	}
	if !decode(w, r, &input) {
		return
	}
	session, err := h.manager.Create(r.Context(), codex.StartInput{RequestID: input.RequestID, ProjectPath: input.ProjectPath, Cols: input.Cols, Rows: input.Rows})
	respond(w, map[string]any{"session": session}, err)
}

func (h *Handler) resume(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, true) {
		return
	}
	var input struct {
		RequestID     string `json:"requestId"`
		ExpectedRunID string `json:"expectedRunId"`
		Cols          *int   `json:"cols,omitempty"`
		Rows          *int   `json:"rows,omitempty"`
	}
	if !decode(w, r, &input) {
		return
	}
	session, err := h.manager.Resume(r.Context(), r.PathValue("id"), codex.ResumeInput{RequestID: input.RequestID, ExpectedRunID: input.ExpectedRunID, Cols: input.Cols, Rows: input.Rows})
	respond(w, map[string]any{"session": session}, err)
}

func (h *Handler) end(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, true) {
		return
	}
	session, err := h.manager.End(r.Context(), r.PathValue("id"), r.PathValue("run"))
	respond(w, map[string]any{"session": session}, err)
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, true) {
		return
	}
	var input struct {
		Revision int             `json:"revision"`
		Title    json.RawMessage `json:"title"`
		Archived json.RawMessage `json:"archived"`
	}
	if !decode(w, r, &input) {
		return
	}
	patch := storage.CodexMetadataPatch{Revision: input.Revision}
	if input.Title != nil {
		var title string
		if !utf8.Valid(input.Title) || bytes.Equal(bytes.TrimSpace(input.Title), []byte("null")) || json.Unmarshal(input.Title, &title) != nil {
			respond(w, nil, requestFailure(400, "invalid_metadata", "Provide a string for the session title."))
			return
		}
		patch.Title = &title
	}
	if input.Archived != nil {
		var archived bool
		if bytes.Equal(bytes.TrimSpace(input.Archived), []byte("null")) || json.Unmarshal(input.Archived, &archived) != nil {
			respond(w, nil, requestFailure(400, "invalid_metadata", "Provide a boolean for archived."))
			return
		}
		patch.Archived = &archived
	}
	session, err := h.manager.Patch(r.Context(), r.PathValue("id"), patch)
	respond(w, map[string]any{"session": session}, err)
}
