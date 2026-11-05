package notifications

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func failure(w http.ResponseWriter, err error) {
	status, code, message := 503, "notification_store_unavailable", "Notification storage is unavailable. Existing records were preserved."
	switch {
	case errors.Is(err, ErrConflict):
		status, code, message = 409, "notification_conflict", "The original notification store or configuration changed. Review it before trying again."
	case errors.Is(err, ErrInput):
		status, code, message = 400, "invalid_notification_request", "Review the notification settings and saved credentials."
	case errors.Is(err, ErrMissing):
		status, code, message = 404, "notification_unavailable", "The original notification is unavailable."
	}
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}

func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/notifications", m.serve)
	mux.HandleFunc("/api/notifications/", m.serve)
}

func (m *Manager) serve(w http.ResponseWriter, r *http.Request) {
	if !httpaccess.AllowRequest(r, r.Method != http.MethodGet) {
		writeJSON(w, 403, map[string]string{"code": "forbidden", "error": "Owner access is required."})
		return
	}
	local := httpaccess.LocalRequest(r, false)
	path := strings.TrimPrefix(r.URL.Path, "/api/notifications")
	if r.Method == http.MethodGet && path == "" {
		if r.URL.RawQuery != "" {
			failure(w, ErrInput)
			return
		}
		snapshot := m.Snapshot()
		snapshot.CanConfigure = local
		if !local {
			snapshot.Config = Config{Enabled: snapshot.Config.Enabled, EventTypes: snapshot.Config.EventTypes}
		}
		writeJSON(w, 200, snapshot)
		return
	}
	if r.Method == http.MethodGet && path != "" {
		query, err := parseDetail(r)
		if err != nil {
			failure(w, err)
			return
		}
		event, resolution, err := m.Detail(r.Context(), query, strings.TrimPrefix(path, "/"))
		if err != nil {
			failure(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"storeId": query, "event": event, "resolution": resolution})
		return
	}
	if r.Method != http.MethodPost || !httpaccess.LocalRequest(r, true) {
		writeJSON(w, 403, map[string]string{"code": "forbidden", "error": "Configure notifications on the local AXV host."})
		return
	}
	if r.URL.RawQuery != "" || strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		failure(w, ErrInput)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var err error
	switch path {
	case "/config":
		var input Update
		if err = decodeStrict(r.Body, &input); err == nil {
			err = m.Configure(input)
		}
	case "/test", "/disable":
		var input struct {
			StoreID  string `json:"storeId"`
			Revision uint64 `json:"revision"`
		}
		if err = decodeStrict(r.Body, &input); err == nil {
			if path == "/test" {
				err = m.Test(input.StoreID, input.Revision)
			} else {
				err = m.Disable(input.StoreID, input.Revision)
			}
		}
	default:
		err = ErrMissing
	}
	if err != nil {
		failure(w, err)
		return
	}
	snapshot := m.Snapshot()
	snapshot.CanConfigure = true
	writeJSON(w, 200, snapshot)
}

func parseDetail(r *http.Request) (string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	// Reject unknown, duplicate and malformed query forms.
	if err != nil || r.URL.RawQuery == "" || len(q) != 1 || len(q["storeId"]) != 1 || q.Get("storeId") == "" {
		err = ErrInput
	}
	return q.Get("storeId"), err
}
