package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/desktop"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/management"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

func DesktopStatus(instanceID string, status func() desktop.Snapshot) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !localaccess.Request(r, false) {
			WriteError(w, http.StatusForbidden, "cloudflare_local_only", "Check Cloudflare access on the computer running AXV.")
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			InstanceID string `json:"instanceId"`
			desktop.Snapshot
		}{InstanceID: instanceID, Snapshot: status()})
	})
}

func DesktopSettings(controller DesktopController) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !localaccess.Request(r, true) {
			WriteError(w, http.StatusForbidden, "cloudflare_local_only", "Change Cloudflare access on the computer running AXV.")
			return
		}
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Enabled == nil || decoder.Decode(new(any)) != io.EOF {
			WriteError(w, http.StatusBadRequest, "invalid_cloudflare_request", "Provide only an enabled boolean to change Cloudflare access.")
			return
		}
		if err := controller.SetEnabled(r.Context(), *input.Enabled); err != nil {
			var failure *management.Problem
			if errors.As(err, &failure) {
				WriteError(w, problemStatus(failure.Code), failure.Code, failure.Message)
			} else {
				WriteError(w, http.StatusServiceUnavailable, "cloudflare_stopping", "The Cloudflare access change could not complete. Refresh its status.")
			}
			return
		}
		_ = json.NewEncoder(w).Encode(Connection(controller.Status()))
	})
}

// DesktopController keeps listener lifecycle ownership outside the HTTP adapter.
type DesktopController interface {
	Status() desktop.Snapshot
	SetEnabled(context.Context, bool) error
}
