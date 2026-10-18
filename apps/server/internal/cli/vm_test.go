package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

const testMachineID = "vm-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func cliHost(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "axv-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			json.NewEncoder(w).Encode(map[string]any{"ready": true, "hostId": "host-cli", "configFingerprint": "config-cli", "protocolVersion": compute.ProtocolVersion})
			return
		}
		handler(w, r)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	home := filepath.Join(dir, "uninitialized-profile")
	t.Setenv("AXV_HOME", home)
	t.Setenv("AXV_COMPUTE_SOCKET", socket)
	t.Setenv("ADDR", "broken desktop address")
	t.Setenv("DATA_DIR", filepath.Join(dir, "unused-data"))
	return home
}

func cliInvoke(args ...string) (string, string, error) {
	var out, diagnostic bytes.Buffer
	err := Execute(context.Background(), args, strings.NewReader(""), &out, &diagnostic, "test")
	return out.String(), diagnostic.String(), err
}

func hostInfo() compute.Info {
	images := []compute.ImageInfo{{ID: "ubuntu", Name: "Ubuntu", Provider: "lima", Arch: "arm64", Status: "ready", MinRootGiB: 10}}
	providers := []compute.Capability{{Provider: "lima", Available: true}}
	return compute.Info{HostID: "host-cli", ConfigFingerprint: "config-cli", ProtocolVersion: compute.ProtocolVersion, Configured: true, HostArch: "arm64", Capacity: compute.Capacity{Resources: compute.Resources{CPUs: 8, MemoryMiB: 8192, DiskGiB: 100}, Machines: 10}, Images: images, Providers: providers, Defaults: compute.DefaultCreationSpec(images, providers, "arm64")}
}

func TestCreateUsesHostDefaultsAndJSONOnlyStdout(t *testing.T) {
	var request compute.CreateRequest
	home := cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/info":
			json.NewEncoder(w).Encode(hostInfo())
		case "/v1/machines":
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			w.WriteHeader(202)
			json.NewEncoder(w).Encode(compute.Result{Machine: compute.Machine{ID: testMachineID, HostID: "host-cli", Spec: request.Spec, Status: "queued"}, Operation: compute.Operation{ID: "op-create", RequestID: request.RequestID, MachineID: testMachineID, Status: "queued"}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	out, diagnostic, err := cliInvoke("vm", "create", "  My VM  ", "--memory", "2048", "--json", "--no-wait")
	if err != nil {
		t.Fatal(err)
	}
	var result compute.Result
	if err = json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("stdout is not one JSON object: %q %v", out, err)
	}
	if request.Spec.Name != "My VM" || request.Spec.Resources.CPUs != 2 || request.Spec.Resources.MemoryMiB != 2048 || request.Spec.Resources.DiskGiB != 10 || request.Spec.ProjectGiB != 4 || !request.Spec.RetainVolume || !request.Start {
		t.Fatalf("wrong defaults: %+v", request)
	}
	if !strings.Contains(diagnostic, request.RequestID) || !strings.Contains(diagnostic, result.Operation.ID) {
		t.Fatalf("missing recovery receipt: %s", diagnostic)
	}
	if _, err = os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("VM CLI initialized desktop/profile storage: %v", err)
	}
}

func TestCreateMultipleImagesRequiresExplicitChoiceWithoutMutation(t *testing.T) {
	var writes atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writes.Add(1)
		}
		info := hostInfo()
		second := info.Images[0]
		second.ID = "second"
		info.Images = append(info.Images, second)
		json.NewEncoder(w).Encode(info)
	})
	out, _, err := cliInvoke("vm", "create", "demo", "--no-wait")
	if err == nil || !strings.Contains(err.Error(), "choose --image") || out != "" || writes.Load() != 0 {
		t.Fatalf("ambiguous creation: %s %v writes=%d", out, err, writes.Load())
	}
}

func TestRejectedCreationDoesNotSuggestWaitingForAnUnrecordedOperation(t *testing.T) {
	var request compute.CreateRequest
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/info":
			json.NewEncoder(w).Encode(hostInfo())
		case "/v1/machines":
			json.NewDecoder(r.Body).Decode(&request)
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(compute.Problem("name_conflict", "Name is already reserved."))
		default:
			t.Errorf("rejected request must not start polling: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	out, diagnostic, err := cliInvoke("vm", "create", "duplicate", "--json")
	if err == nil || !strings.Contains(err.Error(), "was rejected") || strings.Contains(err.Error(), "operation wait") || out != "" {
		t.Fatalf("misleading rejection: stdout=%q error=%v", out, err)
	}
	if request.RequestID == "" || !strings.Contains(diagnostic, request.RequestID) || !strings.Contains(err.Error(), request.RequestID) {
		t.Fatalf("rejection lost its receipt: %s %v", diagnostic, err)
	}
}

func TestLostResponseCanBeRecoveredByPrintedRequestID(t *testing.T) {
	var mu sync.Mutex
	var accepted compute.CreateRequest
	var writes atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/info" {
			json.NewEncoder(w).Encode(hostInfo())
			return
		}
		if r.Method == "POST" {
			writes.Add(1)
			mu.Lock()
			json.NewDecoder(r.Body).Decode(&accepted)
			mu.Unlock()
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		mu.Lock()
		request := accepted
		mu.Unlock()
		if r.URL.Path != "/v1/operations/by-request/"+request.RequestID {
			t.Errorf("wrong recovery target %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(compute.Operation{ID: "op-lost", RequestID: request.RequestID, MachineID: testMachineID, Status: "succeeded"})
	})
	out, receipt, err := cliInvoke("vm", "create", "demo", "--json")
	if err == nil || out != "" || writes.Load() != 1 {
		t.Fatalf("uncertain mutation retried: %d %s %v", writes.Load(), out, err)
	}
	mu.Lock()
	request := accepted
	mu.Unlock()
	if request.RequestID == "" || !strings.Contains(receipt, request.RequestID) {
		t.Fatalf("receipt was not recoverable: %s", receipt)
	}
	out, _, err = cliInvoke("vm", "operation", "wait", "--request-id", request.RequestID, "--json")
	if err != nil || !strings.Contains(out, "op-lost") || writes.Load() != 1 {
		t.Fatalf("recovery failed: %s %v", out, err)
	}
}

func TestOperation404WaitsAndTimeoutLeavesUnknown(t *testing.T) {
	var reads atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(compute.Error{Code: "not_found", Message: "not observed"})
	})
	out, _, err := cliInvoke("vm", "operation", "wait", "--request-id", "request-pending", "--timeout", "40ms")
	if err == nil || !strings.Contains(err.Error(), "not cancelled") || out != "" || reads.Load() != 1 {
		t.Fatalf("404 was treated as final absence: %q %v", out, err)
	}
}

func TestFailedBootKeepsMachineAndOperationReceipt(t *testing.T) {
	op := compute.Operation{ID: "op-failed", MachineID: testMachineID, Status: "failed", Error: "guest boot failed"}
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/info":
			json.NewEncoder(w).Encode(hostInfo())
		case "/v1/machines":
			var request compute.CreateRequest
			json.NewDecoder(r.Body).Decode(&request)
			op.RequestID = request.RequestID
			json.NewEncoder(w).Encode(compute.Result{Machine: compute.Machine{ID: testMachineID}, Operation: op})
		case "/v1/operations/op-failed":
			json.NewEncoder(w).Encode(op)
		default:
			json.NewEncoder(w).Encode(compute.Machine{ID: testMachineID, Status: "failed"})
		}
	})
	out, receipt, err := cliInvoke("vm", "create", "demo", "--json")
	if err == nil || !strings.Contains(err.Error(), "guest boot failed") {
		t.Fatalf("failed boot claimed success: %v", err)
	}
	var result compute.Result
	if json.Unmarshal([]byte(out), &result) != nil || result.Machine.ID != testMachineID || result.Operation.ID != "op-failed" {
		t.Fatalf("lost machine identity: %s", out)
	}
	if !strings.Contains(receipt, testMachineID) || !strings.Contains(err.Error(), op.RequestID) {
		t.Fatalf("missing recovery identities: %s %v", receipt, err)
	}
}

func TestStoppedSSHAndUnconfirmedDeleteNeverMutate(t *testing.T) {
	var writes atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writes.Add(1)
		}
		json.NewEncoder(w).Encode(map[string]any{"machines": []compute.Machine{{ID: testMachineID, Status: "stopped", Desired: "stopped", Spec: compute.Spec{Name: "demo", RetainVolume: true, VolumeID: "vol-retained"}}}})
	})
	for _, action := range []string{"ssh", "rm"} {
		out, _, err := cliInvoke("vm", action, "demo")
		if err == nil || out != "" || writes.Load() != 0 {
			t.Fatalf("%s changed a VM: %q %v", action, out, err)
		}
	}
}

func TestDeleteConfirmationCancellationAndTimeoutUnblockInput(t *testing.T) {
	for _, cancelNow := range []bool{true, false} {
		reader, writer := io.Pipe()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		if cancelNow {
			cancel()
		}
		line, err := confirmationLine(ctx, reader)
		cancel()
		writer.Close()
		if line != "" || (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) {
			t.Fatalf("confirmation did not detach: line=%q error=%v", line, err)
		}
		if cancelNow && ExitCode(err) != 130 {
			t.Fatalf("Ctrl+C exit code = %d", ExitCode(err))
		}
		if _, err = reader.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("cancelled confirmation input remained open: %v", err)
		}
	}
}

func TestInitAndCompletionsDoNotStartServices(t *testing.T) {
	home := filepath.Join(t.TempDir(), "profile")
	t.Setenv("AXV_HOME", home)
	t.Setenv("ADDR", "invalid")
	for _, args := range [][]string{{"completion", "bash"}, {"vm", "ssh", "--help"}, {"vm", "create", "--help"}} {
		if _, _, err := cliInvoke(args...); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("informational command initialized profile")
		}
	}
	if _, _, err := cliInvoke("init"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("init created service state: %+v", entries)
	}
	before, _ := os.ReadFile(filepath.Join(home, "config.json"))
	if _, _, err := cliInvoke("init"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(home, "config.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("init overwrote profile")
	}
}
