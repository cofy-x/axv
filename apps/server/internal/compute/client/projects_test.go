package client

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestProjectRequestsRejectInvalidUTF8BeforeSending(t *testing.T) {
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
	for _, change := range []struct {
		name string
		edit func(*compute.RegisterProjectRequest)
	}{
		{"request ID", func(r *compute.RegisterProjectRequest) { r.RequestID = invalid }},
		{"volume ID", func(r *compute.RegisterProjectRequest) { r.VolumeID = invalid }},
		{"machine ID", func(r *compute.RegisterProjectRequest) { r.MachineID = invalid }},
		{"root", func(r *compute.RegisterProjectRequest) { r.Root = invalid }},
		{"name", func(r *compute.RegisterProjectRequest) { r.Name = invalid }},
	} {
		t.Run("registration/"+change.name, func(t *testing.T) {
			request := compute.RegisterProjectRequest{RequestID: "original-request", VolumeID: "volume", MachineID: "machine", VolumeRevision: 1, Root: "repo", Name: "Project"}
			change.edit(&request)
			if _, err := c.RegisterProject(t.Context(), request); compute.ErrorCode(err) != "invalid_request" {
				t.Fatalf("invalid UTF-8 registration value was encoded: %v", err)
			}
		})
	}
	if _, err := c.ProjectBinding(t.Context(), invalid); compute.ErrorCode(err) != "invalid_request" {
		t.Fatalf("invalid UTF-8 project ID was escaped: %v", err)
	}
	for _, change := range []struct {
		name string
		edit func(*compute.ProjectFileRequest)
	}{
		{"host ID", func(r *compute.ProjectFileRequest) { r.Binding.HostID = invalid }},
		{"project ID", func(r *compute.ProjectFileRequest) { r.Binding.ProjectID = invalid }},
		{"volume ID", func(r *compute.ProjectFileRequest) { r.Binding.VolumeID = invalid }},
		{"machine ID", func(r *compute.ProjectFileRequest) { r.Binding.MachineID = invalid }},
		{"boot ID", func(r *compute.ProjectFileRequest) { r.Binding.BootID = invalid }},
		{"instance ID", func(r *compute.ProjectFileRequest) { r.Binding.InstanceID = invalid }},
		{"path", func(r *compute.ProjectFileRequest) { r.Path = invalid }},
		{"cursor", func(r *compute.ProjectFileRequest) { r.Cursor = invalid }},
		{"content", func(r *compute.ProjectFileRequest) { r.Content = invalid }},
		{"expected SHA-256", func(r *compute.ProjectFileRequest) { r.ExpectedSHA256 = &invalid }},
	} {
		t.Run("file/"+change.name, func(t *testing.T) {
			request := compute.ProjectFileRequest{Binding: compute.ProjectBinding{HostID: "host", ProjectID: "project", ProjectRevision: 1, VolumeID: "volume", VolumeRevision: 1, MachineID: "machine", BootID: "boot", InstanceID: "instance"}, Path: "readme.txt"}
			change.edit(&request)
			if _, err := c.WriteProjectFile(t.Context(), "project", request); compute.ErrorCode(err) != "invalid_request" {
				t.Fatalf("invalid UTF-8 file value was encoded: %v", err)
			}
		})
	}
	if _, err := c.ReadProjectFile(t.Context(), invalid, compute.ProjectFileRequest{}); compute.ErrorCode(err) != "invalid_request" {
		t.Fatalf("invalid UTF-8 file project ID was escaped: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid UTF-8 requests reached the host: %d", requests.Load())
	}
}

func TestProjectClientPreservesUnicodePathsAndFileBytes(t *testing.T) {
	content, path := "Hello 世界 😀 �\n", "项目/说明.md"
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			runtime(w, "host")
			return
		}
		var request compute.ProjectFileRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Path != path || request.Content != content {
			t.Errorf("Unicode project request changed: %+v", request)
		}
		json.NewEncoder(w).Encode(map[string]any{"path": request.Path, "content": request.Content})
	}))
	c, err := Connect(t.Context(), socket, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	expected := ""
	result, err := c.WriteProjectFile(t.Context(), "project", compute.ProjectFileRequest{Path: path, Content: content, ExpectedSHA256: &expected})
	if err != nil || result.Path != path || result.Content != content {
		t.Fatalf("Unicode project response changed: %+v %v", result, err)
	}
}
