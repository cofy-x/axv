package control

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func (s *Service) InitializeProject(ctx context.Context, projectID string, req compute.InitializeProjectRequest) (compute.ProjectTaskResult, error) {
	spec, err := compute.ProjectInitializeSpec(req)
	if err != nil {
		return compute.ProjectTaskResult{}, err
	}
	return s.submitProjectTask(ctx, projectID, compute.SubmitProjectTaskRequest{RequestID: req.RequestID, Binding: req.Binding, Spec: spec}, true)
}

// FinishProjectInitialization is an explicit registration of the task's new
// directory. It never refreshes the original task's execution environment.
func (s *Service) FinishProjectInitialization(ctx context.Context, projectID, requestID string, binding compute.ProjectBinding) (compute.Project, error) {
	result, err := s.ProjectTaskByRequest(ctx, projectID, requestID, binding.HostID)
	if err != nil {
		return compute.Project{}, err
	}
	if binding != result.Attempt.Binding {
		return compute.Project{}, compute.Problem("binding_changed", "Finish must retain the initialization attempt's original environment.")
	}
	intent := result.Task.Spec.Initialization
	if intent == nil || compute.ValidateInitializationTaskSpec(result.Task.Spec) != nil {
		return compute.Project{}, compute.Problem("invalid_request", "This receipt is not an AXV project initialization task.")
	}
	if result.Attempt.Status != "succeeded" || !result.Attempt.GuestFinal {
		return compute.Project{}, compute.Problem("task_conflict", "The original project initialization must succeed before its directory can be opened as a project.")
	}
	created := result.Attempt.CreatedRoot
	if created == nil || created.Root != path.Join(result.Attempt.Root, intent.Directory) || created.RootIdentity == "" {
		return compute.Project{}, compute.Problem("guest_identity_mismatch", "The initialization has no verified receipt for its created directory.")
	}
	hash := sha256.Sum256([]byte(result.Attempt.ID))
	req := compute.RegisterProjectRequest{RequestID: fmt.Sprintf("initialize-finish-%x", hash), VolumeID: binding.VolumeID, VolumeRevision: binding.VolumeRevision, MachineID: binding.MachineID, Root: created.Root, Name: intent.Name}
	// A committed finalization remains recoverable even after the VM is gone.
	if p, exists, err := projectRequest(ctx, s.store.db, req); exists || err != nil {
		return p, err
	}
	release, err := s.store.projectLeases.acquire(ctx, false, binding.VolumeID, binding.MachineID)
	if err != nil {
		return compute.Project{}, err
	}
	defer release()
	parent, machine, driver, err := s.taskEnvironment(ctx, projectID, binding)
	if err != nil {
		return compute.Project{}, err
	}
	if parent.Root != result.Attempt.Root || parent.RootIdentity != result.Attempt.RootIdentity {
		return compute.Project{}, compute.Problem("project_changed", "The initialization parent no longer matches its original receipt.")
	}
	var root protocol.ProjectFileRoot
	if err = projectJSON(ctx, machine, driver, "inspect", protocol.ProjectFileScope{Root: parent.Root, RootIdentity: parent.RootIdentity, BootID: binding.BootID, InstanceID: binding.InstanceID}, &root); err != nil {
		return compute.Project{}, err
	}
	if root.Root != parent.Root || root.RootIdentity != parent.RootIdentity {
		return compute.Project{}, compute.Problem("project_changed", "The initialization's parent directory changed; no replacement was adopted.")
	}
	if err = projectJSON(ctx, machine, driver, "inspect", protocol.ProjectFileScope{Root: req.Root, RootIdentity: created.RootIdentity, BootID: binding.BootID, InstanceID: binding.InstanceID}, &root); err != nil {
		return compute.Project{}, err
	}
	if root.Root != req.Root || root.RootIdentity != created.RootIdentity {
		return compute.Project{}, compute.Problem("guest_identity_mismatch", "The guest could not verify the initialized directory.")
	}
	return s.store.registerProject(ctx, req, root.RootIdentity)
}
