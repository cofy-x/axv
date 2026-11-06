package httpapi

import (
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
)

func registerProjectGitHub(mux *http.ServeMux, service *control.Service, bound func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v1/projects/{id}/github", bound(func(w http.ResponseWriter, r *http.Request) {
		state, err := service.ProjectGitHubState(r.Context(), r.PathValue("id"), r.Header.Get("X-AXV-Host-ID"))
		reply(w, state, err)
	}))
	for _, action := range []string{"git", "review", "associate", "clear", "refresh"} {
		mux.HandleFunc("POST /v1/projects/{id}/github/"+action, bound(func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				Binding      compute.ProjectBinding `json:"binding"`
				Revision     *int64                 `json:"revision"`
				Repository   string                 `json:"repository,omitempty"`
				RepositoryID string                 `json:"repositoryId,omitempty"`
				AccountID    string                 `json:"accountId,omitempty"`
			}
			if err := decodeProjectJSON(w, r, &input, 8<<10); err != nil {
				failure(w, err)
				return
			}
			if input.Revision == nil {
				failure(w, compute.Problem("invalid_request", "The explicit GitHub association revision is required."))
				return
			}
			if input.Binding.HostID != r.Header.Get("X-AXV-Host-ID") {
				failure(w, compute.Problem("binding_changed", "GitHub reads require the same original host in the binding and transport."))
				return
			}
			result, err := service.ProjectGitHub(r.Context(), r.PathValue("id"), action, compute.ProjectGitHubRequest{Binding: input.Binding, Revision: *input.Revision, Repository: input.Repository, RepositoryID: input.RepositoryID, AccountID: input.AccountID})
			reply(w, result, err)
		}))
	}
}
