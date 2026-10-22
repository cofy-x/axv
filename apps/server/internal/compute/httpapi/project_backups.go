package httpapi

import (
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
)

func registerProjectBackups(mux *http.ServeMux, service *control.Service, bound func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/project-backups", bound(func(w http.ResponseWriter, r *http.Request) {
		backups, err := service.ProjectBackups(r.Context())
		reply(w, map[string]any{"backups": backups}, err)
	}))
	mux.HandleFunc("POST /v1/projects/{id}/backups", bound(func(w http.ResponseWriter, r *http.Request) {
		var req compute.BackupProjectRequest
		if err := decodeProjectJSON(w, r, &req, 64<<10); err != nil {
			failure(w, err)
			return
		}
		result, err := service.BackupProject(r.Context(), r.PathValue("id"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	}))
	mux.HandleFunc("POST /v1/project-backups/{id}/restore", bound(func(w http.ResponseWriter, r *http.Request) {
		var req compute.RestoreProjectVolumeRequest
		if err := decodeProjectJSON(w, r, &req, 64<<10); err != nil {
			failure(w, err)
			return
		}
		result, err := service.RestoreProjectVolume(r.Context(), r.PathValue("id"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	}))
	mux.HandleFunc("POST /v1/project-backups/{id}/bind", bound(func(w http.ResponseWriter, r *http.Request) {
		var req compute.BindRestoredProjectRequest
		if err := decodeProjectJSON(w, r, &req, 64<<10); err != nil {
			failure(w, err)
			return
		}
		project, err := service.BindRestoredProject(r.Context(), r.PathValue("id"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusCreated, map[string]any{"project": project})
	}))
}
