package gateway

import (
	"context"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
)

// ProjectContext is a captured private host route and verified project. It
// does not retain the gateway or select another route after publication.
type ProjectContext struct {
	Project                   compute.ProjectBindingResult
	Socket, ConfigFingerprint string
}

// CaptureProject verifies the caller's already reviewed binding through one
// immutable routing generation. Later requests retain these exact identities.
func (g *Gateway) CaptureProject(ctx context.Context, expected compute.ProjectBinding) (ProjectContext, error) {
	g.mu.Lock()
	binding, closed := g.binding, g.closed
	g.mu.Unlock()
	if closed || binding == nil {
		return ProjectContext{}, compute.Problem("unavailable", "The VM service is unavailable.")
	}
	if binding.hostID != "" && binding.hostID != expected.HostID {
		return ProjectContext{}, compute.Problem("binding_changed", "The selected project belongs to another VM host.")
	}
	client, err := computeclient.Connect(ctx, binding.socket, computeclient.Binding{HostID: expected.HostID, ConfigFingerprint: binding.fingerprint})
	if err != nil {
		return ProjectContext{}, err
	}
	defer client.Close()
	project, err := client.ProjectBinding(ctx, expected.ProjectID)
	if err != nil {
		return ProjectContext{}, err
	}
	if project.Binding != expected {
		return ProjectContext{}, compute.Problem("binding_changed", "The reviewed project environment changed.")
	}
	return ProjectContext{Project: project, Socket: binding.socket, ConfigFingerprint: client.Binding.ConfigFingerprint}, nil
}
