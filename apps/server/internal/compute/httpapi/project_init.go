package httpapi

import (
	"mime"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
)

func registerProjectInitialization(mux *http.ServeMux, service *control.Service) {
	bound := func(handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if r.Header.Get("X-AXV-Host-ID") != service.Runtime().HostID {
				failure(w, compute.Problem("binding_changed", "The original AXV host identity is required."))
				return
			}
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if r.URL.RawQuery != "" || err != nil || media != "application/json" {
				failure(w, compute.Problem("invalid_request", "Project initialization requires JSON and no query parameters."))
				return
			}
			handler(w, r)
		}
	}
	mux.HandleFunc("POST /v1/projects/{id}/initialize", bound(func(w http.ResponseWriter, r *http.Request) {
		var req compute.InitializeProjectRequest
		if err := decodeProjectJSON(w, r, &req, 64<<10); err != nil {
			failure(w, err)
			return
		}
		if req.Binding.HostID != r.Header.Get("X-AXV-Host-ID") {
			failure(w, compute.Problem("binding_changed", "The initialization and transport must retain the same host."))
			return
		}
		result, err := service.InitializeProject(r.Context(), r.PathValue("id"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	}))
	mux.HandleFunc("POST /v1/projects/{id}/initializations/{requestId}/finish", bound(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Binding compute.ProjectBinding `json:"binding"`
		}
		if err := decodeProjectJSON(w, r, &req, 64<<10); err != nil {
			failure(w, err)
			return
		}
		if req.Binding.HostID != r.Header.Get("X-AXV-Host-ID") {
			failure(w, compute.Problem("binding_changed", "The initialization and transport must retain the same host."))
			return
		}
		project, err := service.FinishProjectInitialization(r.Context(), r.PathValue("id"), r.PathValue("requestId"), req.Binding)
		reply(w, map[string]any{"project": project}, err)
	}))
}
