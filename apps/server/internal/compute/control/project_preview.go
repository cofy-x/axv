package control

import (
	"context"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

// ProjectPreviewAccess validates the original environment on every request.
// Preview streams do not hold lifecycle leases: stopping a VM must remain
// possible while a development service has an open WebSocket.
func (s *Service) ProjectPreviewAccess(ctx context.Context, b compute.ProjectBinding) (GuestAccess, protocol.ProjectFileScope, error) {
	current, err := s.ProjectBinding(ctx, b.ProjectID, b.HostID)
	if err != nil {
		return GuestAccess{}, protocol.ProjectFileScope{}, err
	}
	if current.Binding != b {
		return GuestAccess{}, protocol.ProjectFileScope{}, compute.Problem("binding_changed", "The preview environment changed. Register a new preview explicitly.")
	}
	access, err := s.BoundGuestAccess(ctx, b.MachineID, "project-previews", b.HostID, true)
	return access, protocol.ProjectFileScope{Root: current.Project.Root, RootIdentity: current.Project.RootIdentity, BootID: b.BootID, InstanceID: b.InstanceID}, err
}
