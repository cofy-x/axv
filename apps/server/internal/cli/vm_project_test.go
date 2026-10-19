package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func projectBindingFixture(t *testing.T) (string, compute.ProjectBindingResult) {
	t.Helper()
	result := compute.ProjectBindingResult{
		Project: compute.Project{ID: "project-cli", HostID: "host-cli", Kind: "vm", VolumeID: "volume-cli", Revision: 2},
		Binding: compute.ProjectBinding{HostID: "host-cli", ProjectID: "project-cli", ProjectRevision: 2, VolumeID: "volume-cli", VolumeRevision: 3, MachineID: testMachineID, BootID: "boot-original", InstanceID: "instance-original"},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "binding.json")
	if err = os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	return filename, result
}

func TestVMProjectRegistrationCapturesVolumeRevision(t *testing.T) {
	var request compute.RegisterProjectRequest
	var inventoryRevision atomic.Int64
	inventoryRevision.Store(7)
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AXV-Host-ID") != "host-cli" {
			t.Errorf("missing original host identity")
		}
		switch r.URL.Path {
		case "/v1/machines/" + testMachineID:
			json.NewEncoder(w).Encode(compute.Machine{ID: testMachineID})
		case "/v1/volumes":
			json.NewEncoder(w).Encode(map[string]any{"volumes": []compute.Volume{{ID: "volume-cli", Revision: inventoryRevision.Load()}}})
		case "/v1/projects":
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			json.NewEncoder(w).Encode(map[string]any{"project": compute.Project{ID: "project-cli", VolumeID: request.VolumeID, Root: request.Root}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	out, receipt, err := cliInvoke("vm", "project", "register", "--machine", testMachineID, "--volume", "volume-cli", "--root", "repo", "--request-id", "register-original", "--json")
	if err != nil || request.MachineID != testMachineID || request.VolumeRevision != 7 || request.Root != "repo" || request.RequestID != "register-original" || !strings.Contains(out, "project-cli") || !strings.Contains(receipt, request.RequestID) || !strings.Contains(receipt, "Volume revision: 7") {
		t.Fatalf("registration lost identity: request=%+v stdout=%q receipt=%q err=%v", request, out, receipt, err)
	}
	inventoryRevision.Store(9)
	out, _, err = cliInvoke("vm", "project", "register", "--host-id", "host-cli", "--machine", testMachineID, "--volume", "volume-cli", "--volume-revision", "7", "--root", "repo", "--request-id", "register-original", "--json")
	if err != nil || request.VolumeRevision != 7 || request.MachineID != testMachineID || request.RequestID != "register-original" || !strings.Contains(out, "project-cli") {
		t.Fatalf("replay substituted current inventory revision: request=%+v stdout=%q err=%v", request, out, err)
	}
}

func TestVMProjectRegistrationRecoversLostResponseWithoutCurrentResources(t *testing.T) {
	registration := compute.RegisterProjectRequest{RequestID: "register-original", MachineID: testMachineID, VolumeID: "volume-cli", VolumeRevision: 7, Root: "repo", Name: "Original project"}
	var submissions atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		if submissions.Load() > 0 && r.URL.Path != "/v1/projects" {
			t.Errorf("registration recovery looked up removed VM or volume: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/v1/machines":
			json.NewEncoder(w).Encode(map[string]any{"machines": []compute.Machine{{ID: testMachineID, Spec: compute.Spec{Name: "original-vm"}}}})
		case "/v1/volumes":
			json.NewEncoder(w).Encode(map[string]any{"volumes": []compute.Volume{{ID: "volume-cli", Revision: 7}}})
		case "/v1/projects":
			var request compute.RegisterProjectRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if r.Header.Get("X-AXV-Host-ID") != "host-cli" {
				t.Error("registration recovery changed the original host")
			}
			if request != registration {
				t.Errorf("registration recovery changed original payload: got %+v, want %+v", request, registration)
			}
			if submissions.Add(1) == 1 {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				connection.Close()
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"project": compute.Project{ID: "project-cli", VolumeID: request.VolumeID, Root: request.Root}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	out, receipt, err := cliInvoke("vm", "project", "register", "--machine", "original-vm", "--volume", "volume-cli", "--root", "repo", "--name", "Original project", "--request-id", "register-original", "--json")
	if err == nil || out != "" || submissions.Load() != 1 || !strings.Contains(receipt, "Host: host-cli  Machine: "+testMachineID) || !strings.Contains(receipt, "Volume revision: 7  Request: register-original") {
		t.Fatalf("lost response did not preserve the original receipt: stdout=%q receipt=%q submissions=%d error=%v", out, receipt, submissions.Load(), err)
	}
	out, _, err = cliInvoke("vm", "project", "register", "--host-id", "host-cli", "--machine", testMachineID, "--volume", "volume-cli", "--volume-revision", "7", "--root", "repo", "--name", "Original project", "--request-id", "register-original", "--json")
	if err != nil || submissions.Load() != 2 || !strings.Contains(out, "project-cli") {
		t.Fatalf("registration receipt unavailable after original VM was removed: stdout=%q submissions=%d error=%v", out, submissions.Load(), err)
	}
}

func TestVMProjectRegistrationRecoveryRejectsChangedHostBeforeSubmission(t *testing.T) {
	var requests atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("registration recovery reached a replacement host: %s", r.URL.Path)
		http.NotFound(w, r)
	})
	out, _, err := cliInvoke("vm", "project", "register", "--host-id", "host-original", "--machine", testMachineID, "--volume", "volume-cli", "--volume-revision", "7", "--request-id", "register-original")
	if err == nil || !strings.Contains(err.Error(), "identity or configuration changed") || out != "" || requests.Load() != 0 {
		t.Fatalf("registration recovery did not pin original host: stdout=%q requests=%d error=%v", out, requests.Load(), err)
	}
}

func TestVMProjectFileCommandsRequireExplicitCompleteBindingBeforeConnection(t *testing.T) {
	file, _ := projectBindingFixture(t)
	t.Setenv("AXV_HOME", filepath.Join(t.TempDir(), "absent-profile"))
	for _, args := range [][]string{
		{"vm", "project", "read", "project-cli", "file.txt"},
		{"vm", "project", "read", "other-project", "file.txt", "--binding-file", file},
		{"vm", "project", "write", "project-cli", "file.txt", "--binding-file", file},
		{"vm", "project", "write", "project-cli", "file.txt", "--binding-file", file, "--create", "--expected-sha256", strings.Repeat("a", 64)},
		{"vm", "project", "write", "project-cli", "file.txt", "--binding-file", file, "--expected-sha256", "invalid"},
		{"vm", "project", "register", "--volume", "volume-cli"},
		{"vm", "project", "register", "--machine", testMachineID, "--volume", "volume-cli", "--volume-revision", "0"},
		{"vm", "project", "register", "--machine", testMachineID, "--volume", "volume-cli", "--volume-revision", "7", "--request-id", "original"},
		{"vm", "project", "register", "--host-id", "host-cli", "--machine", testMachineID, "--volume", "volume-cli", "--request-id", "original"},
		{"vm", "project", "register", "--host-id", "host-cli", "--machine", testMachineID, "--volume", "volume-cli", "--volume-revision", "7"},
		{"vm", "project", "register", "--host-id", "host-cli", "--machine", "replacement-name", "--volume", "volume-cli", "--volume-revision", "7", "--request-id", "original"},
		{"vm", "project", "register", "--machine", testMachineID, "--volume", "volume-cli", "--root", "repo-\xff"},
		{"vm", "project", "register", "--machine", testMachineID, "--volume", "volume-cli", "--name", "Project-\xff"},
		{"vm", "project", "read", "project-cli", "file-\xff.txt", "--binding-file", file},
		{"vm", "project", "read", "project-cli", "../outside.txt", "--binding-file", file},
		{"vm", "project", "read", "project-cli", "", "--binding-file", file},
	} {
		out, _, err := cliInvoke(args...)
		if err == nil || ExitCode(err) != 2 || out != "" {
			t.Fatalf("unsafe command accepted: %v stdout=%q error=%v", args, out, err)
		}
	}
}

func TestReadProjectBindingRejectsInvalidUnicodeWithoutReplacement(t *testing.T) {
	_, binding := projectBindingFixture(t)
	data, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"invalid-\xff", `invalid-\ud800`} {
		filename := filepath.Join(t.TempDir(), "invalid-binding.json")
		modified := strings.Replace(string(data), `"name":""`, `"name":"`+value+`"`, 1)
		if modified == string(data) {
			t.Fatal("fixture has no project name")
		}
		if err := os.WriteFile(filename, []byte(modified), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readProjectBinding(filename, binding.Project.ID); err == nil || ExitCode(err) != 2 {
			t.Fatalf("invalid Unicode binding was silently decoded: %q %v", value, err)
		}
	}
}

func TestVMProjectWriteRejectsInvalidUTF8BeforeConnection(t *testing.T) {
	bindingFile, _ := projectBindingFixture(t)
	t.Setenv("AXV_HOME", filepath.Join(t.TempDir(), "absent-profile"))
	var out, diagnostic bytes.Buffer
	err := Execute(t.Context(), []string{"vm", "project", "write", "project-cli", "readme.txt", "--binding-file", bindingFile, "--create"}, strings.NewReader("invalid-\xff"), &out, &diagnostic, "test")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "content must be UTF-8") || out.Len() != 0 {
		t.Fatalf("invalid UTF-8 content was encoded: stdout=%q error=%v", out.String(), err)
	}
}

func TestVMProjectWritePreservesBindingVersionAndUTF8Content(t *testing.T) {
	file, saved := projectBindingFixture(t)
	var writes atomic.Int32
	var request compute.ProjectFileRequest
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/project-cli/files/write" || r.Method != http.MethodPost {
			t.Errorf("file command refreshed or retargeted binding: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		writes.Add(1)
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(map[string]any{"project": saved.Project, "binding": saved.Binding, "path": request.Path, "sha256": strings.Repeat("b", 64), "content": request.Content})
	})
	var out, diagnostic bytes.Buffer
	err := Execute(context.Background(), []string{"vm", "project", "write", "project-cli", "src/main.txt", "--binding-file", file, "--expected-sha256", strings.Repeat("a", 64)}, strings.NewReader("hello 世界\n"), &out, &diagnostic, "test")
	if err != nil || writes.Load() != 1 || request.Binding != saved.Binding || request.ExpectedSHA256 == nil || *request.ExpectedSHA256 != strings.Repeat("a", 64) || request.Content != "hello 世界\n" {
		t.Fatalf("write changed identity/content: %+v writes=%d error=%v", request, writes.Load(), err)
	}
	var response map[string]any
	if err = json.Unmarshal(out.Bytes(), &response); err != nil || response["sha256"] != strings.Repeat("b", 64) {
		t.Fatalf("write result lost version: %s %v", out.String(), err)
	}
}

func TestVMProjectCreateAndStaleBindingAreNeverRetried(t *testing.T) {
	file, original := projectBindingFixture(t)
	var writes atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		var request compute.ProjectFileRequest
		json.NewDecoder(r.Body).Decode(&request)
		if request.Binding != original.Binding || request.ExpectedSHA256 == nil || *request.ExpectedSHA256 != "" {
			t.Errorf("create did not retain original binding/create-only expectation: %+v", request)
		}
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(compute.Problem("binding_changed", "Project volume now belongs to a different VM."))
	})
	out, _, err := cliInvoke("vm", "project", "write", "project-cli", "new.txt", "--binding-file", file, "--create")
	if err == nil || compute.ErrorCode(err) != "binding_changed" || out != "" || writes.Load() != 1 {
		t.Fatalf("stale binding was not rejected once: writes=%d stdout=%q error=%v", writes.Load(), out, err)
	}
}

func TestVMProjectLostMutationResponseHasNoAutomaticRetry(t *testing.T) {
	file, _ := projectBindingFixture(t)
	var writes atomic.Int32
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		connection, _, _ := w.(http.Hijacker).Hijack()
		connection.Close()
	})
	out, _, err := cliInvoke("vm", "project", "mkdir", "project-cli", "src", "--binding-file", file)
	if err == nil || out != "" || writes.Load() != 1 {
		t.Fatalf("uncertain file mutation retried: writes=%d stdout=%q error=%v", writes.Load(), out, err)
	}
}
