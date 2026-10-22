package control

import (
	"context"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"testing"
)

func TestProjectPreviewAccessRequiresOriginalBindingAndCapability(t *testing.T) {
	f := newProjectFixture(t, "project-previews")
	current := f.registered(t)
	access, scope, err := f.service.ProjectPreviewAccess(context.Background(), current.Binding)
	if err != nil || access.Machine.ID != current.Binding.MachineID || scope.RootIdentity != current.Project.RootIdentity {
		t.Fatalf("preview access = %#v %#v %v", access, scope, err)
	}
	f.mu.Lock()
	f.health.InstanceID = "replacement-instance"
	f.mu.Unlock()
	if _, _, err = f.service.ProjectPreviewAccess(context.Background(), current.Binding); compute.ErrorCode(err) != "binding_changed" {
		t.Fatalf("replaced environment: %v", err)
	}
	missing := newProjectFixture(t)
	old := missing.registered(t)
	if _, _, err = missing.service.ProjectPreviewAccess(context.Background(), old.Binding); compute.ErrorCode(err) != "unsupported" {
		t.Fatalf("missing capability: %v", err)
	}
}
