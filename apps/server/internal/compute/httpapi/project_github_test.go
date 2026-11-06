package httpapi

import (
	"net/http"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestProjectGitHubHTTPRequiresOriginalHostAndTypedRevision(t *testing.T) {
	f := newProjectHTTPFixture(t, "project-github")
	b := f.registerAndBind(t)
	handler := Handler(f.service.Service)
	base := "/v1/projects/" + b.Project.ID + "/github"
	for _, action := range []string{"git", "review", "associate", "clear", "refresh"} {
		path := base + "/" + action
		assertProjectHTTPProblem(t, projectHTTPCall(t, handler, "different-host", "POST", path, `{}`), 409, "binding_changed")
		assertProjectHTTPProblem(t, projectHTTPCall(t, handler, b.Binding.HostID, "POST", path, map[string]any{"binding": b.Binding}), 400, "invalid_request")
		assertProjectHTTPProblem(t, projectHTTPCall(t, handler, b.Binding.HostID, "POST", path, map[string]any{"binding": b.Binding, "revision": 0, "command": "arbitrary shell"}), 400, "invalid_request")
	}
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, "different-host", "GET", base, nil), 409, "binding_changed")
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, b.Binding.HostID, "GET", base+"?token=forbidden", nil), 400, "invalid_request")
	response := projectHTTPCall(t, handler, b.Binding.HostID, "GET", base, nil)
	if response.Code != http.StatusOK || response.Body.String() != "{\"revision\":0}\n" {
		t.Fatalf("unconfigured association = %d %s", response.Code, response.Body.String())
	}
	wrong := b.Binding
	wrong.HostID = "another-host"
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, b.Binding.HostID, "POST", base+"/git", compute.ProjectGitHubRequest{Binding: wrong}), 409, "binding_changed")
}
