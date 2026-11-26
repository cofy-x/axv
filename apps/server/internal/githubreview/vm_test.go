package githubreview

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

// A lost acknowledgement must resolve to exactly one submit and one original
// attempt, even if someone retried the Compute Task from another AXV window.
func TestVMTestRecoversReceiptAndUsesOriginalAttemptLog(t *testing.T) {
	dir, err := os.MkdirTemp("", "rv-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Repository: "octo/example", Number: 9, BaseSHA: strings.Repeat("b", 40), HeadSHA: strings.Repeat("a", 40)}
	command := []string{"go", "test", "./example"}
	binding := compute.ProjectBinding{HostID: "host", ProjectID: "project", ProjectRevision: 1, MachineID: "vm", VolumeID: "volume", VolumeRevision: 1, BootID: "boot", InstanceID: "guest"}
	key := boundReviewRequestID("attempt-one", binding)
	var submissions atomic.Int32
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runtime":
			json.NewEncoder(w).Encode(compute.Runtime{HostID: "host", ConfigFingerprint: "config", Ready: true, ProtocolVersion: compute.ProtocolVersion})
		case "/v1/projects/project/binding":
			json.NewEncoder(w).Encode(compute.ProjectBindingResult{Binding: binding})
		case "/v1/projects/project/task-requests/" + key:
			if submissions.Load() == 0 {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(compute.Problem("not_found", "Task request receipt not found."))
				return
			}
			exitCode := 10
			result := compute.ProjectTaskResult{
				Task:    compute.ProjectTask{ID: "task", RequestID: key, HostID: "host", ProjectID: "project", Spec: reviewTaskSpec(target, command), CurrentAttemptID: "another-attempt"},
				Attempt: compute.TaskAttempt{ID: "attempt", RequestID: key, TaskID: "task", Binding: binding, Status: "failed", Reason: "exit status 10", ExitCode: &exitCode, GuestFinal: true, LogBytes: 4, CachedBytes: 4},
			}
			json.NewEncoder(w).Encode(result)
		case "/v1/projects/project/tasks":
			if r.Method != "POST" || submissions.Add(1) != 1 {
				t.Error("duplicate submit or wrong method")
			}
			var request compute.SubmitProjectTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.RequestID != key || request.Binding != binding {
				t.Error("wrong task binding", err)
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/v1/projects/project/tasks/task/attempts/attempt/logs":
			json.NewEncoder(w).Encode(compute.ProjectTaskLog{TaskID: "task", AttemptID: "attempt", TaskLog: protocol.TaskLog{Data: []byte("FAIL"), NextOffset: 4, LogBytes: 4}})
		default:
			t.Errorf("unexpected VM request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	g := gateway.NewGateway("", gateway.GatewayOptions{})
	defer g.Close()
	g.Bind(socket, "host", "config")
	named := ReviewVMTarget{Name: "Go checks", HostID: "host", ProjectID: "project", MachineID: "vm"}
	vm := ComputeVMTests{Gateway: g, HostID: "host", ProjectID: "default-project", MachineID: "default-vm",
		RepositoryTargets: map[string]ReviewVMTarget{"octo/example": named}}
	service := Service{vm: vm}
	poll := func(a VMTestAttempt) VMTestAttempt {
		t.Helper()
		next, err := service.vmForRepository(target.Repository).Poll(t.Context(), target, command, "attempt-one", a)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	a := poll(VMTestAttempt{Attempt: 1})
	if a.Binding == nil || *a.Binding != binding || a.RequestID != key || a.Status != "running" || submissions.Load() != 0 {
		t.Fatalf("binding not captured before submission: %+v", a)
	}
	observe := a
	observe.ObserveOnly = true
	if result := poll(observe); result.Status != "canceled" || submissions.Load() != 0 {
		t.Fatalf("observe-only receipt recovery submitted new work: %+v", result)
	}
	other := named
	other.MachineID = "another-vm"
	vm.RepositoryTargets["octo/example"] = other
	if result := poll(a); result.Status != "error" || result.Binding == nil || *result.Binding != binding || submissions.Load() != 0 {
		t.Fatalf("bound attempt submitted after environment changed: %+v", result)
	}
	vm.RepositoryTargets["octo/example"] = named
	a = poll(a)
	if a.Status != "running" || submissions.Load() != 1 || a.TaskID != "" {
		t.Fatalf("lost acknowledgement classified as final: %+v", a)
	}
	vm.RepositoryTargets["octo/example"] = other
	a.ObserveOnly = true
	a = poll(a)
	if a.Status != "failed" || a.AttemptID != "attempt" || a.Log != "FAIL" || submissions.Load() != 1 {
		t.Fatalf("original receipt lost: %+v", a)
	}
	a = poll(VMTestAttempt{Attempt: 2})
	if a.Status != "error" || a.Binding != nil {
		t.Fatalf("new attempt silently retargeted: %+v", a)
	}
}

func TestVMTestRequestUsesDurableScopedIdentity(t *testing.T) {
	dir, err := os.MkdirTemp("", "rv-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	const key = "same-candidate-and-side"
	target := Target{Repository: "octo/library", Number: 27, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	command := []string{"go", "test", "./example"}
	original := workerBinding()
	pin := original
	pin.ProjectID = "other-review-project"
	scoped := boundReviewRequestID(key, pin)
	reboot := pin
	reboot.BootID = "another-boot"
	if scoped == boundReviewRequestID(key, original) || scoped == boundReviewRequestID(key, reboot) {
		t.Fatal("request identity must include the selected project and boot")
	}
	var submissions atomic.Int32
	var conflictScoped atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runtime":
			json.NewEncoder(w).Encode(compute.Runtime{HostID: pin.HostID, ConfigFingerprint: "config", Ready: true, ProtocolVersion: compute.ProtocolVersion})
		case "/v1/projects/" + pin.ProjectID + "/task-requests/" + scoped:
			if conflictScoped.Load() {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(compute.Problem("idempotency_conflict", "Stored scoped request conflicts."))
				return
			}
			if submissions.Load() == 0 {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(compute.Problem("not_found", "Task request receipt not found."))
				return
			}
			exit := 0
			json.NewEncoder(w).Encode(compute.ProjectTaskResult{
				Task:    compute.ProjectTask{ID: "task", RequestID: scoped, HostID: pin.HostID, ProjectID: pin.ProjectID, Spec: reviewTaskSpec(target, command)},
				Attempt: compute.TaskAttempt{ID: "attempt", RequestID: scoped, TaskID: "task", Binding: pin, Status: "succeeded", ExitCode: &exit, GuestFinal: true},
			})
		case "/v1/projects/" + pin.ProjectID + "/tasks":
			var req compute.SubmitProjectTaskRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			if r.Method != http.MethodPost || submissions.Add(1) != 1 || err != nil || req.RequestID != scoped || req.Binding != pin {
				t.Errorf("incorrect retry binding or request: %+v, %v", req, err)
			}
			// Acceptance happened before the client lost its acknowledgement.
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected Compute request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	g := gateway.NewGateway("", gateway.GatewayOptions{})
	defer g.Close()
	g.Bind(socket, pin.HostID, "config")
	vm := ComputeVMTests{Gateway: g, HostID: pin.HostID, MachineID: pin.MachineID, ProjectID: pin.ProjectID}
	poll := func(a VMTestAttempt) VMTestAttempt {
		t.Helper()
		next, err := vm.Poll(t.Context(), target, command, key, a)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	if a := poll(VMTestAttempt{Binding: &pin, Status: "running"}); a.Status != "error" || submissions.Load() != 0 {
		t.Fatalf("bound attempt without request identity should fail: %+v", a)
	}
	var a VMTestAttempt
	bindVMTestAttempt(&a, pin, key)
	a.Status = "running"
	wire, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var resumed VMTestAttempt
	if err := json.Unmarshal(wire, &resumed); err != nil {
		t.Fatal(err)
	}
	a = poll(resumed)
	if a.Status != "running" || a.RequestID != scoped || a.TaskID != "" || submissions.Load() != 1 {
		t.Fatalf("lost response changed scoped attempt: %+v", a)
	}
	a.ObserveOnly = true
	a = poll(a)
	if a.Status != "passed" || a.RequestID != scoped || a.TaskID != "task" || a.AttemptID != "attempt" || submissions.Load() != 1 {
		t.Fatalf("original scoped receipt did not recover: %+v", a)
	}
	conflictScoped.Store(true)
	if a = poll(a); a.Status != "error" || a.RequestID != scoped || submissions.Load() != 1 {
		t.Fatalf("conflict retried or discarded pinned identity: %+v", a)
	}
}

func TestVMTestCheckoutUsesOriginalLogPrefixAndPreservesUnknownAssertionExit(t *testing.T) {
	target := Target{Repository: "owner/library", Number: 12, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	header := "Preparing " + target.Repository + " at " + target.HeadSHA + "\nTesting commit " + target.HeadSHA + "\n"
	pin := workerBinding()
	const footer = "Test command exited with status 17\n"
	for _, tc := range []struct {
		name                         string
		output                       string
		verified, limited, exitKnown bool
	}{
		{"real checkout", "sudo: unable to resolve host guest\n" + header + "result\n" + footer, true, false, true},
		{"assertion imitated exit", header + footer + footer, true, false, false},
		{"unverified header", "Preparing " + target.Repository + " at " + target.HeadSHA + "\nTesting commit " + target.BaseSHA + "\n" + header + footer, false, false, true},
		{"large original log", header + strings.Repeat("x", reviewLogLimit+10) + "\n" + footer, true, false, false},
		{"truncated source", header + "result\n" + footer, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// t.TempDir's full test name can exceed the host Unix socket length.
			dir, err := os.MkdirTemp("", "cc-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			sock := filepath.Join(dir, "s")
			l, err := net.Listen("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			cmd := []string{"python3", "-c", "import sys\nsys.exit(17)"}
			key := boundReviewRequestID("comparison", pin)
			count := int64(len(tc.output))
			code := 10
			api := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var response any
				switch r.URL.Path {
				case "/v1/runtime":
					response = compute.Runtime{HostID: pin.HostID, ConfigFingerprint: "config", Ready: true, ProtocolVersion: compute.ProtocolVersion}
				case "/v1/projects/project/task-requests/" + key:
					response = compute.ProjectTaskResult{
						Task:    compute.ProjectTask{ID: "task", RequestID: key, HostID: pin.HostID, ProjectID: pin.ProjectID, Spec: reviewTaskSpec(target, cmd)},
						Attempt: compute.TaskAttempt{ID: "try", RequestID: key, TaskID: "task", Binding: pin, Status: "failed", Reason: "exit status 10", ExitCode: &code, GuestFinal: true, CachedBytes: count, LogBytes: count, LogLimited: tc.limited},
					}
				case "/v1/projects/project/tasks/task/attempts/try/logs":
					offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
					if err != nil {
						t.Error(err)
						return
					}
					limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
					if err != nil {
						t.Error(err)
						return
					}
					end := min(len(tc.output), offset+limit)
					response = compute.ProjectTaskLog{TaskID: "task", AttemptID: "try", TaskLog: protocol.TaskLog{Offset: int64(offset), Data: []byte(tc.output[offset:end]), NextOffset: int64(end), LogBytes: count, LogLimited: tc.limited}}
				default:
					http.NotFound(w, r)
					return
				}
				json.NewEncoder(w).Encode(response)
			})}
			go api.Serve(l)
			defer api.Close()
			g := gateway.NewGateway("", gateway.GatewayOptions{})
			defer g.Close()
			g.Bind(sock, pin.HostID, "config")
			vm := ComputeVMTests{Gateway: g, HostID: pin.HostID, ProjectID: pin.ProjectID, MachineID: pin.MachineID}
			a, err := vm.Poll(t.Context(), target, cmd, "comparison", VMTestAttempt{Binding: &pin, VMID: pin.MachineID, ProjectID: pin.ProjectID, RequestID: key})
			if err != nil || a.Status != "failed" || a.RequestID != key || (a.CheckoutSHA == target.HeadSHA) != tc.verified || (a.AssertionExitCode != nil) != tc.exitKnown || (a.AssertionExitCode != nil && *a.AssertionExitCode != 17) || len(a.Log) > reviewLogLimit {
				t.Fatalf("wrong VM evidence: status=%s, checkout=%s, assertion exit=%v, err=%v", a.Status, a.CheckoutSHA, a.AssertionExitCode, err)
			}
		})
	}
}
