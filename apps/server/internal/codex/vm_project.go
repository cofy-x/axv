package codex

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/hostdir"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
	"github.com/cofy-x/axv/apps/server/internal/storage"
	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

func applyVMProjectInput(in *storage.CodexRunInput, project *VMProjectInput) error {
	if project == nil {
		return nil
	}
	if err := vmwork.ValidateBinding(project.Binding); err != nil {
		return failure(Invalid, "invalid_session", "Provide the complete VM project environment binding.")
	}
	binding := project.Binding
	in.VMProjectBinding = &binding
	return nil
}

func (m *Manager) resolveVMProject(ctx context.Context, in *storage.CodexRunInput, previous *vmwork.Project) error {
	if m.settings.ResolveVMProject == nil {
		return failure(Unavailable, "vm_project_unavailable", "The VM project service is unavailable. No host project was substituted.")
	}
	if previous != nil && (previous.Binding.HostID != in.VMProjectBinding.HostID || previous.Binding.ProjectID != in.VMProjectBinding.ProjectID || previous.Binding.VolumeID != in.VMProjectBinding.VolumeID) {
		return failure(Conflict, "project_changed", "This conversation belongs to a different VM project. Start a separate conversation instead.")
	}
	resolved, err := m.settings.ResolveVMProject(ctx, *in.VMProjectBinding)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		code := compute.ErrorCode(err)
		switch code {
		case "binding_changed", "boot_changed", "instance_changed", "project_changed", "volume_conflict", "revision_conflict":
			return failure(Conflict, "vm_project_changed", "The reviewed VM project environment changed. Review it explicitly before starting another run.")
		default:
			return failure(Unavailable, "vm_project_unavailable", "The reviewed VM project could not be verified. Check its VM and guest service, then retry the same action.")
		}
	}
	if vmwork.ValidateProject(resolved.Project) != nil || resolved.Project.Binding != *in.VMProjectBinding || !filepath.IsAbs(resolved.Socket) || resolved.ConfigFingerprint == "" {
		return failure(Unavailable, "vm_project_unavailable", "The VM project service returned an invalid context. No run was started.")
	}
	if previous != nil && !vmwork.SameProjectIdentity(*previous, resolved.Project) {
		return failure(Conflict, "project_changed", "The registered VM project root changed. Start a separate conversation instead.")
	}
	in.VMProject, in.VMSocket, in.VMConfigFingerprint = &resolved.Project, resolved.Socket, resolved.ConfigFingerprint
	return nil
}

// Each VM conversation gets an empty host workspace for the host provider's
// trust and history identity. Guest files are never mirrored into this path.
func (m *Manager) vmWorkspace(requestID string) (string, error) {
	dir := filepath.Join(m.settings.DataDir, "codex-workspaces", fmt.Sprintf("%x", sha256.Sum256([]byte(requestID))))
	if err := owneddir.Ensure(dir, m.namePrefix+"/"+requestID); err != nil {
		return "", failure(Unavailable, "workspace_unavailable", "The private Codex workspace could not be verified. Check the AXV data directory.")
	}
	canonical, err := hostdir.CanonicalDirectory(dir)
	if err != nil {
		return "", err
	}
	return canonical, nil
}

// Continuation must not adopt another directory at the original host path.
// Ownership checks never recreate the workspace or reseed a missing marker.
func (m *Manager) checkVMWorkspace(ctx context.Context, session storage.CodexSession) error {
	initial, err := m.store.CodexInitialRun(ctx, session.ID)
	if err != nil {
		return err
	}
	info, statErr := os.Lstat(session.ProjectPath)
	if initial.VMProject == nil || initial.ProjectPath != session.ProjectPath || statErr != nil || !info.IsDir() || owneddir.Check(session.ProjectPath, m.namePrefix+"/"+initial.RequestID) != nil {
		return failure(Conflict, "workspace_changed", "The original private Codex workspace could not be verified. Restore it before continuing this conversation; no replacement workspace or conversation was created.")
	}
	return nil
}
