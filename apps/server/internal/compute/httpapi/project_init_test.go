package httpapi

import (
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestProjectInitializationHTTPPreservesAcceptanceAndRecovery(t *testing.T) {
	f := newProjectHTTPFixture(t, "project-tasks", "project-initialization")
	bound := f.registerAndBind(t)
	handler := Handler(f.service.Service)
	base := "/v1/projects/" + bound.Project.ID
	req := compute.InitializeProjectRequest{RequestID: "initialize-http-original", Binding: bound.Binding, Directory: "new-project"}
	accepted := decodeHTTPTask(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/initialize", req), 202)
	replayed := decodeHTTPTask(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/initialize", req), 202)
	if replayed.Attempt.ID != accepted.Attempt.ID {
		t.Fatal("HTTP replay duplicated initialization")
	}
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/initializations/"+req.RequestID+"/finish", map[string]any{"binding": bound.Binding}), 409, "task_conflict")
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, "other-host", "POST", base+"/initialize", req), 409, "binding_changed")
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/initialize?unknown=1", req), 400, "invalid_request")
	req.Repository = "https://secret@example.com/repository.git"
	response := projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/initialize", req)
	assertProjectHTTPProblem(t, response, 400, "invalid_request")
}
