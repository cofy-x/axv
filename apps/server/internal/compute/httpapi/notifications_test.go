package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func TestTaskNotificationHTTPReadsExactAttemptWithoutGuestAccess(t *testing.T) {
	f := newProjectHTTPFixture(t, "project-tasks")
	bound := f.registerAndBind(t)
	handler := Handler(f.service.Service)
	base := "/v1/projects/" + bound.Project.ID + "/tasks"
	accepted := decodeHTTPTask(t, projectHTTPCall(t, handler, bound.Binding.HostID, http.MethodPost, base, compute.SubmitProjectTaskRequest{RequestID: "notification-http", Binding: bound.Binding, Spec: compute.TaskSpec{Argv: []string{"private-command"}}}), http.StatusAccepted)
	f.instance.Store("replacement-instance")
	before := f.healthCalls.Load()
	response := projectHTTPCall(t, handler, bound.Binding.HostID, http.MethodGet, "/v1/task-events?after=0", nil)
	var batch notificationevent.Batch
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &batch) != nil || batch.StoreID != bound.Binding.HostID || batch.SourceID != "compute:"+bound.Binding.HostID || len(batch.Events) != 0 {
		t.Fatalf("empty durable feed: HTTP %d %s", response.Code, response.Body)
	}
	path := base + "/" + accepted.Task.ID + "/attempts/" + accepted.Attempt.ID + "/status"
	response = projectHTTPCall(t, handler, bound.Binding.HostID, http.MethodGet, path, nil)
	var resolution notificationevent.Resolution
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &resolution) != nil || !resolution.Available || !resolution.Current || resolution.State != "accepted" || resolution.Target.AttemptID != accepted.Attempt.ID {
		t.Fatalf("exact attempt resolution: HTTP %d %s", response.Code, response.Body)
	}
	for _, private := range []string{"private-command", "requestId", "rootIdentity", "binding"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("resolution leaked task details %q: %s", private, response.Body)
		}
	}
	if f.healthCalls.Load() != before {
		t.Fatal("durable notification reads contacted the replacement guest")
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("notification read lost private headers: %v", response.Header())
	}
	response = projectHTTPCall(t, handler, bound.Binding.HostID, http.MethodGet, base+"/"+accepted.Task.ID+"/attempts/missing/status", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &resolution) != nil || resolution.Available {
		t.Fatalf("missing original attempt did not remain unavailable: HTTP %d %s", response.Code, response.Body)
	}
}

func TestTaskNotificationHTTPRequiresHostAndStrictCursor(t *testing.T) {
	f := newProjectHTTPFixture(t)
	handler := Handler(f.service.Service)
	for _, path := range []string{"/v1/task-events?unknown=1", "/v1/projects/missing/tasks/missing/attempts/missing/status?unknown=1"} {
		for _, host := range []string{"", "replacement-host"} {
			assertProjectHTTPProblem(t, projectHTTPCall(t, handler, host, http.MethodGet, path, nil), http.StatusConflict, "binding_changed")
		}
	}
	for _, query := range []string{"after=-1", "after=", "after=1&after=2", "after=1&other=2", "other=1", "after=18446744073709551615", "after=1.5", "after=%zz"} {
		assertProjectHTTPProblem(t, projectHTTPCall(t, handler, f.service.Store.HostID, http.MethodGet, "/v1/task-events?"+query, nil), http.StatusBadRequest, "invalid_request")
	}
}
