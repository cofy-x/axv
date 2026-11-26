package server

import (
	"encoding/json"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/doctor"
	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

func doctorHandler(instanceID, home, version string) http.Handler {
	if version == "" {
		version = "dev"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !httpaccess.LocalRequest(r, false) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":  "doctor_local_only",
				"error": "Inspect host diagnostics on the computer running AXV.",
			})
			return
		}
		report := doctor.Run(r.Context(), home, version)
		_ = json.NewEncoder(w).Encode(struct {
			InstanceID string `json:"instanceId,omitempty"`
			doctor.Report
		}{InstanceID: instanceID, Report: report})
	})
}
