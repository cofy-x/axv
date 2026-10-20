package httpapi

import (
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
)

func registerTasks(mux *http.ServeMux, s *control.Service) {
	bound := func(handler http.HandlerFunc, logs bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-AXV-Host-ID") != s.Runtime().HostID {
				failure(w, compute.Problem("binding_changed", "The original AXV host identity is required for project tasks."))
				return
			}
			if !logs && r.URL.RawQuery != "" {
				failure(w, compute.Problem("invalid_request", "Task routes accept query parameters only for log offsets."))
				return
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			handler(w, r)
		}
	}
	decode := func(w http.ResponseWriter, r *http.Request, out any, fields ...string) bool {
		kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || kind != "application/json" {
			failure(w, compute.Problem("invalid_request", "Task mutations require application/json."))
			return false
		}
		if err := decodeProjectJSONFields(w, r, out, 256<<10, fields, fields); err != nil {
			failure(w, err)
			return false
		}
		return true
	}
	base := "/v1/projects/{id}"
	mux.HandleFunc("POST "+base+"/tasks", bound(func(w http.ResponseWriter, r *http.Request) {
		var req compute.SubmitProjectTaskRequest
		if !decode(w, r, &req, "requestId", "binding", "spec") {
			return
		}
		if req.Binding.HostID != r.Header.Get("X-AXV-Host-ID") {
			failure(w, compute.Problem("binding_changed", "The task and transport must use the same original host."))
			return
		}
		result, err := s.SubmitProjectTask(r.Context(), r.PathValue("id"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	}, false))
	mux.HandleFunc("GET "+base+"/tasks", bound(func(w http.ResponseWriter, r *http.Request) {
		items, err := s.ProjectTasks(r.Context(), r.PathValue("id"), r.Header.Get("X-AXV-Host-ID"))
		reply(w, map[string]any{"tasks": items}, err)
	}, false))
	mux.HandleFunc("GET "+base+"/task-requests/{request}", bound(func(w http.ResponseWriter, r *http.Request) {
		result, err := s.ProjectTaskByRequest(r.Context(), r.PathValue("id"), r.PathValue("request"), r.Header.Get("X-AXV-Host-ID"))
		reply(w, result, err)
	}, false))
	mux.HandleFunc("GET "+base+"/tasks/{task}", bound(func(w http.ResponseWriter, r *http.Request) {
		result, err := s.ProjectTask(r.Context(), r.PathValue("id"), r.PathValue("task"), r.Header.Get("X-AXV-Host-ID"))
		reply(w, result, err)
	}, false))
	mux.HandleFunc("POST "+base+"/tasks/{task}/retry", bound(func(w http.ResponseWriter, r *http.Request) {
		var req compute.RetryProjectTaskRequest
		if !decode(w, r, &req, "requestId", "binding") {
			return
		}
		if req.Binding.HostID != r.Header.Get("X-AXV-Host-ID") {
			failure(w, compute.Problem("binding_changed", "Retry requires its explicit original transport host."))
			return
		}
		result, err := s.RetryProjectTask(r.Context(), r.PathValue("id"), r.PathValue("task"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	}, false))
	mux.HandleFunc("POST "+base+"/tasks/{task}/attempts/{attempt}/cancel", bound(func(w http.ResponseWriter, r *http.Request) {
		var req compute.CancelProjectTaskRequest
		if !decode(w, r, &req, "binding") {
			return
		}
		if req.Binding.HostID != r.Header.Get("X-AXV-Host-ID") {
			failure(w, compute.Problem("binding_changed", "Cancellation requires the original transport host."))
			return
		}
		result, err := s.CancelProjectTaskAttempt(r.Context(), r.PathValue("id"), r.PathValue("task"), r.PathValue("attempt"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	}, false))
	mux.HandleFunc("GET "+base+"/tasks/{task}/attempts/{attempt}/logs", bound(func(w http.ResponseWriter, r *http.Request) {
		query, err := parseTaskLogQuery(r)
		if err != nil {
			failure(w, err)
			return
		}
		result, err := s.ProjectTaskLogs(r.Context(), r.PathValue("id"), r.PathValue("task"), r.PathValue("attempt"), r.Header.Get("X-AXV-Host-ID"), query.offset, query.limit)
		reply(w, result, err)
	}, true))
}

type taskLogQuery struct {
	offset int64
	limit  int
}

func parseTaskLogQuery(r *http.Request) (taskLogQuery, error) {
	var result taskLogQuery
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return result, compute.Problem("invalid_request", "Invalid task log query.")
	}
	for key, items := range values {
		if (key != "offset" && key != "limit") || len(items) != 1 || items[0] == "" {
			return result, compute.Problem("invalid_request", "Use only one offset and limit query parameter.")
		}
	}
	if value := values.Get("offset"); value != "" {
		result.offset, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return result, compute.Problem("invalid_request", "The log offset must be a nonnegative integer.")
		}
	}
	if value := values.Get("limit"); value != "" {
		result.limit, err = strconv.Atoi(value)
		if err != nil || result.limit < 1 || result.limit > 65536 {
			return result, compute.Problem("invalid_request", "The log limit must be an integer from 1 to 65536.")
		}
	}
	return result, nil
}
