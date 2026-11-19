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
	key := reviewRequestID("attempt-one")
	binding := compute.ProjectBinding{HostID: "host", ProjectID: "project", ProjectRevision: 1, MachineID: "vm", VolumeID: "volume", VolumeRevision: 1, BootID: "boot", InstanceID: "guest"}
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
	vm := ComputeVMTests{Gateway: g, HostID: "host", ProjectID: "project", MachineID: "vm"}
	poll := func(a VMTestAttempt) VMTestAttempt {
		t.Helper()
		next, err := vm.Poll(t.Context(), target, command, "attempt-one", a)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	a := poll(VMTestAttempt{Attempt: 1})
	if a.Binding == nil || *a.Binding != binding || a.Status != "running" || submissions.Load() != 0 {
		t.Fatalf("binding not captured before submission: %+v", a)
	}
	observe := a
	observe.ObserveOnly = true
	if result := poll(observe); result.Status != "canceled" || submissions.Load() != 0 {
		t.Fatalf("observe-only receipt recovery submitted new work: %+v", result)
	}
	a = poll(a)
	if a.Status != "running" || submissions.Load() != 1 || a.TaskID != "" {
		t.Fatalf("lost acknowledgement classified as final: %+v", a)
	}
	a.ObserveOnly = true
	a = poll(a)
	if a.Status != "failed" || a.AttemptID != "attempt" || a.Log != "FAIL" || submissions.Load() != 1 {
		t.Fatalf("original receipt lost: %+v", a)
	}
	vm.MachineID = "another-vm"
	a = poll(VMTestAttempt{Attempt: 2})
	if a.Status != "error" || a.Binding != nil {
		t.Fatalf("new attempt silently retargeted: %+v", a)
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
			key := reviewRequestID("comparison")
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
			a, err := vm.Poll(t.Context(), target, cmd, "comparison", VMTestAttempt{Binding: &pin, VMID: pin.MachineID, ProjectID: pin.ProjectID})
			if err != nil || a.Status != "failed" || (a.CheckoutSHA == target.HeadSHA) != tc.verified || (a.AssertionExitCode != nil) != tc.exitKnown || (a.AssertionExitCode != nil && *a.AssertionExitCode != 17) || len(a.Log) > reviewLogLimit {
				t.Fatalf("wrong VM evidence: status=%s, checkout=%s, assertion exit=%v, err=%v", a.Status, a.CheckoutSHA, a.AssertionExitCode, err)
			}
		})
	}
}
