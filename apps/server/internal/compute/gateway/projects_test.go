package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestCaptureProjectRejectsReplacementAndRetainsCapturedRoute(t *testing.T) {
	dir := shortSocketDirectory(t)
	firstSocket, nextSocket := filepath.Join(dir, "first.sock"), filepath.Join(dir, "next.sock")
	expected := compute.ProjectBinding{HostID: "first", ProjectID: "project", ProjectRevision: 1, VolumeID: "volume", VolumeRevision: 1, MachineID: "machine", BootID: "boot", InstanceID: "guest"}
	project := compute.Project{ID: "project", HostID: "first", Kind: "vm", Name: "Repository", VolumeID: "volume", Root: "repo", RootIdentity: "inode:1", Revision: 1, CreatedAt: time.Now().UTC()}
	entered, release := make(chan struct{}), make(chan struct{})
	bindingHost(t, firstSocket, "first", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/project/binding" || r.Header.Get("X-AXV-Host-ID") != "first" {
			t.Errorf("unexpected binding request %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		close(entered)
		<-release
		_ = json.NewEncoder(w).Encode(compute.ProjectBindingResult{Project: project, Binding: expected})
	})
	bindingHost(t, nextSocket, "next", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("capture reached replacement host")
		w.WriteHeader(400)
	})
	gateway := NewGateway("", GatewayOptions{})
	defer gateway.Close()
	gateway.Bind(firstSocket, "first", "first-config")
	type result struct {
		value ProjectContext
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := gateway.CaptureProject(context.Background(), expected)
		done <- result{value, err}
	}()
	<-entered
	gateway.Bind(nextSocket, "next", "next-config")
	close(release)
	got := <-done
	if got.err != nil || got.value.Socket != firstSocket || got.value.ConfigFingerprint != "first-config" || got.value.Project.Binding != expected {
		t.Fatalf("in-flight capture rebound: %+v %v", got.value, got.err)
	}
	if _, err := gateway.CaptureProject(context.Background(), expected); compute.ErrorCode(err) != "binding_changed" {
		t.Fatalf("old view reached replacement host: %v", err)
	}
}

func TestCaptureProjectRequiresExactReviewedGuestBinding(t *testing.T) {
	dir := shortSocketDirectory(t)
	socket := filepath.Join(dir, "host.sock")
	expected := compute.ProjectBinding{HostID: "host", ProjectID: "project", ProjectRevision: 1, VolumeID: "volume", VolumeRevision: 1, MachineID: "machine", BootID: "boot", InstanceID: "guest"}
	bindingHost(t, socket, "host", func(w http.ResponseWriter, _ *http.Request) {
		changed := expected
		changed.InstanceID = "replacement"
		_ = json.NewEncoder(w).Encode(compute.ProjectBindingResult{Binding: changed})
	})
	gateway := NewGateway("", GatewayOptions{})
	defer gateway.Close()
	gateway.Bind(socket, "host", "host-config")
	if _, err := gateway.CaptureProject(context.Background(), expected); compute.ErrorCode(err) != "binding_changed" {
		t.Fatalf("capture refreshed stale binding: %v", err)
	}
}
