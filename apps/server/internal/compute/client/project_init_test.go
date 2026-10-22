package client

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestProjectInitializationClientRetainsRequestAndBinding(t *testing.T) {
	binding := compute.ProjectBinding{HostID: "host", ProjectID: "project", VolumeID: "volume", MachineID: "machine", VolumeRevision: 1, ProjectRevision: 1, BootID: "boot", InstanceID: "instance"}
	req := compute.InitializeProjectRequest{RequestID: "initialize/client?#%", Binding: binding, Directory: "new-project", Repository: "https://example.com/repository.git", Name: "Example"}
	var calls []string
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			runtime(w, "host")
			return
		}
		if r.Header.Get("X-AXV-Host-ID") != "host" {
			t.Error("client did not retain original host")
		}
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		if r.URL.Path == "/v1/projects/project/initialize" {
			var received compute.InitializeProjectRequest
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil || !reflect.DeepEqual(received, req) {
				t.Errorf("request changed: %+v %v", received, err)
			}
			json.NewEncoder(w).Encode(compute.ProjectTaskResult{Task: compute.ProjectTask{ID: "task"}, Attempt: compute.TaskAttempt{ID: "attempt", Binding: binding}})
			return
		}
		var received struct {
			Binding compute.ProjectBinding `json:"binding"`
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil || received.Binding != binding {
			t.Errorf("finish changed binding: %+v %v", received, err)
		}
		json.NewEncoder(w).Encode(map[string]any{"project": compute.Project{ID: "created-project"}})
	}))
	client, err := Connect(t.Context(), socket, Binding{HostID: "host"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if result, err := client.InitializeProject(t.Context(), "project", req); err != nil || result.Attempt.ID != "attempt" {
		t.Fatalf("initialization: %+v %v", result, err)
	}
	if result, err := client.FinishProjectInitialization(t.Context(), "project", req.RequestID, binding); err != nil || result.ID != "created-project" {
		t.Fatalf("finish: %+v %v", result, err)
	}
	want := []string{"POST /v1/projects/project/initialize", "POST /v1/projects/project/initializations/initialize%2Fclient%3F%23%25/finish"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("wrong routes: %v", calls)
	}
}
