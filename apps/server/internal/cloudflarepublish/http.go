package cloudflarepublish

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cloudflare-publications", m.listHTTP)
	mux.HandleFunc("POST /api/cloudflare-publications", m.publishHTTP)
	mux.HandleFunc("POST /api/cloudflare-publications/{id}/unpublish", m.unpublishHTTP)
	mux.HandleFunc("POST /api/cloudflare-publications/{id}/retry", m.retryHTTP)
}

func controlReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func controlError(w http.ResponseWriter, status int, code, message string) {
	controlReply(w, status, map[string]string{"code": code, "error": message})
}
func writeFailure(w http.ResponseWriter, err error) {
	var p *Problem
	if errors.As(err, &p) {
		controlError(w, p.Status, p.Code, p.Message)
	} else {
		controlError(w, 503, "publication_unavailable", "The publication change could not complete. Inspect its original status before retrying.")
	}
}

func allowed(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || !localaccess.AllowRequest(r, r.Method != http.MethodGet && r.Method != http.MethodHead) {
		controlError(w, 403, "publication_forbidden", "Open publishing controls from the AXV desktop.")
		return false
	}
	return true
}
func decodeControl(w http.ResponseWriter, r *http.Request, result any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return errors.New("application/json is required")
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil || !jsonunicode.Valid(data) {
		return errors.New("bounded valid Unicode JSON is required")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return errors.New("one JSON object is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(result); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("one JSON object is required")
	}
	return nil
}

func (m *Manager) listHTTP(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	settings, err := m.options.Settings()
	supported, reason := m.Availability()
	result := map[string]any{"instanceId": m.InstanceID(), "publications": m.List(), "supported": supported}
	if err == nil && validateManagement(settings) == nil {
		mode, _ := profile.CloudflareAllocationMode(settings.Domain)
		result["domain"] = settings.Domain
		result["isolation"] = map[string]any{"mode": mode, "domain": settings.Domain, "maxOwnerSessionSeconds": int(cloudflare.AccessLeaseDuration / time.Second), "uniqueHostnames": true, "retiredSitesReusable": false}
	}
	if !supported {
		result["reason"] = reason
	}
	controlReply(w, 200, result)
}
func (m *Manager) publishHTTP(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	var input Registration
	if decodeControl(w, r, &input) != nil || r.Header.Get("X-AXV-Host-ID") == "" || r.Header.Get("X-AXV-Host-ID") != input.Binding.HostID {
		controlError(w, 400, "invalid_publication_request", "Provide one bounded original publication request and its exact host identity.")
		return
	}
	value, err := m.Publish(r.Context(), input)
	if err != nil {
		writeFailure(w, err)
		return
	}
	controlReply(w, 202, value)
}
func (m *Manager) selected(w http.ResponseWriter, r *http.Request) (record, bool) {
	if !allowed(w, r) {
		return record{}, false
	}
	var empty struct{}
	if decodeControl(w, r, &empty) != nil {
		controlError(w, 400, "invalid_publication_request", "Provide an empty JSON object.")
		return record{}, false
	}
	item, ok := m.record(r.PathValue("id"))
	if !ok {
		controlError(w, 404, "publication_not_found", "The original publication was not found.")
		return record{}, false
	}
	if r.Header.Get("X-AXV-Host-ID") != item.Input.Binding.HostID {
		controlError(w, 409, "publication_binding_changed", "This action belongs to the publication's original VM host.")
		return record{}, false
	}
	return item, true
}
func (m *Manager) unpublishHTTP(w http.ResponseWriter, r *http.Request) {
	item, ok := m.selected(w, r)
	if !ok {
		return
	}
	value, err := m.Unpublish(r.Context(), item.Publication.ID)
	if err != nil {
		writeFailure(w, err)
		return
	}
	controlReply(w, 202, value)
}
func (m *Manager) retryHTTP(w http.ResponseWriter, r *http.Request) {
	item, ok := m.selected(w, r)
	if !ok {
		return
	}
	value, err := m.Retry(r.Context(), item.Publication.ID)
	if err != nil {
		writeFailure(w, err)
		return
	}
	controlReply(w, 202, value)
}
