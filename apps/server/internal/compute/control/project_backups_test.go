package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

type projectCopyDriver struct {
	*lifecycleDriver
	copies  int
	failure error
}

func (d *projectCopyDriver) CopyVolume(context.Context, compute.Volume, compute.Volume) error {
	d.copies++
	return d.failure
}

func backupFixture(t *testing.T) (*projectFixture, compute.ProjectBindingResult, *projectCopyDriver) {
	t.Helper()
	f := newProjectFixture(t, "project-tasks", "project-restore")
	b := f.registered(t)
	d := &projectCopyDriver{lifecycleDriver: f.driver}
	f.service.drivers["lima"] = d
	f.service.config.Capacity.DiskGiB = 128
	return f, b, d
}

func stopBackupSource(t *testing.T, f *projectFixture, b compute.ProjectBindingResult) compute.BackupProjectRequest {
	t.Helper()
	m, _ := f.service.Machine(context.Background(), f.machine.ID)
	if _, err := f.service.Action(context.Background(), m.ID, compute.ActionRequest{RequestID: "backup-stop-source", Revision: m.Revision, Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	m = runClaimed(t, f.service)
	v, _ := f.service.store.Volume(context.Background(), m.Spec.VolumeID)
	return compute.BackupProjectRequest{RequestID: "backup-original-request", Name: "Offline backup", MachineID: m.ID, MachineRevision: m.Revision, VolumeRevision: v.Revision, ProjectRevision: b.Project.Revision}
}

func finishProjectCopy(t *testing.T, s *Service, id string) compute.Volume {
	t.Helper()
	op, found, err := s.store.Claim(context.Background())
	if err != nil || !found || op.ID != id {
		t.Fatalf("claim copy: %+v %v", op, err)
	}
	observed, err := s.perform(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.Finish(context.Background(), op, observed, nil); err != nil {
		t.Fatal(err)
	}
	v, err := s.store.Volume(context.Background(), op.VolumeID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestProjectBackupRequiresStoppedSourceAndReconciledTasks(t *testing.T) {
	f, b, d := backupFixture(t)
	ctx := context.Background()
	running := compute.BackupProjectRequest{RequestID: "running-backup-request", Name: "Blocked", ProjectRevision: b.Project.Revision, VolumeRevision: b.Binding.VolumeRevision, MachineID: f.machine.ID, MachineRevision: f.machine.Revision}
	if _, err := f.service.BackupProject(ctx, b.Project.ID, running); compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("running source accepted: %v", err)
	}
	task, err := f.service.store.admitTask(ctx, b.Project.ID, "", "backup-active-task", "task-hash", b.Binding, compute.NormalizeTaskSpec(compute.TaskSpec{Argv: []string{"sleep", "30"}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.saveTaskFailure(ctx, task.Attempt.ID, "unknown", "transport lost"); err != nil {
		t.Fatal(err)
	}
	req := stopBackupSource(t, f, b)
	if _, err := f.service.BackupProject(ctx, b.Project.ID, req); compute.ErrorCode(err) != "task_conflict" {
		t.Fatalf("unknown work accepted: %v", err)
	}
	if err := f.service.store.saveTaskFailure(ctx, task.Attempt.ID, "interrupted", "VM confirmed stopped"); err != nil {
		t.Fatal(err)
	}
	accepted, err := f.service.BackupProject(ctx, b.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Action(ctx, req.MachineID, compute.ActionRequest{RequestID: "start-during-backup", Revision: req.MachineRevision, Action: "start"}); compute.ErrorCode(err) != "operation_conflict" {
		t.Fatalf("backup source started during capture: %v", err)
	}
	claimed, found, err := f.service.store.Claim(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err := f.service.store.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	v := finishProjectCopy(t, f.service, claimed.ID)
	if !v.IsProjectBackup() || v.Backup.SourceVolumeID != b.Project.VolumeID || len(v.Backup.Projects) != 1 || v.Backup.Projects[0].ID != b.Project.ID || v.Status != "ready" || d.copies != 1 {
		t.Fatalf("bad backup: %+v", v)
	}
	replayed, err := f.service.BackupProject(ctx, b.Project.ID, req)
	if err != nil || replayed.Operation.ID != accepted.Operation.ID || replayed.Volume.ID != v.ID {
		t.Fatalf("changed backup recovery: %+v %v", replayed, err)
	}
	delete(f.service.drivers, v.Provider)
	offline, err := f.service.BackupProject(ctx, b.Project.ID, req)
	if err != nil || offline.Operation.ID != accepted.Operation.ID || offline.Volume.ID != v.ID {
		t.Fatalf("committed backup recovery depended on its provider: %+v %v", offline, err)
	}
	f.service.drivers[v.Provider] = d
	changed := req
	changed.Name = "Changed payload"
	if _, err := f.service.BackupProject(ctx, b.Project.ID, changed); compute.ErrorCode(err) != "idempotency_conflict" {
		t.Fatalf("changed backup payload: %v", err)
	}
	if _, err := f.service.store.VolumeAction(ctx, f.service.config, v.ID, compute.VolumeRequest{RequestID: "mutate-backup", Revision: v.Revision, Action: "retain", Retain: false}); compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("backup became writable: %v", err)
	}
	spec := f.machine.Spec
	spec.Name, spec.VolumeID = "Attach backup directly", v.ID
	if _, err := f.service.Create(ctx, compute.CreateRequest{RequestID: "attach-backup-directly", Spec: spec, Start: true}); compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("backup mounted directly: %v", err)
	}
}

func TestProjectRestoreCreatesSeparateVolumeAndExplicitProject(t *testing.T) {
	f, b, _ := backupFixture(t)
	ctx := context.Background()
	b.Project.Name = strings.Repeat("n", 80)
	if _, err := f.service.store.db.ExecContext(ctx, "UPDATE projects SET data=? WHERE id=?", encode(b.Project), b.Project.ID); err != nil {
		t.Fatal(err)
	}
	req := stopBackupSource(t, f, b)
	accepted, err := f.service.BackupProject(ctx, b.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	backup := finishProjectCopy(t, f.service, accepted.Operation.ID)
	restoreReq := compute.RestoreProjectVolumeRequest{RequestID: "restore-original-request", Revision: backup.Revision, Name: "Restored volume"}
	restored, err := f.service.RestoreProjectVolume(ctx, backup.ID, restoreReq)
	if err != nil {
		t.Fatal(err)
	}
	v := finishProjectCopy(t, f.service, restored.Operation.ID)
	if v.ID == b.Project.VolumeID || v.ID == backup.ID || v.RestoredFrom != backup.ID || v.IsProjectBackup() {
		t.Fatalf("restore reused source identities: %+v", v)
	}
	spec := f.machine.Spec
	spec.Name, spec.VolumeID = "Restored VM", v.ID
	created, err := f.service.Create(ctx, compute.CreateRequest{RequestID: "create-restored-vm", Spec: spec, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.health.MachineID, f.health.BootID, f.health.InstanceID = created.Machine.ID, "restore-boot", "restore-guest"
	f.mu.Unlock()
	m := runClaimed(t, f.service)
	v, _ = f.service.store.Volume(ctx, v.ID)
	if _, err := f.service.RegisterProject(ctx, compute.RegisterProjectRequest{RequestID: "bypass-restore-check", VolumeID: v.ID, VolumeRevision: v.Revision, MachineID: m.ID, Root: b.Project.Root}); compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("generic registration bypassed restore validation: %v", err)
	}
	f.mu.Lock()
	f.file = func(w http.ResponseWriter, r *http.Request) {
		var scope protocol.ProjectFileScope
		if err := json.NewDecoder(r.Body).Decode(&scope); err != nil || r.URL.Path != "/v1/project-files/restore-check" || scope.RootIdentity != b.Project.RootIdentity {
			t.Errorf("wrong restore scope: %+v %v", scope, err)
		}
		_ = json.NewEncoder(w).Encode(protocol.ProjectFileRoot{Root: scope.Root, RootIdentity: scope.RootIdentity})
	}
	f.mu.Unlock()
	bind := compute.BindRestoredProjectRequest{RequestID: "bind-restored-original", SourceProjectID: b.Project.ID, VolumeID: v.ID, VolumeRevision: v.Revision, MachineID: m.ID}
	f.mu.Lock()
	capabilities := f.health.Capabilities
	f.health.Capabilities = []string{"project-files", "project-tasks"}
	f.mu.Unlock()
	if _, err := f.service.BindRestoredProject(ctx, backup.ID, bind); compute.ErrorCode(err) != "unsupported" {
		t.Fatalf("restored binding assumed project-files included restore verification: %v", err)
	}
	f.mu.Lock()
	f.health.Capabilities = capabilities
	f.mu.Unlock()
	image, err := f.service.store.Image(ctx, m.Spec.ImageID)
	if err != nil {
		t.Fatal(err)
	}
	originalImage := encode(image)
	image.Image.Guest.Capabilities = []string{"project-files", "project-tasks"}
	if _, err := f.service.store.db.ExecContext(ctx, "UPDATE catalog SET data=? WHERE id=? AND kind='image'", encode(image), image.Image.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.BindRestoredProject(ctx, backup.ID, bind); compute.ErrorCode(err) != "unsupported" {
		t.Fatalf("runtime capability bypassed the selected image's restore declaration: %v", err)
	}
	if _, err := f.service.store.db.ExecContext(ctx, "UPDATE catalog SET data=? WHERE id=? AND kind='image'", originalImage, image.Image.ID); err != nil {
		t.Fatal(err)
	}
	p, err := f.service.BindRestoredProject(ctx, backup.ID, bind)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == b.Project.ID || p.VolumeID != v.ID || p.Root != b.Project.Root || p.RootIdentity != b.Project.RootIdentity || p.Name != b.Project.Name {
		t.Fatalf("restore adopted old project: %+v", p)
	}
	before := f.calls.Load()
	if _, err := f.service.ProjectFiles(ctx, p.ID, "read", compute.ProjectFileRequest{Binding: b.Binding, Path: "README.md"}); compute.ErrorCode(err) != "binding_changed" || f.calls.Load() != before {
		t.Fatalf("old binding redirected to restored volume: %v", err)
	}
	original, _ := f.service.Project(ctx, b.Project.ID)
	if original != b.Project {
		t.Fatalf("original project mutated: %+v", original)
	}
	f.mu.Lock()
	f.health.Ready = false
	f.mu.Unlock()
	replay, err := f.service.BindRestoredProject(ctx, backup.ID, bind)
	if err != nil || replay.ID != p.ID || f.calls.Load() != before {
		t.Fatalf("binding recovery required a fresh environment: %+v %v", replay, err)
	}
}

func TestFailedProjectBackupCannotBeRestored(t *testing.T) {
	f, b, d := backupFixture(t)
	req := stopBackupSource(t, f, b)
	accepted, err := f.service.BackupProject(context.Background(), b.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	d.failure = fmt.Errorf("disk full while copying")
	op, _, _ := f.service.store.Claim(context.Background())
	observed, failure := f.service.perform(context.Background(), op)
	if err := f.service.store.Finish(context.Background(), op, observed, failure); err != nil {
		t.Fatal(err)
	}
	v, _ := f.service.store.Volume(context.Background(), accepted.Volume.ID)
	if v.Status != "failed" || v.TransientGiB == 0 {
		t.Fatalf("incomplete copy allocation lost: %+v", v)
	}
	if _, err := f.service.RestoreProjectVolume(context.Background(), v.ID, compute.RestoreProjectVolumeRequest{RequestID: "restore-incomplete-backup", Revision: v.Revision, Name: "unsafe"}); compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("incomplete backup restored: %v", err)
	}
}
