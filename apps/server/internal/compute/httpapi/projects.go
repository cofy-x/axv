package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

func registerProjects(mux *http.ServeMux, service *control.Service) {
	bound := func(handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-AXV-Host-ID") != service.Runtime().HostID {
				failure(w, compute.Problem("binding_changed", "The original AXV host identity is required for VM projects."))
				return
			}
			if r.URL.RawQuery != "" {
				failure(w, compute.Problem("invalid_request", "VM project routes do not accept query parameters."))
				return
			}
			if r.Method == http.MethodPost {
				mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || mediaType != "application/json" {
					failure(w, compute.Problem("invalid_request", "VM project requests require application/json."))
					return
				}
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			handler(w, r)
		}
	}
	mux.HandleFunc("GET /v1/projects", bound(func(w http.ResponseWriter, r *http.Request) {
		projects, err := service.Projects(r.Context())
		reply(w, map[string]any{"projects": projects}, err)
	}))
	mux.HandleFunc("POST /v1/projects", bound(func(w http.ResponseWriter, r *http.Request) {
		var req compute.RegisterProjectRequest
		if err := decodeProjectJSON(w, r, &req, 64<<10); err != nil {
			failure(w, err)
			return
		}
		project, err := service.RegisterProject(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusCreated, map[string]any{"project": project})
	}))
	mux.HandleFunc("GET /v1/projects/{id}", bound(func(w http.ResponseWriter, r *http.Request) {
		project, err := service.Project(r.Context(), r.PathValue("id"))
		reply(w, map[string]any{"project": project}, err)
	}))
	mux.HandleFunc("GET /v1/projects/{id}/binding", bound(func(w http.ResponseWriter, r *http.Request) {
		binding, err := service.ProjectBinding(r.Context(), r.PathValue("id"), r.Header.Get("X-AXV-Host-ID"))
		reply(w, binding, err)
	}))
	for _, action := range []string{"list", "read", "write", "mkdir"} {
		mux.HandleFunc("POST /v1/projects/{id}/files/"+action, bound(func(w http.ResponseWriter, r *http.Request) {
			var req compute.ProjectFileRequest
			if err := decodeProjectFile(w, r, action, &req); err != nil {
				failure(w, err)
				return
			}
			if req.Binding.HostID != r.Header.Get("X-AXV-Host-ID") {
				failure(w, compute.Problem("binding_changed", "The file request and transport must use the same original AXV host."))
				return
			}
			result, err := service.ProjectFiles(r.Context(), r.PathValue("id"), action, req)
			reply(w, result, err)
		}))
	}
}

func decodeProjectFile(w http.ResponseWriter, r *http.Request, action string, result *compute.ProjectFileRequest) error {
	limit := int64(64 << 10)
	allowed := []string{"binding", "path"}
	required := []string{"binding", "path"}
	if action == "write" {
		limit = 8 << 20
		allowed = append(allowed, "content", "expectedSha256")
		required = append(required, "content", "expectedSha256")
	}
	if action == "list" {
		allowed = append(allowed, "cursor", "limit")
	}
	return decodeProjectJSONFields(w, r, result, limit, allowed, required)
}

func decodeProjectJSON(w http.ResponseWriter, r *http.Request, result any, limit int64) error {
	return decodeProjectJSONFields(w, r, result, limit, nil, nil)
}

func decodeProjectJSONFields(w http.ResponseWriter, r *http.Request, result any, limit int64, allowed, required []string) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return compute.Problem("invalid_request", "The project request body exceeds its limit.")
	}
	data = bytes.TrimSpace(data)
	if !jsonunicode.Valid(data) {
		return compute.Problem("invalid_request", "Project requests require valid UTF-8 and complete Unicode surrogate pairs.")
	}
	if len(data) == 0 || data[0] != '{' {
		return compute.Problem("invalid_request", "Expected one JSON object.")
	}
	if allowed != nil {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return compute.Problem("invalid_request", "Invalid project request: %v", err)
		}
		for key := range fields {
			valid := false
			for _, field := range allowed {
				valid = valid || key == field
			}
			if !valid {
				return compute.Problem("invalid_request", "The %s field does not belong to this file action.", key)
			}
		}
		for _, field := range required {
			value, exists := fields[field]
			if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return compute.Problem("invalid_request", "The %s field is required and cannot be null.", field)
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return compute.Problem("invalid_request", "Invalid project-file write: %v", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return compute.Problem("invalid_request", "Expected one JSON object.")
	}
	return nil
}
