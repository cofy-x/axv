// Package httpapi adapts Cloudflare management operations to the desktop HTTP API.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/management"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

type Handler struct{ service *management.Service }

func New(service *management.Service) *Handler { return &Handler{service: service} }
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(value)
}

// WriteError emits the safe Cloudflare error shape, including unavailable feature fallbacks.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "error": message})
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.URL.RawQuery != "" || r.URL.ForceQuery || mediaErr != nil || media != "application/json" {
		WriteError(w, 400, "invalid_cloudflare_request", "Provide one Cloudflare JSON object without query parameters.")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	// A pointer wrapper rejects JSON null without losing strict field checks.
	var raw json.RawMessage
	if d.Decode(&raw) != nil || len(raw) == 0 || raw[0] != '{' || !jsonunicode.Valid(raw) || d.Decode(new(any)) != io.EOF {
		WriteError(w, 400, "invalid_cloudflare_request", "Provide one valid Cloudflare JSON object.")
		return false
	}
	d = json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		WriteError(w, 400, "invalid_cloudflare_request", "Cloudflare settings contain invalid or unknown fields.")
		return false
	}
	return true
}

func (m *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cloudflare/config", m.getConfig)
	mux.HandleFunc("PUT /api/cloudflare/config", m.putConfig)
	mux.HandleFunc("PUT /api/cloudflare/token", m.rotateToken)
	mux.HandleFunc("POST /api/cloudflare/wizard", m.wizard)
	mux.HandleFunc("GET /api/cloudflare/health", m.getHealth)
	mux.HandleFunc("GET /api/cloudflare/traffic", m.getTraffic)
	mux.HandleFunc("GET /api/cloudflare/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if !localaccess.AllowRequest(r, false) {
			WriteError(w, 403, "cloudflare_forbidden", "Authenticated desktop access is required.")
			return
		}
		writeJSON(w, map[string]bool{"configure": localaccess.Request(r, false), "publish": true, "health": true, "traffic": true})
	})
}

func (m *Handler) getConfig(w http.ResponseWriter, r *http.Request) {
	if !localaccess.Request(r, false) {
		WriteError(w, 403, "cloudflare_local_only", "Configure Cloudflare on the computer running AXV.")
		return
	}
	value, err := m.service.Configuration(r.Context())
	reply(w, r, value, err)
}

func (m *Handler) putConfig(w http.ResponseWriter, r *http.Request) {
	if !localaccess.Request(r, true) {
		WriteError(w, 403, "cloudflare_local_only", "Configure Cloudflare on the computer running AXV.")
		return
	}
	var input management.ConfigUpdate
	if !decode(w, r, &input) {
		return
	}
	value, err := m.service.SaveConfiguration(r.Context(), input)
	reply(w, r, value, err)
}

func (m *Handler) rotateToken(w http.ResponseWriter, r *http.Request) {
	if !localaccess.Request(r, true) {
		WriteError(w, 403, "cloudflare_local_only", "Rotate Cloudflare credentials on the computer running AXV.")
		return
	}
	var input management.TokenUpdate
	if !decode(w, r, &input) {
		return
	}
	value, err := m.service.RotateToken(r.Context(), input)
	reply(w, r, value, err)
}

func (m *Handler) wizard(w http.ResponseWriter, r *http.Request) {
	if !localaccess.Request(r, true) {
		WriteError(w, 403, "cloudflare_local_only", "Configure Cloudflare on the computer running AXV.")
		return
	}
	var input management.WizardInput
	if !decode(w, r, &input) {
		return
	}
	value, err := m.service.Wizard(r.Context(), input)
	reply(w, r, value, err)
}

func (m *Handler) getHealth(w http.ResponseWriter, r *http.Request) {
	if !localaccess.AllowRequest(r, false) {
		WriteError(w, 403, "cloudflare_forbidden", "Authenticated desktop access is required.")
		return
	}
	if r.URL.RawQuery != "" && r.URL.RawQuery != "force=true" {
		WriteError(w, 400, "invalid_cloudflare_request", "Use only force=true to refresh tunnel health.")
		return
	}
	value, err := m.service.Health(r.Context(), r.URL.Query().Get("force") == "true")
	reply(w, r, value, err)
}

func (m *Handler) getTraffic(w http.ResponseWriter, r *http.Request) {
	if !localaccess.AllowRequest(r, false) {
		WriteError(w, 403, "cloudflare_forbidden", "Authenticated desktop access is required.")
		return
	}
	hours := 24
	query := r.URL.Query()
	if len(query) > 1 || len(query["hours"]) > 1 || len(query) == 1 && query.Get("hours") == "" {
		WriteError(w, 400, "invalid_cloudflare_request", "Choose a traffic window of 1, 6, 12, 24, 168 or 720 hours.")
		return
	}
	if value := query.Get("hours"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			WriteError(w, 400, "invalid_cloudflare_request", "Choose a traffic window of 1, 6, 12, 24, 168 or 720 hours.")
			return
		}
		hours = n
	}
	value, err := m.service.Traffic(r.Context(), hours)
	reply(w, r, value, err)
}

func reply(w http.ResponseWriter, r *http.Request, value any, err error) {
	if r.Context().Err() != nil {
		return
	}
	if err == nil {
		writeJSON(w, value)
		return
	}
	var failure *management.Problem
	if !errors.As(err, &failure) {
		WriteError(w, 503, "cloudflare_stopping", "The Cloudflare operation could not complete. Inspect its current status.")
		return
	}
	WriteError(w, problemStatus(failure.Code), failure.Code, failure.Message)
}

func problemStatus(code string) int {
	status := http.StatusServiceUnavailable
	switch code {
	case "invalid_cloudflare_request", "invalid_cloudflare_config", "invalid_cloudflare_token_action", "invalid_cloudflare_token", "invalid_cloudflare_step", "cloudflare_validation_failed", "cloudflare_zone_mismatch", "cloudflare_domain_mismatch", "cloudflare_domain_isolation", "cloudflare_tunnel_local":
		status = http.StatusBadRequest
	case "cloudflare_close_failed", "cloudflare_instance_changed", "cloudflare_config_conflict", "cloudflare_config_busy", "cloudflare_disable_required", "cloudflare_publications_active", "cloudflare_desktop_site_reserved":
		status = http.StatusConflict
	case "cloudflare_config_failed":
		status = http.StatusInternalServerError
	case "cloudflare_stopping":
		status = http.StatusServiceUnavailable
	}
	return status
}
