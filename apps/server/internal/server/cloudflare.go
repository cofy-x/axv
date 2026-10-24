package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

func cloudflareStatusHandler(instanceID string, status func() cloudflareSnapshot) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !localaccess.Request(r, false) {
			remoteWriteError(w, http.StatusForbidden, "cloudflare_local_only", "Check Cloudflare access on the computer running AXV.")
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			InstanceID string `json:"instanceId"`
			cloudflareSnapshot
		}{InstanceID: instanceID, cloudflareSnapshot: status()})
	})
}

func cloudflareSettingsHandler(controller *cloudflareController) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !localaccess.Request(r, true) {
			remoteWriteError(w, http.StatusForbidden, "cloudflare_local_only", "Change Cloudflare access on the computer running AXV.")
			return
		}
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Enabled == nil || decoder.Decode(new(any)) != io.EOF {
			remoteWriteError(w, http.StatusBadRequest, "invalid_cloudflare_request", "Provide only an enabled boolean to change Cloudflare access.")
			return
		}
		if err := controller.SetEnabled(r.Context(), *input.Enabled); err != nil {
			var failure *cloudflareControlError
			if errors.As(err, &failure) {
				remoteWriteError(w, failure.status, failure.code, failure.message)
			} else {
				remoteWriteError(w, http.StatusServiceUnavailable, "cloudflare_stopping", "The Cloudflare access change could not complete. Refresh its status.")
			}
			return
		}
		_ = json.NewEncoder(w).Encode(cloudflareConnection(controller.Status()))
	})
}
