package storage

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

func vmCodexProject() vmwork.Project {
	return vmwork.Project{
		Project: compute.Project{ID: "project", HostID: "host", Kind: "vm", Name: "VM repository", VolumeID: "volume", Root: "repo", RootIdentity: "inode:123", Revision: 1, CreatedAt: time.Now().UTC()},
		Binding: compute.ProjectBinding{HostID: "host", ProjectID: "project", ProjectRevision: 1, VolumeID: "volume", VolumeRevision: 1, MachineID: "vm-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BootID: "boot", InstanceID: "guest"},
	}
}

func TestCodexVMContextPersistsPerRunAndDoesNotBecomeHostRecent(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	store, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project := vmCodexProject()
	input := CodexRunInput{Mode: "new", RequestID: "vm-create", ProjectPath: "/private/empty/workspace", HistoryHome: "/provider/history", NamePrefix: "axv-vm", Cols: 80, Rows: 24, VMProjectBinding: &project.Binding, VMProject: &project, VMSocket: "/private/host.sock", VMConfigFingerprint: "first-config"}
	first, created, err := store.ReserveCodexRun(ctx, input)
	if err != nil || !created || first.Target != "host" || !reflect.DeepEqual(first.VMProject, &project) {
		t.Fatalf("VM reservation lost independent identities: %+v %v", first, err)
	}
	if _, err = store.BindCodexConversation(ctx, first.RunID, "01234567-89ab-cdef-0123-456789abcdef", first.HistoryHome); err != nil {
		t.Fatal(err)
	}
	if _, err = store.StartCodexRun(ctx, first.RunID); err != nil {
		t.Fatal(err)
	}
	if projects, err := store.CodexProjects(ctx); err != nil || len(projects) != 0 {
		t.Fatalf("VM workspace leaked into host recents: %+v %v", projects, err)
	}
	first, err = store.UpdateCodexRun(ctx, first.RunID, "ended", "Ended.")
	if err != nil {
		t.Fatal(err)
	}
	next := project
	next.Binding.MachineID = "vm-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	next.Binding.BootID = "next-boot"
	next.Binding.InstanceID = "next-guest"
	next.Binding.VolumeRevision = 2
	resume := CodexRunInput{Mode: "resume", RequestID: "vm-resume", SessionID: first.ID, ExpectedRunID: first.RunID, NamePrefix: "axv-vm", Cols: 80, Rows: 24, VMProjectBinding: &next.Binding, VMProject: &next, VMSocket: "/private/next.sock", VMConfigFingerprint: "next-config"}
	second, created, err := store.ReserveCodexRun(ctx, resume)
	if err != nil || !created || second.ProjectPath != first.ProjectPath || second.ConversationID != first.ConversationID || !reflect.DeepEqual(second.VMProject, &next) {
		t.Fatalf("resume lost conversation or reviewed binding: %+v %v", second, err)
	}
	if retry, created, err := store.ReserveCodexRun(ctx, resume); err != nil || created || retry.RunID != second.RunID {
		t.Fatalf("lost response duplicated VM run: %+v %v", retry, err)
	}
	changed := resume
	changedBinding := next.Binding
	changedBinding.InstanceID = "unexpected"
	changed.VMProjectBinding = &changedBinding
	if err := MatchCodexRequest(second, changed); errorCode(err) != "request_conflict" {
		t.Fatalf("retry accepted another binding: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	old, err := reopened.CodexRun(ctx, first.ID, first.RunID)
	if err != nil || !reflect.DeepEqual(old, first) || old.VMSocket == second.VMSocket {
		t.Fatalf("old context rebound after restart: %+v %v", old, err)
	}
	latest, err := reopened.CodexRun(ctx, second.ID, second.RunID)
	if err != nil || !reflect.DeepEqual(latest, second) {
		t.Fatalf("latest context changed after restart: %+v %v", latest, err)
	}
	encoded, err := json.Marshal(latest)
	if err != nil || strings.Contains(string(encoded), "next-config") || strings.Contains(string(encoded), "/private/next.sock") {
		t.Fatalf("private compute route leaked into API: %s %v", encoded, err)
	}
}

func TestCodexVMContinuationCannotSwitchProjectOrLoseItsContext(t *testing.T) {
	store := testStore(t)
	project := vmCodexProject()
	input := CodexRunInput{Mode: "new", RequestID: "vm-create", ProjectPath: "/private/workspace", HistoryHome: "/history", NamePrefix: "axv-vm", Cols: 80, Rows: 24, VMProjectBinding: &project.Binding, VMProject: &project, VMSocket: "/host.sock", VMConfigFingerprint: "config"}
	first, _, err := store.ReserveCodexRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.BindCodexConversation(ctx, first.RunID, "thread", first.HistoryHome); err != nil {
		t.Fatal(err)
	}
	first, err = store.UpdateCodexRun(ctx, first.RunID, "ended", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing", "different project", "different root"} {
		t.Run(kind, func(t *testing.T) {
			next := project
			resume := CodexRunInput{Mode: "resume", RequestID: kind, SessionID: first.ID, ExpectedRunID: first.RunID, NamePrefix: "axv-vm", Cols: 80, Rows: 24}
			if kind != "missing" {
				if kind == "different project" {
					next.Project.ID = "another-project"
					next.Binding.ProjectID = "another-project"
				} else {
					next.Project.RootIdentity = "inode:changed"
				}
				resume.VMProjectBinding = &next.Binding
				resume.VMProject = &next
				resume.VMSocket = "/host.sock"
				resume.VMConfigFingerprint = "config"
			}
			if _, created, err := store.ReserveCodexRun(ctx, resume); created || errorCode(err) != "project_changed" {
				t.Fatalf("conversation changed project: created=%v err=%v", created, err)
			}
		})
	}
}
