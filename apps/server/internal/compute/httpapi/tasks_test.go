package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func decodeHTTPTask(t *testing.T, response *httptest.ResponseRecorder, status int) compute.ProjectTaskResult {
	t.Helper()
	var result compute.ProjectTaskResult
	if response.Code != status || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Task.ID == "" || result.Attempt.ID == "" {
		t.Fatalf("expected HTTP %d task result, got %d: %s", status, response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("task response lost private response headers: %v", response.Header())
	}
	return result
}

func TestProjectTaskHTTPDurableAcceptanceRecoveryAndExactCancellation(t *testing.T) {
	f := newProjectHTTPFixture(t, "project-tasks")
	bound := f.registerAndBind(t)
	handler := Handler(f.service.Service)
	base := "/v1/projects/" + bound.Project.ID
	request := compute.SubmitProjectTaskRequest{RequestID: "task/http-original?#%", Binding: bound.Binding, Spec: compute.TaskSpec{Argv: []string{"true"}}}
	response := projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks", request)
	accepted := decodeHTTPTask(t, response, 202)
	if accepted.Attempt.Status != "accepted" || accepted.Attempt.Binding != bound.Binding || len(accepted.Attempts) != 1 || accepted.Task.Spec.Limits.TimeoutSeconds != 900 {
		t.Fatalf("acceptance did not preserve execution identity/defaults: %+v", accepted)
	}
	request.Spec = compute.NormalizeTaskSpec(request.Spec)
	replayed := decodeHTTPTask(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks", request), 202)
	if replayed.Task.ID != accepted.Task.ID || replayed.Attempt.ID != accepted.Attempt.ID {
		t.Fatalf("normalized replay duplicated work: %+v", replayed)
	}
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks/"+accepted.Task.ID+"/retry", compute.RetryProjectTaskRequest{RequestID: "retry-http-original", Binding: bound.Binding}), 409, "task_conflict")
	wrong := bound.Binding
	wrong.InstanceID = "replacement-instance"
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks/"+accepted.Task.ID+"/attempts/"+accepted.Attempt.ID+"/cancel", compute.CancelProjectTaskRequest{Binding: wrong}), 409, "binding_changed")
	cancelled := decodeHTTPTask(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks/"+accepted.Task.ID+"/attempts/"+accepted.Attempt.ID+"/cancel", compute.CancelProjectTaskRequest{Binding: bound.Binding}), 202)
	if !cancelled.Attempt.Cancel || cancelled.Attempt.ID != accepted.Attempt.ID {
		t.Fatalf("cancellation intent changed its attempt: %+v", cancelled)
	}
	f.instance.Store("replacement-instance")
	before := f.healthCalls.Load()
	receipt := decodeHTTPTask(t, projectHTTPCall(t, handler, bound.Binding.HostID, "GET", base+"/task-requests/"+url.PathEscape(request.RequestID), nil), 200)
	if receipt.Attempt.ID != accepted.Attempt.ID || f.healthCalls.Load() != before {
		t.Fatalf("receipt required a current execution environment: %+v", receipt)
	}
	request.Spec.Argv = []string{"another-command"}
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks", request), 409, "idempotency_conflict")
	response = projectHTTPCall(t, handler, bound.Binding.HostID, "GET", base+"/tasks", nil)
	var listed struct {
		Tasks []compute.ProjectTaskResult `json:"tasks"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &listed) != nil || len(listed.Tasks) != 1 || listed.Tasks[0].Task.ID != accepted.Task.ID || f.healthCalls.Load() != before {
		t.Fatalf("offline task list lost durable acceptance: HTTP %d: %s", response.Code, response.Body)
	}
	request.RequestID = "submit-stale-environment"
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks", request), 409, "binding_changed")
}

func TestProjectTaskHTTPRequiresOriginalHostBeforeAnyLookup(t *testing.T) {
	f := newProjectHTTPFixture(t, "project-tasks")
	handler := Handler(f.service.Service)
	before := f.healthCalls.Load()
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/v1/projects/missing/tasks", ""},
		{"GET", "/v1/projects/missing/tasks/missing", ""},
		{"GET", "/v1/projects/missing/task-requests/missing", ""},
		{"GET", "/v1/projects/missing/tasks/missing/attempts/missing/logs?unknown=1", ""},
		{"POST", "/v1/projects/missing/tasks", "invalid JSON"},
		{"POST", "/v1/projects/missing/tasks/missing/retry", "invalid JSON"},
		{"POST", "/v1/projects/missing/tasks/missing/attempts/missing/cancel", "invalid JSON"},
	} {
		for _, host := range []string{"", "replacement-host"} {
			assertProjectHTTPProblem(t, projectHTTPCall(t, handler, host, route.method, route.path, route.body), 409, "binding_changed")
		}
	}
	if f.healthCalls.Load() != before {
		t.Fatal("unbound task request reached the guest")
	}
}

func TestProjectTaskHTTPRejectsMalformedUnicodeAndActionParameters(t *testing.T) {
	f := newProjectHTTPFixture(t, "project-tasks")
	bound := f.registerAndBind(t)
	handler := Handler(f.service.Service)
	base := "/v1/projects/" + bound.Project.ID
	request := compute.SubmitProjectTaskRequest{RequestID: "strict-task-original", Binding: bound.Binding, Spec: compute.TaskSpec{Argv: []string{"true"}}}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		"null",
		string(data) + "{}",
		strings.TrimSuffix(string(data), "}") + `,"unknown":true}`,
		strings.Replace(string(data), `"true"`, `"\ud800"`, 1),
		strings.Replace(string(data), `"true"`, "\"invalid-\xff\"", 1),
		strings.Repeat(" ", (256<<10)+1),
		`{"requestId":"strict-task-original","binding":null,"spec":{"argv":["true"]}}`,
	} {
		assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks", body), 400, "invalid_request")
	}
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks?offset=0", request), 400, "invalid_request")
	for _, query := range []string{"offset=-1", "limit=-1", "limit=0", "limit=65537", "offset=1&offset=2", "limit=1&limit=2", "unknown=1", "offset=", "offset=%zz"} {
		assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "GET", base+"/tasks/missing/attempts/missing/logs?"+query, nil), 400, "invalid_request")
	}
	wrongHost := request
	wrongHost.Binding.HostID = "replacement-host"
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks", wrongHost), 409, "binding_changed")
	contentType := httptest.NewRequest("POST", base+"/tasks", bytes.NewReader(data))
	contentType.Header.Set("X-AXV-Host-ID", bound.Binding.HostID)
	contentType.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, contentType)
	assertProjectHTTPProblem(t, response, 400, "invalid_request")
}

func TestProjectTaskHTTPLargeEscapedArgvAndCachedEmptyLogs(t *testing.T) {
	f := newProjectHTTPFixture(t, "project-tasks")
	bound := f.registerAndBind(t)
	handler := Handler(f.service.Service)
	base := "/v1/projects/" + bound.Project.ID
	request := compute.SubmitProjectTaskRequest{RequestID: "escaped-argv-original", Binding: bound.Binding, Spec: compute.TaskSpec{Argv: []string{"printf", strings.Repeat("\x01", 30000)}}}
	data, err := json.Marshal(request)
	if err != nil || len(data) <= 64<<10 {
		t.Fatalf("fixture does not exercise escaped task envelope: %d %v", len(data), err)
	}
	accepted := decodeHTTPTask(t, projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks", string(data)), 202)
	if accepted.Task.Spec.Argv[1] != request.Spec.Argv[1] {
		t.Fatal("escaped task arguments changed during acceptance")
	}
	path := base + "/tasks/" + accepted.Task.ID + "/attempts/" + accepted.Attempt.ID + "/logs"
	response := projectHTTPCall(t, handler, bound.Binding.HostID, "GET", path, nil)
	var logs struct {
		Data       string `json:"data"`
		NextOffset int64  `json:"nextOffset"`
	}
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"data":""`)) || json.Unmarshal(response.Body.Bytes(), &logs) != nil || logs.NextOffset != 0 {
		t.Fatalf("empty cached logs were not a stable byte string: HTTP %d: %s", response.Code, response.Body)
	}
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "GET", path+"?offset=1", nil), 400, "invalid_request")
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, bound.Binding.HostID, "GET", base+"/task-requests/not-accepted", nil), 404, "not_found")
}

func TestProjectTaskHTTPCapacityAndGuestStateErrorMapping(t *testing.T) {
	f := newProjectHTTPFixture(t, "project-tasks")
	bound := f.registerAndBind(t)
	handler := Handler(f.service.Service)
	base := "/v1/projects/" + bound.Project.ID
	request := compute.SubmitProjectTaskRequest{Binding: bound.Binding, Spec: compute.NormalizeTaskSpec(compute.TaskSpec{Argv: []string{"true"}})}
	request.Spec.Limits.MaxLogBytes = 8 << 20
	for index := range 33 {
		request.RequestID = fmt.Sprintf("capacity-task-%03d", index)
		response := projectHTTPCall(t, handler, bound.Binding.HostID, "POST", base+"/tasks", request)
		if index < 32 {
			decodeHTTPTask(t, response, 202)
		} else {
			assertProjectHTTPProblem(t, response, 429, "capacity_exceeded")
		}
	}
	for code, status := range map[string]int{"task_limit": 429, "task_state_unavailable": 503, "task_not_found": 404, "unsupported": 503, "disk_full": 507} {
		response := httptest.NewRecorder()
		failure(response, compute.Problem(code, "Visible task failure."))
		assertProjectHTTPProblem(t, response, status, code)
	}
}
