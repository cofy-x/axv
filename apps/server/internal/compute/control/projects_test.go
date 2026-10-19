package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

type projectFixture struct {
	service *Service
	driver  *lifecycleDriver
	machine compute.Machine
	request compute.RegisterProjectRequest
	mu      sync.Mutex
	health  protocol.Health
	calls   atomic.Int32
	file    func(http.ResponseWriter, *http.Request)
}

func newProjectFixture(t *testing.T) *projectFixture {
	t.Helper()
	s, driver, created := guestFixture(t, &guestimage.Contract{ImageVersion: 1, ProtocolVersion: protocol.Version, Capabilities: []string{"project-files"}})
	f := &projectFixture{service: s, driver: driver}
	f.health = protocol.Health{Version: protocol.Version, MachineID: created.Machine.ID, BootID: "boot-original", InstanceID: "guest-original", Ready: true, Capabilities: []string{"project-files"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		health, file := f.health, f.file
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "guest token mismatch", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/v1/health" {
			_ = json.NewEncoder(w).Encode(health)
			return
		}
		f.calls.Add(1)
		if r.URL.Path == "/v1/project-files/inspect" {
			var in protocol.ProjectFileScope
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.BootID != health.BootID || in.InstanceID != health.InstanceID || (in.RootIdentity != "" && in.RootIdentity != "inode:123") {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(compute.Error{Code: "project_changed", Message: "wrong original scope"})
				return
			}
			_ = json.NewEncoder(w).Encode(protocol.ProjectFileRoot{Root: in.Root, RootIdentity: "inode:123"})
			return
		}
		if file != nil {
			file(w, r)
			return
		}
		var in protocol.ProjectFileWriteRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Root != "repo" || in.RootIdentity != "inode:123" || in.BootID != health.BootID || in.InstanceID != health.InstanceID {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(compute.Error{Code: "project_changed", Message: "file escaped original scope"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/mkdir") {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(protocol.ProjectFileEntry{Path: in.Path, Name: in.Path, Kind: "folder"})
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.ProjectFile{Path: in.Path, Content: in.Content, SHA256: "reviewed-hash", Size: int64(len(in.Content))})
	}))
	t.Cleanup(server.Close)
	driver.address = strings.TrimPrefix(server.URL, "http://")
	f.machine = runClaimed(t, s)
	v, err := s.store.Volume(context.Background(), f.machine.Spec.VolumeID)
	if err != nil {
		t.Fatal(err)
	}
	f.request = compute.RegisterProjectRequest{RequestID: "register-original", Name: "Real project", Root: "repo", VolumeID: v.ID, VolumeRevision: v.Revision, MachineID: f.machine.ID}
	return f
}

func (f *projectFixture) registered(t *testing.T) compute.ProjectBindingResult {
	t.Helper()
	p, err := f.service.RegisterProject(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := f.service.ProjectBinding(context.Background(), p.ID, f.service.store.HostID)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestProjectRegistrationPersistsAndReplaysWithoutGuest(t *testing.T) {
	f := newProjectFixture(t)
	bound := f.registered(t)
	f.mu.Lock()
	f.health.Ready = false
	f.mu.Unlock()
	before := f.calls.Load()
	replayed, err := f.service.RegisterProject(context.Background(), f.request)
	if err != nil || replayed.ID != bound.Project.ID || f.calls.Load() != before {
		t.Fatalf("registration retry changed its identity or required guest access: %+v %v", replayed, err)
	}
	changed := f.request
	changed.Root = "different-project"
	if _, err := f.service.RegisterProject(context.Background(), changed); compute.ErrorCode(err) != "idempotency_conflict" {
		t.Fatalf("request payload was silently changed: %v", err)
	}
	projects, err := f.service.Projects(context.Background())
	if err != nil || len(projects) != 1 || projects[0] != bound.Project {
		t.Fatalf("persistent project identity: %+v %v", projects, err)
	}
	var dir string
	if err := f.service.store.db.QueryRow("SELECT substr(file,1,length(file)-length('/compute.sqlite')) FROM pragma_database_list WHERE name='main'").Scan(&dir); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	project, err := reopened.Project(context.Background(), bound.Project.ID)
	if err != nil || project != bound.Project || reopened.HostID != bound.Binding.HostID {
		t.Fatalf("restart replaced project or host identity: %+v %v", project, err)
	}
}

func TestProjectRegistrationRejectsWrongVolumeAndReusedOperationKey(t *testing.T) {
	f := newProjectFixture(t)
	for _, modify := range []func(*compute.RegisterProjectRequest){
		func(r *compute.RegisterProjectRequest) { r.VolumeRevision++ },
		func(r *compute.RegisterProjectRequest) { r.MachineID = "another-machine" },
		func(r *compute.RegisterProjectRequest) { r.Root = "../host" },
		func(r *compute.RegisterProjectRequest) { r.RequestID = "guest-fixture-create" },
	} {
		r := f.request
		modify(&r)
		before := f.calls.Load()
		if _, err := f.service.RegisterProject(context.Background(), r); err == nil || f.calls.Load() != before {
			t.Fatalf("invalid registration reached guest: %+v %v", r, err)
		}
	}
	f.registered(t)
	if _, err := f.service.Action(context.Background(), f.machine.ID, compute.ActionRequest{RequestID: f.request.RequestID, Revision: f.machine.Revision, Action: "stop"}); compute.ErrorCode(err) != "idempotency_conflict" {
		t.Fatalf("operation reused registration key: %v", err)
	}
}

func TestProjectFileBindingsRejectStaleIdentitiesBeforeGuestAccess(t *testing.T) {
	f := newProjectFixture(t)
	original := f.registered(t)
	for _, field := range []string{"host", "project", "project-revision", "volume", "volume-revision", "machine", "boot", "instance"} {
		t.Run(field, func(t *testing.T) {
			binding := original.Binding
			switch field {
			case "host":
				binding.HostID = "replacement-host"
			case "project":
				binding.ProjectID = "replacement-project"
			case "project-revision":
				binding.ProjectRevision++
			case "volume":
				binding.VolumeID = "replacement-volume"
			case "volume-revision":
				binding.VolumeRevision++
			case "machine":
				binding.MachineID = "replacement-machine"
			case "boot":
				binding.BootID = "replacement-boot"
			case "instance":
				binding.InstanceID = "replacement-guest"
			}
			before := f.calls.Load()
			if _, err := f.service.ProjectFiles(context.Background(), original.Project.ID, "read", compute.ProjectFileRequest{Binding: binding, Path: "package.json"}); compute.ErrorCode(err) != "binding_changed" || f.calls.Load() != before {
				t.Fatalf("stale %s reached file access: %v", field, err)
			}
		})
	}
	f.mu.Lock()
	f.health.InstanceID = "guest-restarted"
	f.mu.Unlock()
	if _, err := f.service.ProjectFiles(context.Background(), original.Project.ID, "read", compute.ProjectFileRequest{Binding: original.Binding, Path: "package.json"}); compute.ErrorCode(err) != "binding_changed" {
		t.Fatalf("same boot with a new guest instance accepted old file request: %v", err)
	}
}

func TestProjectFilesForwardOnlyExactScopeAndPreserveGuestErrors(t *testing.T) {
	f := newProjectFixture(t)
	b := f.registered(t)
	empty := ""
	req := compute.ProjectFileRequest{Binding: b.Binding, Path: "new.txt", Content: strings.Repeat("x", 100<<10), ExpectedSHA256: &empty}
	result, err := f.service.ProjectFiles(context.Background(), b.Project.ID, "write", req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	var file protocol.ProjectFile
	if err := json.Unmarshal(data, &file); err != nil || file.Content != req.Content || file.Path != req.Path {
		t.Fatalf("write result lost project file bytes: %+v %v", file, err)
	}
	if _, err := f.service.ProjectFiles(context.Background(), b.Project.ID, "mkdir", compute.ProjectFileRequest{Binding: b.Binding, Path: "src"}); err != nil {
		t.Fatalf("successful guest mkdir was rejected: %v", err)
	}
	f.mu.Lock()
	f.file = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInsufficientStorage)
		_ = json.NewEncoder(w).Encode(compute.Error{Code: "disk_full", Message: "project volume is full"})
	}
	f.mu.Unlock()
	if _, err := f.service.ProjectFiles(context.Background(), b.Project.ID, "write", req); compute.ErrorCode(err) != "disk_full" {
		t.Fatalf("disk error was hidden: %v", err)
	}
}

func TestProjectFileLeasePrecedesNewLifecycleIntent(t *testing.T) {
	f := newProjectFixture(t)
	b := f.registered(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.file = func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_ = json.NewEncoder(w).Encode(protocol.ProjectFile{Path: "readme.txt", Content: "original"})
	}
	f.mu.Unlock()
	fileDone := make(chan error, 1)
	go func() {
		_, err := f.service.ProjectFiles(context.Background(), b.Project.ID, "read", compute.ProjectFileRequest{Binding: b.Binding, Path: "readme.txt"})
		fileDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("guest file request did not start")
	}
	mutationDone := make(chan error, 1)
	go func() {
		_, err := f.service.Action(context.Background(), f.machine.ID, compute.ActionRequest{RequestID: "stop-during-file-request", Revision: f.machine.Revision, Action: "stop"})
		mutationDone <- err
	}()
	select {
	case err := <-mutationDone:
		close(release)
		t.Fatalf("lifecycle intent changed during the guest file lease: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-fileDone; err != nil {
		t.Fatal(err)
	}
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
	}
	before := f.calls.Load()
	if _, err := f.service.ProjectFiles(context.Background(), b.Project.ID, "read", compute.ProjectFileRequest{Binding: b.Binding, Path: "readme.txt"}); err == nil || f.calls.Load() != before {
		t.Fatalf("accepted stop did not block later files: %v", err)
	}
}

func TestProjectFileLeaseLeavesOtherResourcesAndCancellationAvailable(t *testing.T) {
	f := newProjectFixture(t)
	b := f.registered(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	f.mu.Lock()
	f.file = func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_ = json.NewEncoder(w).Encode(protocol.ProjectFile{Path: "readme.txt", Content: "original"})
	}
	f.mu.Unlock()
	fileDone := make(chan error, 1)
	go func() {
		_, err := f.service.ProjectFiles(context.Background(), b.Project.ID, "read", compute.ProjectFileRequest{Binding: b.Binding, Path: "readme.txt"})
		fileDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("guest file request did not start")
	}
	otherDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		spec := f.machine.Spec
		spec.Name, spec.VolumeID = "Unrelated VM", ""
		result, err := f.service.Create(ctx, compute.CreateRequest{RequestID: "unrelated-create", Spec: spec})
		if err == nil {
			var op operationRecord
			var found bool
			op, found, err = f.service.store.Claim(ctx)
			if err == nil && (!found || op.ID != result.Operation.ID) {
				err = fmt.Errorf("unrelated operation could not be claimed")
			}
			if err == nil {
				err = f.service.store.Finish(ctx, op, compute.Observation{Status: "stopped"}, nil)
			}
			if err == nil {
				var m compute.Machine
				m, err = f.service.store.Machine(ctx, result.Machine.ID)
				if err == nil {
					_, err = f.service.Action(ctx, m.ID, compute.ActionRequest{RequestID: "unrelated-delete", Revision: m.Revision, Action: "delete"})
				}
			}
		}
		otherDone <- err
	}()
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatalf("unrelated lifecycle was delayed by guest I/O: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("guest I/O blocked another VM's lifecycle")
	}
	cancelled := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := f.service.Action(ctx, f.machine.ID, compute.ActionRequest{RequestID: "cancel-waiting-stop", Revision: f.machine.Revision, Action: "stop"})
		cancelled <- err
	}()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting lifecycle did not honor cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lease acquisition ignored context cancellation")
	}
	m, err := f.service.store.Machine(context.Background(), f.machine.ID)
	if err != nil || m.Desired != "running" || m.Revision != f.machine.Revision {
		t.Fatalf("cancelled admission changed original VM: %+v %v", m, err)
	}
	once.Do(func() { close(release) })
	if err := <-fileDone; err != nil {
		t.Fatal(err)
	}
}

func TestProjectRetainsIdentityWhenVMIsReplaced(t *testing.T) {
	f := newProjectFixture(t)
	b := f.registered(t)
	ctx := context.Background()
	deleted, err := f.service.Action(ctx, f.machine.ID, compute.ActionRequest{RequestID: "delete-original-vm", Revision: f.machine.Revision, Action: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	op, found, err := f.service.store.Claim(ctx)
	if err != nil || !found || op.ID != deleted.Operation.ID {
		t.Fatalf("claim delete: %v %v", found, err)
	}
	if err := f.service.store.Finish(ctx, op, compute.Observation{Status: "deleted"}, nil); err != nil {
		t.Fatal(err)
	}
	v, _ := f.service.store.Volume(ctx, b.Project.VolumeID)
	if !v.Retain || v.MachineID != "" {
		t.Fatalf("project storage was not retained: %+v", v)
	}
	if _, err := f.service.DeleteVolume(ctx, v.ID, "delete-project-volume", v.Revision); compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("registered project volume was deletable: %v", err)
	}
	if _, err := f.service.store.VolumeAction(ctx, f.service.config, v.ID, compute.VolumeRequest{RequestID: "unretain-project-volume", Revision: v.Revision, Action: "retain", Retain: false}); compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("registered project volume lost retention: %v", err)
	}
	spec := f.machine.Spec
	spec.Name, spec.VolumeID = "Replacement VM", v.ID
	replacement, err := f.service.Create(ctx, compute.CreateRequest{RequestID: "create-replacement-vm", Spec: spec, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.health.MachineID, f.health.BootID, f.health.InstanceID = replacement.Machine.ID, "new-boot", "new-guest"
	f.mu.Unlock()
	newMachine := runClaimed(t, f.service)
	before := f.calls.Load()
	if _, err := f.service.ProjectFiles(ctx, b.Project.ID, "read", compute.ProjectFileRequest{Binding: b.Binding, Path: "README.md"}); compute.ErrorCode(err) != "binding_changed" || f.calls.Load() != before {
		t.Fatalf("old request was sent to replacement VM: %v", err)
	}
	next, err := f.service.ProjectBinding(ctx, b.Project.ID, b.Binding.HostID)
	if err != nil || next.Project != b.Project || next.Binding.MachineID != newMachine.ID || next.Binding.VolumeRevision == b.Binding.VolumeRevision {
		t.Fatalf("replacement changed persistent project identity or reused binding: %+v %v", next, err)
	}
	// Changing VM workload kind must not bypass volume retention protection.
	spec = newMachine.Spec
	spec.Kind, spec.TTLSeconds, spec.RetainVolume = "task", 3600, false
	_, err = f.service.store.Update(ctx, f.service.config, newMachine.ID, compute.UpdateRequest{RequestID: "unretain-through-config", Revision: newMachine.Revision, Spec: spec})
	if compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("configuration bypassed project retention: %v", err)
	}
}

func TestProjectRejectsInvalidRoots(t *testing.T) {
	for _, root := range []string{"/host", ".", "..", "a/../b", "a//b", "a/", "a\\b", ".axv-private", "a\n", string([]byte{0xff})} {
		t.Run(fmt.Sprintf("%q", root), func(t *testing.T) {
			if err := compute.ValidateProjectRoot(root); err == nil {
				t.Fatalf("accepted invalid root %q", root)
			}
		})
	}
}

func TestProjectRejectsInvalidUTF8BeforeGuestEncoding(t *testing.T) {
	f := newProjectFixture(t)
	b := f.registered(t)
	before := f.calls.Load()
	invalid := string([]byte{0xff})
	for _, field := range []string{"name", "root", "requestId"} {
		req := f.request
		switch field {
		case "name":
			req.Name = invalid
		case "root":
			req.Root = invalid
		case "requestId":
			req.RequestID = "invalid-" + invalid
		}
		if _, err := f.service.RegisterProject(context.Background(), req); compute.ErrorCode(err) != "invalid_request" {
			t.Fatalf("registration normalized invalid %s: %v", field, err)
		}
	}
	for _, field := range []string{"path", "content", "cursor"} {
		expected := ""
		req := compute.ProjectFileRequest{Binding: b.Binding, Path: "file.txt", Content: "text", ExpectedSHA256: &expected}
		switch field {
		case "path":
			req.Path = invalid
		case "content":
			req.Content = invalid
		case "cursor":
			req.Cursor = invalid
		}
		if _, err := f.service.ProjectFiles(context.Background(), b.Project.ID, "write", req); compute.ErrorCode(err) != "invalid_request" {
			t.Fatalf("file operation normalized invalid %s: %v", field, err)
		}
	}
	if f.calls.Load() != before {
		t.Fatal("invalid Unicode reached the guest")
	}
}
