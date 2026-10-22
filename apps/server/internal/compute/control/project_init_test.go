package control

import (
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func TestProjectInitializationCapturedRootCannotBeOverwrittenByStaleReconciliation(t *testing.T) {
	f := newTaskFixture(t, "project-initialization")
	req := compute.InitializeProjectRequest{RequestID: "initialize-root-reconcile", Binding: f.bound.Binding, Directory: "new-project"}
	accepted, err := f.service.InitializeProject(t.Context(), f.bound.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	f.reconcile(t, accepted.Task.ID)
	f.finish(accepted.Attempt.ID, nil)
	f.reconcile(t, accepted.Task.ID)
	current, err := f.service.ProjectTask(t.Context(), f.bound.Project.ID, accepted.Task.ID, f.bound.Project.HostID)
	if err != nil || current.Attempt.CreatedRoot == nil {
		t.Fatalf("root not persisted: %+v %v", current, err)
	}
	response := protocol.TaskExecution{TaskIdentity: taskIdentity(current.Attempt), Spec: current.Task.Spec, Status: current.Attempt.Status, FinishedAt: current.Attempt.FinishedAt, CreatedRoot: &protocol.ProjectFileRoot{Root: current.Attempt.CreatedRoot.Root, RootIdentity: "replacement-root"}}
	if err := f.service.store.saveTaskExecution(t.Context(), current.Attempt.ID, response, nil); compute.ErrorCode(err) != "guest_identity_mismatch" {
		t.Fatal("transaction overwrote root captured by another reconciliation", err)
	}
}

func TestProjectInitializationDurableReceiptAndExplicitFinish(t *testing.T) {
	f := newTaskFixture(t, "project-initialization")
	req := compute.InitializeProjectRequest{RequestID: "initialize-original", Binding: f.bound.Binding, Directory: "new-repository", Repository: "https://example.com/repository.git", Name: "New project"}
	accepted, err := f.service.InitializeProject(t.Context(), f.bound.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Attempt.Binding != req.Binding || accepted.Attempt.Status != "accepted" {
		t.Fatalf("initialization lost original binding: %+v", accepted)
	}
	replay, err := f.service.InitializeProject(t.Context(), f.bound.Project.ID, req)
	if err != nil || replay.Attempt.ID != accepted.Attempt.ID {
		t.Fatalf("replay duplicated initialization: %+v %v", replay, err)
	}
	changed := req
	changed.Name = "Another project"
	if _, err := f.service.InitializeProject(t.Context(), f.bound.Project.ID, changed); compute.ErrorCode(err) != "idempotency_conflict" {
		t.Fatal("changed initialization silently replayed", err)
	}
	if _, err := f.service.FinishProjectInitialization(t.Context(), f.bound.Project.ID, req.RequestID, req.Binding); compute.ErrorCode(err) != "task_conflict" {
		t.Fatal("unfinished task created a project", err)
	}
	wrong := req.Binding
	wrong.InstanceID = "replacement"
	if _, err := f.service.FinishProjectInitialization(t.Context(), f.bound.Project.ID, req.RequestID, wrong); compute.ErrorCode(err) != "binding_changed" {
		t.Fatal("finish replaced original environment", err)
	}
	f.reconcile(t, accepted.Task.ID)
	f.finish(accepted.Attempt.ID, nil)
	f.reconcile(t, accepted.Task.ID)
	project, err := f.service.FinishProjectInitialization(t.Context(), f.bound.Project.ID, req.RequestID, req.Binding)
	if err != nil || project.Root != "repo/new-repository" || project.Name != "New project" || project.VolumeID != req.Binding.VolumeID {
		t.Fatalf("finish did not register initialized directory: %+v %v", project, err)
	}
	f.projectFixture.mu.Lock()
	f.health.Ready = false
	f.projectFixture.mu.Unlock()
	recovered, err := f.service.FinishProjectInitialization(t.Context(), f.bound.Project.ID, req.RequestID, req.Binding)
	if err != nil || recovered.ID != project.ID {
		t.Fatalf("committed finish was not recoverable offline: %+v %v", recovered, err)
	}
	replay, err = f.service.InitializeProject(t.Context(), f.bound.Project.ID, req)
	if err != nil || replay.Attempt.ID != accepted.Attempt.ID {
		t.Fatalf("accepted initialization was not recoverable offline: %+v %v", replay, err)
	}
}

func TestProjectInitializationRejectsChangedGuestBeforeRegistration(t *testing.T) {
	f := newTaskFixture(t, "project-initialization")
	req := compute.InitializeProjectRequest{RequestID: "initialize-stale-guest", Binding: f.bound.Binding, Directory: "empty-project"}
	accepted, err := f.service.InitializeProject(t.Context(), f.bound.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	f.reconcile(t, accepted.Task.ID)
	f.finish(accepted.Attempt.ID, nil)
	f.reconcile(t, accepted.Task.ID)
	f.projectFixture.mu.Lock()
	f.health.InstanceID = "new-executor"
	f.projectFixture.mu.Unlock()
	if _, err := f.service.FinishProjectInitialization(t.Context(), f.bound.Project.ID, req.RequestID, req.Binding); compute.ErrorCode(err) != "binding_changed" {
		t.Fatal("finish silently adopted another executor", err)
	}
	projects, err := f.service.Projects(t.Context())
	if err != nil || len(projects) != 1 {
		t.Fatalf("stale finish created a project: %+v %v", projects, err)
	}
}

func TestProjectInitializationCannotFinishAnOrdinaryTask(t *testing.T) {
	f := newTaskFixture(t, "project-initialization")
	accepted := f.submit(t, "ordinary-task-original")
	if _, err := f.service.FinishProjectInitialization(t.Context(), f.bound.Project.ID, accepted.Task.RequestID, f.bound.Binding); compute.ErrorCode(err) != "invalid_request" {
		t.Fatal("ordinary task was adopted as initialization", err)
	}
}

func TestProjectInitializationOrdinarySubmissionCannotForgeTypedIntent(t *testing.T) {
	f := newTaskFixture(t, "project-initialization")
	spec, err := compute.ProjectInitializeSpec(compute.InitializeProjectRequest{RequestID: "initialize-forged-original", Directory: "safe-child", Name: "Example"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, compute.SubmitProjectTaskRequest{RequestID: "initialize-forged-original", Binding: f.bound.Binding, Spec: spec}); compute.ErrorCode(err) != "invalid_request" {
		t.Fatal("ordinary submission accepted reserved intent", err)
	}
}
