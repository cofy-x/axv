package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

type taskReceiptFailure struct{ err error }

func (w taskReceiptFailure) Write([]byte) (int, error) { return 0, w.err }

func TestVMProjectTaskDoesNotSubmitWithoutRecoveryIdentityOutput(t *testing.T) {
	file, _ := projectBindingFixture(t)
	var mutations atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		mutations.Add(1)
		t.Errorf("task mutation preceded its recoverable identity: %s", r.URL.Path)
		http.Error(w, "unexpected mutation", http.StatusInternalServerError)
	})
	failure := errors.New("task receipt output unavailable")
	for _, args := range [][]string{
		{"submit", "project-cli", "--binding-file", file, "--", "true"},
		{"retry", "project-cli", "task-original", "--binding-file", file},
	} {
		var out bytes.Buffer
		err := Execute(context.Background(), append([]string{"vm", "project", "task"}, args...), nil, &out, taskReceiptFailure{failure}, "test")
		if !errors.Is(err, failure) || mutations.Load() != 0 || out.Len() != 0 {
			t.Fatalf("task escaped failed receipt output: args=%v mutations=%d stdout=%q error=%v", args, mutations.Load(), out.String(), err)
		}
	}
}

func taskResultFixture(binding compute.ProjectBinding) compute.ProjectTaskResult {
	attempt := compute.TaskAttempt{ID: "attempt-original", TaskID: "task-original", Number: 1, Binding: binding, Status: "accepted", RequestID: "task-request-original"}
	return compute.ProjectTaskResult{Task: compute.ProjectTask{ID: attempt.TaskID, HostID: binding.HostID, ProjectID: binding.ProjectID, RequestID: attempt.RequestID, CurrentAttemptID: attempt.ID}, Attempt: attempt, Attempts: []compute.TaskAttempt{attempt}}
}

func TestVMProjectTaskSubmitPreservesReviewedBindingAndCommand(t *testing.T) {
	file, saved := projectBindingFixture(t)
	var submitted compute.SubmitProjectTaskRequest
	var calls atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/project-cli/tasks" || r.Header.Get("X-AXV-Host-ID") != saved.Binding.HostID {
			t.Errorf("task submission resolved another environment: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(taskResultFixture(saved.Binding))
	})
	argv := []string{"sh", "-ec", "printf '%s' 'hello 世界'", "--literal-argument"}
	args := []string{"vm", "project", "task", "submit", "project-cli", "--binding-file", file, "--request-id", "task-request-original", "--directory", "source tree", "--timeout", "1h", "--cpus", "2", "--memory", "1024", "--max-processes", "32", "--max-log-bytes", "4096", "--"}
	out, receipt, err := cliInvoke(append(args, argv...)...)
	if err != nil || calls.Load() != 1 || submitted.Binding != saved.Binding || submitted.RequestID != "task-request-original" || submitted.Spec.Directory != "source tree" || !reflect.DeepEqual(submitted.Spec.Argv, argv) {
		t.Fatalf("submission changed its command or binding: request=%+v calls=%d stdout=%q error=%v", submitted, calls.Load(), out, err)
	}
	wantLimits := compute.TaskLimits{TimeoutSeconds: 3600, CPUs: 2, MemoryMiB: 1024, MaxProcesses: 32, MaxLogBytes: 4096}
	if submitted.Spec.Limits != wantLimits || !strings.Contains(receipt, "Request: task-request-original") || !strings.Contains(receipt, "VM: "+testMachineID) || !strings.Contains(out, "attempt-original") {
		t.Fatalf("submission lost limits or recovery identity: request=%+v receipt=%q stdout=%q", submitted, receipt, out)
	}
}

func TestVMProjectTaskQueriesRequireOriginalHostAndRetainReceipt(t *testing.T) {
	_, saved := projectBindingFixture(t)
	var requests []string
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.EscapedPath())
		if r.Method != http.MethodGet || r.Header.Get("X-AXV-Host-ID") != saved.Binding.HostID {
			t.Errorf("query lost original host: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Path == "/v1/projects/project-cli/tasks" {
			json.NewEncoder(w).Encode(map[string]any{"tasks": []compute.ProjectTaskResult{taskResultFixture(saved.Binding)}})
		} else {
			json.NewEncoder(w).Encode(taskResultFixture(saved.Binding))
		}
	})
	for _, args := range [][]string{
		{"list", "project-cli"},
		{"inspect", "project-cli", "task-original"},
		{"receipt", "project-cli", "request/original?#%"},
	} {
		out, _, err := cliInvoke(append(append([]string{"vm", "project", "task"}, args...), "--host-id", "host-cli")...)
		if err != nil || !strings.Contains(out, "task-original") {
			t.Fatalf("durable task query failed: %v stdout=%q error=%v", args, out, err)
		}
	}
	want := []string{"/v1/projects/project-cli/tasks", "/v1/projects/project-cli/tasks/task-original", "/v1/projects/project-cli/task-requests/request%2Foriginal%3F%23%25"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("query refreshed environment or corrupted receipt identity: got %v, want %v", requests, want)
	}
}

func TestVMProjectTaskQueryRejectsReplacementHostBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("task query reached a replacement host: %s", r.URL.Path)
		http.NotFound(w, r)
	})
	out, _, err := cliInvoke("vm", "project", "task", "inspect", "project-cli", "task-original", "--host-id", "host-original")
	if err == nil || !strings.Contains(err.Error(), "identity or configuration changed") || out != "" || requests.Load() != 0 {
		t.Fatalf("query replaced its original host: stdout=%q requests=%d error=%v", out, requests.Load(), err)
	}
}

func TestVMProjectTaskCancelUsesExactHistoricalAttempt(t *testing.T) {
	_, saved := projectBindingFixture(t)
	history := taskResultFixture(saved.Binding)
	newBinding := saved.Binding
	newBinding.MachineID, newBinding.BootID, newBinding.InstanceID = "replacement-vm", "replacement-boot", "replacement-instance"
	newAttempt := history.Attempt
	newAttempt.ID, newAttempt.Number, newAttempt.Binding = "attempt-new", 2, newBinding
	history.Task.CurrentAttemptID, history.Attempt = newAttempt.ID, newAttempt
	history.Attempts = append(history.Attempts, newAttempt)
	var cancels atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/projects/project-cli/tasks/task-original":
			json.NewEncoder(w).Encode(history)
		case "POST /v1/projects/project-cli/tasks/task-original/attempts/attempt-original/cancel":
			cancels.Add(1)
			var request compute.CancelProjectTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Binding != saved.Binding {
				t.Errorf("old cancellation was redirected to a newer attempt: %+v %v", request, err)
			}
			json.NewEncoder(w).Encode(history)
		default:
			t.Errorf("cancellation addressed another target: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	out, receipt, err := cliInvoke("vm", "project", "task", "cancel", "project-cli", "task-original", "attempt-original", "--host-id", "host-cli")
	if err != nil || cancels.Load() != 1 || !strings.Contains(receipt, "Attempt: attempt-original") || !strings.Contains(out, "attempt-new") {
		t.Fatalf("historical cancellation failed: cancels=%d receipt=%q stdout=%q error=%v", cancels.Load(), receipt, out, err)
	}
	out, _, err = cliInvoke("vm", "project", "task", "cancel", "project-cli", "task-original", "missing-attempt", "--host-id", "host-cli")
	if compute.ErrorCode(err) != "not_found" || cancels.Load() != 1 || out != "" {
		t.Fatalf("missing attempt fell back to the current attempt: cancels=%d stdout=%q error=%v", cancels.Load(), out, err)
	}
}

func TestVMProjectTaskRetryAndBindingCancellationNeverRefreshEnvironment(t *testing.T) {
	file, saved := projectBindingFixture(t)
	var requests atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("task mutation refreshed its explicit binding: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/projects/project-cli/tasks/task-original/retry":
			var request compute.RetryProjectTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Binding != saved.Binding || request.RequestID != "retry-original" {
				t.Errorf("retry changed binding/receipt: %+v %v", request, err)
			}
		case "/v1/projects/project-cli/tasks/task-original/attempts/attempt-original/cancel":
			var request compute.CancelProjectTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Binding != saved.Binding {
				t.Errorf("cancel changed original binding: %+v %v", request, err)
			}
		default:
			t.Errorf("unexpected task mutation: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(compute.Problem("binding_changed", "The recorded VM is unavailable."))
	})
	for _, args := range [][]string{
		{"retry", "project-cli", "task-original", "--request-id", "retry-original"},
		{"cancel", "project-cli", "task-original", "attempt-original"},
	} {
		before := requests.Load()
		out, _, err := cliInvoke(append(append([]string{"vm", "project", "task"}, args...), "--binding-file", file)...)
		if compute.ErrorCode(err) != "binding_changed" || out != "" || requests.Load() != before+1 {
			t.Fatalf("task mutation retried or hid stale binding: %v stdout=%q requests=%d error=%v", args, out, requests.Load(), err)
		}
	}
}

func TestVMProjectTaskLostSubmissionResponseIsRecoveredByReceiptOnly(t *testing.T) {
	file, _ := projectBindingFixture(t)
	var submissions atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		submissions.Add(1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	})
	out, receipt, err := cliInvoke("vm", "project", "task", "submit", "project-cli", "--binding-file", file, "--request-id", "task-request-original", "--", "sh", "-c", "echo install")
	if err == nil || submissions.Load() != 1 || out != "" || !strings.Contains(receipt, "Request: task-request-original") || !strings.Contains(err.Error(), "task receipt project-cli task-request-original --host-id host-cli") {
		t.Fatalf("uncertain submission lost recovery identity or retried: stdout=%q receipt=%q submissions=%d error=%v", out, receipt, submissions.Load(), err)
	}
}

func TestVMProjectTaskLogsAreByteExactAndKeepOffsets(t *testing.T) {
	_, saved := projectBindingFixture(t)
	data := []byte{0xff, 0x00, '\n', 0xe4, 0xb8, 0x96, 0xe7, 0x95, 0x8c}
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/projects/project-cli/tasks/task-original/attempts/attempt-original/logs" || r.URL.Query().Get("offset") != "11" || r.URL.Query().Get("limit") != "9" {
			t.Errorf("log query changed original scope/offset: %s %s", r.Method, r.URL)
		}
		json.NewEncoder(w).Encode(compute.ProjectTaskLog{TaskID: "task-original", AttemptID: "attempt-original", Status: "running", TaskLog: protocol.TaskLog{Offset: 11, Data: data, NextOffset: 20, LogBytes: 50}})
	})
	args := []string{"vm", "project", "task", "logs", "project-cli", "task-original", "attempt-original", "--host-id", saved.Binding.HostID, "--offset", "11", "--limit", "9"}
	out, _, err := cliInvoke(args...)
	var result compute.ProjectTaskLog
	if err != nil || json.Unmarshal([]byte(out), &result) != nil || !bytes.Equal(result.Data, data) || result.NextOffset != 20 || result.Offset != 11 {
		t.Fatalf("JSON logs lost bytes/offsets: stdout=%q result=%+v error=%v", out, result, err)
	}
	out, _, err = cliInvoke(append(args, "--raw")...)
	if err != nil || !bytes.Equal([]byte(out), data) {
		t.Fatalf("raw log output changed bytes: got %q, want %q, error=%v", out, data, err)
	}
}

func TestVMProjectTaskValidationPrecedesConnection(t *testing.T) {
	file, _ := projectBindingFixture(t)
	for _, args := range [][]string{
		{"submit", "project-cli", "--", "true"},
		{"submit", "project-cli", "--binding-file", file, "true"},
		{"submit", "project-cli", "--binding-file", file, "--timeout", "1ms", "--", "true"},
		{"submit", "project-cli", "--binding-file", file, "--memory", "1", "--", "true"},
		{"submit", "project-cli", "--binding-file", file, "--directory", "../host", "--", "true"},
		{"submit", "project-cli", "--binding-file", file, "--", "invalid-\xff"},
		{"submit", "project-cli", "--binding-file", file, "--request-id", "short", "--", "true"},
		{"list", "project-cli"},
		{"inspect", "project-cli", "task-original"},
		{"receipt", "project-cli", "original-request"},
		{"retry", "project-cli", "task-original", "--host-id", "host-cli"},
		{"retry", "project-cli", "task-original", "--binding-file", file, "--host-id", "another-host"},
		{"cancel", "project-cli", "task-original", "attempt-original"},
		{"logs", "project-cli", "task-original", "attempt-original", "--host-id", "host-cli", "--offset", "-1"},
		{"logs", "project-cli", "task-original", "attempt-original", "--host-id", "host-cli", "--limit", "65537"},
		{"logs", "project-cli", "task-original", "attempt-original", "--host-id", "host-cli", "--raw", "--json"},
	} {
		out, _, err := cliInvoke(append([]string{"vm", "project", "task"}, args...)...)
		if err == nil || ExitCode(err) != 2 || out != "" {
			t.Fatalf("unsafe task command accepted: args=%v stdout=%q error=%v", args, out, err)
		}
	}
}
