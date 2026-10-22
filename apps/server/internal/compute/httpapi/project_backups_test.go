package httpapi

import (
	"testing"
)

func TestProjectBackupRoutesRequireOriginalHostAndStrictBodies(t *testing.T) {
	f := newProjectHTTPFixture(t)
	b := f.registerAndBind(t)
	handler := Handler(f.service.Service)
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/project-backups"},
		{"POST", "/v1/projects/" + b.Project.ID + "/backups"},
		{"POST", "/v1/project-backups/backup/restore"},
		{"POST", "/v1/project-backups/backup/bind"},
	} {
		response := projectHTTPCall(t, handler, "another-host", route.method, route.path, "{}")
		assertProjectHTTPProblem(t, response, 409, "binding_changed")
		if route.method == "POST" {
			for _, body := range []string{`{"unknown":true}`, `{"name":"\ud800"}`, `{} {}`} {
				response := projectHTTPCall(t, handler, b.Project.HostID, route.method, route.path, body)
				assertProjectHTTPProblem(t, response, 400, "invalid_request")
			}
		}
	}
}
