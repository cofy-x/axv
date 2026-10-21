//go:build darwin || linux

package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

func codexVMFixtureProject() vmwork.Project {
	return vmwork.Project{Project: compute.Project{ID: "project", HostID: "host", Kind: "vm", Name: "Retained repository", VolumeID: "volume", Root: "repo", RootIdentity: "inode:123", Revision: 1, CreatedAt: time.Now().UTC()}, Binding: compute.ProjectBinding{HostID: "host", ProjectID: "project", ProjectRevision: 1, VolumeID: "volume", VolumeRevision: 1, MachineID: "vm-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BootID: "boot", InstanceID: "guest"}}
}

func TestVMProjectCreateAndContinuationPreserveOriginalRunContext(t *testing.T) {
	manager, server, _ := realManager(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	manager.settings.RunnerExecutable = executable
	original := codexVMFixtureProject()
	current := original
	resolves := 0
	manager.settings.ResolveVMProject = func(_ context.Context, binding compute.ProjectBinding) (VMProjectResolution, error) {
		resolves++
		if binding != current.Binding {
			return VMProjectResolution{}, compute.Problem("binding_changed", "Changed VM environment.")
		}
		return VMProjectResolution{Project: current, Socket: "/private/compute.sock", ConfigFingerprint: "pinned-config"}, nil
	}
	create := map[string]any{"requestId": "vm-new", "vmProject": map[string]any{"binding": original.Binding}}
	status, data := jsonRequest(t, server, "POST", "/api/host/codex/sessions", create)
	first := waitRunning(t, server, decodedRun(t, status, data).CodexSession)
	if first.Target != "host" || first.VMProject == nil || !reflect.DeepEqual(*first.VMProject, original) || filepath.Base(filepath.Dir(first.ProjectPath)) != "codex-workspaces" {
		t.Fatalf("VM project changed host agent boundary: %+v", first)
	}
	captured, err := vmwork.ReadContext(filepath.Join(manager.runDirectory(first.RunID), "vm-project.json"))
	if err != nil || captured.RunID != first.RunID || captured.SessionID != first.ID || captured.Project.Binding != original.Binding || captured.Socket != "/private/compute.sock" || captured.Executable != executable {
		t.Fatalf("private run context lost captured target: %+v %v", captured, err)
	}
	if projects, err := manager.Projects(context.Background()); err != nil || len(projects) != 0 {
		t.Fatalf("VM workspace appeared as host project: %+v %v", projects, err)
	}
	status, data = jsonRequest(t, server, "POST", runPath(first)+"/end", nil)
	if ended := decodedRun(t, status, data); ended.Status != "ended" {
		t.Fatalf("VM run did not end: %+v", ended)
	}
	originalWorkspace := first.ProjectPath + ".retained"
	if err := os.Rename(first.ProjectPath, originalWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(first.ProjectPath, 0700); err != nil {
		t.Fatal(err)
	}
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions/"+first.ID+"/resume", map[string]any{"requestId": "replaced-workspace", "expectedRunId": first.RunID, "vmProject": map[string]any{"binding": original.Binding}})
	if status != 409 || !strings.Contains(string(data), "workspace_changed") || resolves != 1 {
		t.Fatalf("same-path replacement workspace was adopted: %d %s resolves=%d", status, data, resolves)
	}
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions", create)
	if retried := decodedRun(t, status, data); retried.RunID != first.RunID || resolves != 1 {
		t.Fatalf("lost-response recovery revalidated a replaced workspace: %+v resolves=%d", retried, resolves)
	}
	entries, err := os.ReadDir(first.ProjectPath)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected continuation reseeded private workspace: %+v %v", entries, err)
	}
	if err := os.Remove(first.ProjectPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(originalWorkspace, first.ProjectPath); err != nil {
		t.Fatal(err)
	}
	kept, err := manager.store.CodexSession(context.Background(), first.ID)
	if err != nil || kept.RunID != first.RunID || kept.ConversationID != first.ConversationID {
		t.Fatalf("workspace refusal changed provider history identity: %+v %v", kept, err)
	}
	current.Binding.MachineID = "vm-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	current.Binding.BootID = "next-boot"
	current.Binding.InstanceID = "next-instance"
	current.Binding.VolumeRevision = 2
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions", create)
	if retried := decodedRun(t, status, data); retried.RunID != first.RunID || resolves != 1 {
		t.Fatalf("retry refreshed original VM context: %+v resolves=%d", retried, resolves)
	}
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions", map[string]any{"requestId": "stale-new", "vmProject": map[string]any{"binding": original.Binding}})
	if status != 409 {
		t.Fatalf("stale environment started host or replacement run: %d %s", status, data)
	}
	resumePath := "/api/host/codex/sessions/" + first.ID + "/resume"
	status, data = jsonRequest(t, server, "POST", resumePath, map[string]any{"requestId": "missing-binding", "expectedRunId": first.RunID})
	if status != 409 {
		t.Fatalf("VM continuation omitted reviewed binding: %d %s", status, data)
	}
	status, data = jsonRequest(t, server, "POST", resumePath, map[string]any{"requestId": "stale-resume", "expectedRunId": first.RunID, "vmProject": map[string]any{"binding": original.Binding}})
	if status != 409 {
		t.Fatalf("continuation silently rebound replacement: %d %s", status, data)
	}
	resume := map[string]any{"requestId": "vm-continue", "expectedRunId": first.RunID, "vmProject": map[string]any{"binding": current.Binding}}
	status, data = jsonRequest(t, server, "POST", resumePath, resume)
	second := waitRunning(t, server, decodedRun(t, status, data).CodexSession)
	if second.ID != first.ID || second.RunID == first.RunID || second.ConversationID != first.ConversationID || second.ProjectPath != first.ProjectPath || second.VMProject == nil || second.VMProject.Binding != current.Binding {
		t.Fatalf("explicit continuation changed conversation identity: %+v", second)
	}
	resolvesBeforeRetry := resolves
	manager.settings.ResolveVMProject = func(context.Context, compute.ProjectBinding) (VMProjectResolution, error) {
		t.Fatal("matching lost-response retry revalidated live VM")
		return VMProjectResolution{}, nil
	}
	status, data = jsonRequest(t, server, "POST", resumePath, resume)
	if retried := decodedRun(t, status, data); retried.RunID != second.RunID || resolves != resolvesBeforeRetry {
		t.Fatalf("continuation retry duplicated run: %+v", retried)
	}
	status, data = jsonRequest(t, server, "GET", runPath(first), nil)
	old := decodedRun(t, status, data)
	if old.VMProject == nil || old.VMProject.Binding != original.Binding || old.RunID != first.RunID {
		t.Fatalf("old window adopted replacement context: %+v", old)
	}
	again, err := vmwork.ReadContext(filepath.Join(manager.runDirectory(first.RunID), "vm-project.json"))
	if err != nil || !reflect.DeepEqual(again, captured) {
		t.Fatalf("old CLI commands rebound to later run: %+v %v", again, err)
	}
	status, data = jsonRequest(t, server, "POST", runPath(first)+"/end", nil)
	if old := decodedRun(t, status, data); old.RunID != first.RunID {
		t.Fatalf("old End addressed newer VM run: %+v", old)
	}
}

func TestVMProjectRequestRejectsNullUnicodeAndUnknownFieldsBeforeHostWork(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	manager.settings.ResolveVMProject = func(context.Context, compute.ProjectBinding) (VMProjectResolution, error) {
		t.Fatal("invalid VM request inspected compute service")
		return VMProjectResolution{}, nil
	}
	for _, body := range []string{
		`{"requestId":"null","vmProject":null}`,
		`{"requestId":"unknown","vmProject":{"binding":{},"refresh":true}}`,
		`{"requestId":"unknown","vmProject":{"binding":{"hostId":"host","unexpected":true}}}`,
		`{"requestId":"bad-unicode","vmProject":{"binding":{"hostId":"\ud800"}}}`,
		`{"requestId":"missing","vmProject":{"binding":{}}}`,
	} {
		response := metadataRequest(t, manager, "POST", "/api/host/codex/sessions", body)
		if response.Code != 400 {
			t.Fatalf("invalid request reached launch: %d %s", response.Code, response.Body.String())
		}
	}
	if sessions, err := manager.store.CodexSessions(context.Background()); err != nil || len(sessions) != 0 {
		t.Fatalf("invalid VM request reserved sessions: %+v %v", sessions, err)
	}
}
