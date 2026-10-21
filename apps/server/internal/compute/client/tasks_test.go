package client

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func TestProjectTaskClientPreservesReceiptAttemptAndBinaryLogs(t *testing.T) {
	binding := compute.ProjectBinding{HostID: "host", ProjectID: "project", MachineID: "machine", VolumeID: "volume", ProjectRevision: 1, VolumeRevision: 2, BootID: "boot", InstanceID: "instance"}
	submission := compute.SubmitProjectTaskRequest{RequestID: "request/original?#%", Binding: binding, Spec: compute.NormalizeTaskSpec(compute.TaskSpec{Argv: []string{"printf", "hello 世界"}, Directory: "source tree"})}
	var paths []string
	logData := []byte{0xff, 0, '\n', 0xe4, 0xb8, 0x96}
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			runtime(w, "host")
			return
		}
		if r.Header.Get("X-AXV-Host-ID") != "host" {
			t.Error("task client did not preserve the original host")
		}
		paths = append(paths, r.Method+" "+r.URL.EscapedPath())
		if r.Method == http.MethodPost {
			switch r.URL.Path {
			case "/v1/projects/project/tasks":
				var request compute.SubmitProjectTaskRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !reflect.DeepEqual(request, submission) {
					t.Errorf("submission changed before transport: %+v %v", request, err)
				}
			case "/v1/projects/project/tasks/task/retry":
				var request compute.RetryProjectTaskRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Binding != binding || request.RequestID != "retry-original" {
					t.Errorf("retry changed original binding: %+v %v", request, err)
				}
			case "/v1/projects/project/tasks/task/attempts/attempt/cancel":
				var request compute.CancelProjectTaskRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Binding != binding {
					t.Errorf("cancellation changed original attempt scope: %+v %v", request, err)
				}
			}
		}
		if r.URL.Path == "/v1/projects/project/tasks/task/attempts/attempt/logs" {
			if r.URL.Query().Get("offset") != "17" || r.URL.Query().Get("limit") != "32" {
				t.Errorf("log request changed offsets: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(compute.ProjectTaskLog{TaskID: "task", AttemptID: "attempt", TaskLog: protocol.TaskLog{Offset: 17, Data: logData, NextOffset: 23}})
			return
		}
		result := compute.ProjectTaskResult{Task: compute.ProjectTask{ID: "task", HostID: "host", ProjectID: "project"}, Attempt: compute.TaskAttempt{ID: "attempt", Binding: binding}}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/projects/project/tasks" {
			json.NewEncoder(w).Encode(map[string]any{"tasks": []compute.ProjectTaskResult{result}})
		} else {
			json.NewEncoder(w).Encode(result)
		}
	}))
	c, err := Connect(t.Context(), socket, Binding{HostID: "host"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.SubmitProjectTask(t.Context(), "project", submission); err != nil {
		t.Fatal(err)
	}
	if tasks, err := c.ProjectTasks(t.Context(), "project"); err != nil || len(tasks) != 1 {
		t.Fatalf("task list: %+v %v", tasks, err)
	}
	if _, err := c.ProjectTask(t.Context(), "project", "task"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ProjectTaskByRequest(t.Context(), "project", submission.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RetryProjectTask(t.Context(), "project", "task", compute.RetryProjectTaskRequest{RequestID: "retry-original", Binding: binding}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CancelProjectTaskAttempt(t.Context(), "project", "task", "attempt", compute.CancelProjectTaskRequest{Binding: binding}); err != nil {
		t.Fatal(err)
	}
	logs, err := c.ProjectTaskLogs(t.Context(), "project", "task", "attempt", 17, 32)
	if err != nil || !bytes.Equal(logs.Data, logData) || logs.NextOffset != 23 {
		t.Fatalf("logs lost binary bytes/offsets: %+v %v", logs, err)
	}
	want := []string{
		"POST /v1/projects/project/tasks", "GET /v1/projects/project/tasks", "GET /v1/projects/project/tasks/task", "GET /v1/projects/project/task-requests/request%2Foriginal%3F%23%25",
		"POST /v1/projects/project/tasks/task/retry", "POST /v1/projects/project/tasks/task/attempts/attempt/cancel", "GET /v1/projects/project/tasks/task/attempts/attempt/logs",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("task client refreshed or changed resource identity: got %v, want %v", paths, want)
	}
}

func TestProjectTaskClientRejectsLossyUnicodeAndInvalidLogRangesBeforeSending(t *testing.T) {
	var requests atomic.Int32
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			runtime(w, "host")
			return
		}
		requests.Add(1)
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	c, err := Connect(t.Context(), socket, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	invalid := "invalid-\xff"
	for _, request := range []compute.SubmitProjectTaskRequest{
		{RequestID: invalid},
		{Spec: compute.TaskSpec{Argv: []string{invalid}}},
		{Spec: compute.TaskSpec{Directory: invalid}},
		{Binding: compute.ProjectBinding{InstanceID: invalid}},
	} {
		if _, err := c.SubmitProjectTask(t.Context(), "project", request); compute.ErrorCode(err) != "invalid_request" {
			t.Fatalf("lossy task submission reached transport: %+v %v", request, err)
		}
	}
	if _, err := c.ProjectTaskByRequest(t.Context(), "project", invalid); compute.ErrorCode(err) != "invalid_request" {
		t.Fatalf("lossy receipt reached transport: %v", err)
	}
	if _, err := c.RetryProjectTask(t.Context(), "project", "task", compute.RetryProjectTaskRequest{Binding: compute.ProjectBinding{BootID: invalid}}); compute.ErrorCode(err) != "invalid_request" {
		t.Fatalf("lossy retry reached transport: %v", err)
	}
	if _, err := c.CancelProjectTaskAttempt(t.Context(), "project", "task", invalid, compute.CancelProjectTaskRequest{}); compute.ErrorCode(err) != "invalid_request" {
		t.Fatalf("lossy cancellation reached transport: %v", err)
	}
	for _, rangeValue := range []struct {
		offset int64
		limit  int
	}{{-1, 1}, {0, 0}, {0, 65537}} {
		if _, err := c.ProjectTaskLogs(t.Context(), "project", "task", "attempt", rangeValue.offset, rangeValue.limit); compute.ErrorCode(err) != "invalid_request" {
			t.Fatalf("invalid log range reached transport: %+v %v", rangeValue, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid task requests reached host: %d", requests.Load())
	}
}
