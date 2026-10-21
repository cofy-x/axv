//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

func vmProjectAgentFixture(t *testing.T, handler http.HandlerFunc) (string, vmwork.Context) {
	t.Helper()
	_, saved := projectBindingFixture(t)
	saved.Project.RootIdentity = "inode:original"
	cliHost(t, handler)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	value := vmwork.Context{
		Version: vmwork.Version, SessionID: "session-original", RunID: "run-original",
		Project: vmwork.Project(saved), Socket: os.Getenv("AXV_COMPUTE_SOCKET"), ConfigFingerprint: "config-cli", Executable: executable,
	}
	path, _, err := vmwork.Publish(privateAgentDirectory(t), value)
	if err != nil {
		t.Fatal(err)
	}
	return path, value
}

func privateAgentDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func agentInvoke(path string, args ...string) (string, string, error) {
	return cliInvoke(append([]string{"internal-vm-project", path}, args...)...)
}

func TestBoundVMProjectIgnoresMutableProfileAndEnvironment(t *testing.T) {
	var calls atomic.Int32
	var request compute.ProjectFileRequest
	path, bound := vmProjectAgentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/projects/project-cli/files/read" || r.Header.Get("X-AXV-Host-ID") != "host-cli" {
			t.Errorf("bound command resolved another project: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(protocol.ProjectFile{Content: "guest source\n", SHA256: strings.Repeat("a", 64)})
	})
	unusedProfile := filepath.Join(t.TempDir(), "never-initialize")
	t.Setenv("AXV_HOME", unusedProfile)
	t.Setenv("AXV_COMPUTE_SOCKET", filepath.Join(t.TempDir(), "replacement.sock"))
	out, _, err := agentInvoke(path, "read", "src/main.go")
	if err != nil || calls.Load() != 1 || request.Binding != bound.Project.Binding || request.Path != "src/main.go" || !strings.Contains(out, "guest source") {
		t.Fatalf("bound read lost its exact original scope: request=%+v calls=%d output=%q error=%v", request, calls.Load(), out, err)
	}
	if _, err := os.Stat(unusedProfile); !os.IsNotExist(err) {
		t.Fatalf("bound helper initialized mutable profile: %v", err)
	}
}

func TestBoundVMProjectPreservesWriteHashAndStaleBinding(t *testing.T) {
	var calls atomic.Int32
	var request compute.ProjectFileRequest
	path, bound := vmProjectAgentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/project-cli/files/write" {
			t.Errorf("write refreshed a binding: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(compute.Problem("binding_changed", "The original VM was replaced."))
	})
	digest := strings.Repeat("a", 64)
	var output, diagnostic bytes.Buffer
	err := Execute(context.Background(), []string{"internal-vm-project", path, "write", "src/main.go", "--expected-sha256", digest}, strings.NewReader("intended source 世界\n"), &output, &diagnostic, "test")
	if compute.ErrorCode(err) != "binding_changed" || calls.Load() != 1 || request.Binding != bound.Project.Binding || request.ExpectedSHA256 == nil || *request.ExpectedSHA256 != digest || request.Content != "intended source 世界\n" || output.Len() != 0 {
		t.Fatalf("stale write changed scope/hash or retried: %+v calls=%d output=%q error=%v", request, calls.Load(), output.String(), err)
	}
}

func TestBoundVMProjectRejectsOverridesAndUnrelatedCommandsBeforeIO(t *testing.T) {
	var calls atomic.Int32
	path, _ := vmProjectAgentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Errorf("invalid bound command reached the host: %s", r.URL.Path)
		http.NotFound(w, r)
	})
	for _, args := range [][]string{
		{"binding"}, {"register"}, {"list"}, {"vm", "start", "another-vm"},
		{"read", "file.txt", "--binding-file", path},
		{"read", "file.txt", "--binding-file", ""},
		{"read", "project-other", "file.txt"},
		{"write", "file.txt"},
		{"task", "list", "--host-id", "host-other"},
		{"task", "inspect", "task-original", "--binding-file", path},
		{"task", "submit", "--host-id", "", "--", "true"},
		{"task", "cancel", "task-original"},
	} {
		out, _, err := agentInvoke(path, args...)
		if err == nil || calls.Load() != 0 || out != "" {
			t.Fatalf("unsafe bound command accepted: args=%v calls=%d output=%q error=%v", args, calls.Load(), out, err)
		}
	}
}

func TestBoundVMProjectRejectsReplacementHostConfiguration(t *testing.T) {
	var calls atomic.Int32
	_, bound := vmProjectAgentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	})
	bound.ConfigFingerprint = "config-original"
	path, _, err := vmwork.Publish(privateAgentDirectory(t), bound)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := agentInvoke(path, "task", "list")
	if err == nil || !strings.Contains(err.Error(), "identity or configuration changed") || out != "" || calls.Load() != 0 {
		t.Fatalf("bound command adopted changed host configuration: calls=%d output=%q error=%v", calls.Load(), out, err)
	}
}

func TestBoundVMProjectUsesOriginalExecutable(t *testing.T) {
	_, bound := vmProjectAgentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("replacement executable reached host: %s", r.URL.Path)
		http.NotFound(w, r)
	})
	bound.Executable = filepath.Join(t.TempDir(), "other-axv")
	if err := os.WriteFile(bound.Executable, []byte("other executable"), 0700); err != nil {
		t.Fatal(err)
	}
	path, _, err := vmwork.Publish(privateAgentDirectory(t), bound)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := agentInvoke(path, "task", "list")
	if err == nil || !strings.Contains(err.Error(), "original immutable AXV executable") || out != "" {
		t.Fatalf("bound command accepted another executable: output=%q error=%v", out, err)
	}
}

func TestBoundVMProjectRecoversLostTaskResponseWithoutRebinding(t *testing.T) {
	var submissions, receipts atomic.Int32
	var submitted compute.SubmitProjectTaskRequest
	var submittedMu sync.Mutex
	path, bound := vmProjectAgentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/projects/project-cli/tasks":
			submissions.Add(1)
			submittedMu.Lock()
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Error(err)
			}
			submittedMu.Unlock()
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
		case "GET /v1/projects/project-cli/task-requests/request-original":
			receipts.Add(1)
			submittedMu.Lock()
			result := taskResultFixture(submitted.Binding)
			submittedMu.Unlock()
			result.Attempt.Status = "interrupted"
			json.NewEncoder(w).Encode(result)
		default:
			t.Errorf("task recovery captured another binding or replayed work: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	argv := []string{"sh", "-ec", "printf '%s' 'guest test'"}
	args := append([]string{"task", "submit", "--request-id", "request-original", "--"}, argv...)
	out, receipt, err := agentInvoke(path, args...)
	submittedMu.Lock()
	accepted := submitted
	submittedMu.Unlock()
	if err == nil || submissions.Load() != 1 || out != "" || accepted.Binding != bound.Project.Binding || !reflect.DeepEqual(accepted.Spec.Argv, argv) || !strings.Contains(receipt, "Request: request-original") || !strings.Contains(err.Error(), "same bound command with task receipt request-original") {
		t.Fatalf("uncertain bound task changed original execution: request=%+v submissions=%d receipt=%q output=%q error=%v", accepted, submissions.Load(), receipt, out, err)
	}
	out, _, err = agentInvoke(path, "task", "receipt", "request-original")
	if err != nil || receipts.Load() != 1 || submissions.Load() != 1 || !strings.Contains(out, "interrupted") || !strings.Contains(out, "attempt-original") {
		t.Fatalf("receipt could not recover the original task after environment loss: output=%q submissions=%d receipts=%d error=%v", out, submissions.Load(), receipts.Load(), err)
	}
}

func TestBoundVMProjectCancellationKeepsHistoricalAttempt(t *testing.T) {
	var original compute.ProjectBinding
	var cancels atomic.Int32
	path, bound := vmProjectAgentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/projects/project-cli/tasks/task-original":
			history := taskResultFixture(original)
			newAttempt := history.Attempt
			newAttempt.ID, newAttempt.Number = "attempt-new", 2
			newAttempt.Binding.BootID, newAttempt.Binding.InstanceID = "boot-new", "instance-new"
			history.Task.CurrentAttemptID, history.Attempt = newAttempt.ID, newAttempt
			history.Attempts = append(history.Attempts, newAttempt)
			json.NewEncoder(w).Encode(history)
		case "POST /v1/projects/project-cli/tasks/task-original/attempts/attempt-original/cancel":
			cancels.Add(1)
			var request compute.CancelProjectTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Binding != original {
				t.Errorf("cancellation replaced historical binding: %+v %v", request, err)
			}
			json.NewEncoder(w).Encode(taskResultFixture(original))
		default:
			t.Errorf("cancellation targeted another attempt: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	original = bound.Project.Binding
	out, receipt, err := agentInvoke(path, "task", "cancel", "task-original", "attempt-original")
	if err != nil || cancels.Load() != 1 || !strings.Contains(out, "attempt-original") || !strings.Contains(receipt, "Attempt: attempt-original") {
		t.Fatalf("exact bound cancellation failed: calls=%d output=%q receipt=%q error=%v", cancels.Load(), out, receipt, err)
	}
	out, _, err = agentInvoke(path, "task", "cancel", "task-original", "missing-attempt")
	if compute.ErrorCode(err) != "not_found" || cancels.Load() != 1 || out != "" {
		t.Fatalf("missing attempt substituted the latest attempt: calls=%d output=%q error=%v", cancels.Load(), out, err)
	}
}

func TestBoundVMProjectHelpDescribesImplicitProjectWithoutOverrides(t *testing.T) {
	path, _ := vmProjectAgentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("help reached host: %s", r.URL.Path)
		http.NotFound(w, r)
	})
	for _, args := range [][]string{{"--help"}, {"read", "--help"}, {"task", "--help"}, {"task", "submit", "--help"}} {
		out, _, err := agentInvoke(path, args...)
		if err != nil || out == "" || strings.Contains(out, "<project-id>") || strings.Contains(out, "--binding-file") || strings.Contains(out, "--host-id") {
			t.Fatalf("bound help exposed generic project controls: args=%v output=%q error=%v", args, out, err)
		}
	}
}
