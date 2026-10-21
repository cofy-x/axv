package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

// This fixture tests the real host service and durable store through HTTP. The
// fake guest verifies the host's scoped envelope and independently rejects an
// obsolete service instance, including a change after the health handshake.
type projectHTTPFixture struct {
	service           *serviceFixture
	machine           compute.Machine
	instance          atomic.Value
	forcedError       atomic.Value
	changeAfterHealth atomic.Bool
	healthCalls       atomic.Int32
	fileCalls         atomic.Int32
	writes            atomic.Int32
	mu                sync.Mutex
	files             map[string]guestproto.ProjectFile
	capabilities      []string
}

func newProjectHTTPFixture(t *testing.T, capabilities ...string) *projectHTTPFixture {
	t.Helper()
	service, spec := operationService(t)
	created, err := service.Create(t.Context(), compute.CreateRequest{RequestID: "http-project-create", Spec: spec, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	op, found, err := service.Store.Claim(t.Context())
	if err != nil || !found {
		t.Fatalf("claim: %v, %v", found, err)
	}
	if err = service.Store.Finish(t.Context(), op, compute.Observation{Status: "running", Ready: true}, nil); err != nil {
		t.Fatal(err)
	}
	machine, err := service.Store.Machine(t.Context(), created.Machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &projectHTTPFixture{service: service, machine: machine, files: map[string]guestproto.ProjectFile{}, capabilities: append([]string{"project-files"}, capabilities...)}
	fixture.instance.Store("instance-original")
	fixture.forcedError.Store("")
	server := httptest.NewServer(http.HandlerFunc(fixture.serveGuest))
	t.Cleanup(server.Close)
	service.Drivers = map[string]compute.Driver{"lima": accessDriver{address: strings.TrimPrefix(server.URL, "http://")}}
	service.rebuild()
	return fixture
}

func (fixture *projectHTTPFixture) serveGuest(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-guest-token" {
		write(w, 403, map[string]string{"code": "forbidden", "error": "missing injected guest authentication"})
		return
	}
	if r.URL.Path == "/v1/health" {
		fixture.healthCalls.Add(1)
		write(w, 200, guestproto.Health{Version: guestproto.Version, MachineID: fixture.machine.ID, BootID: "boot-project", InstanceID: fixture.instance.Load().(string), Ready: true, Capabilities: fixture.capabilities})
		if fixture.changeAfterHealth.Swap(false) {
			fixture.instance.Store("instance-replacement")
		}
		return
	}
	fixture.fileCalls.Add(1)
	var request struct {
		guestproto.ProjectFileScope
		Path           string  `json:"path"`
		Content        string  `json:"content"`
		ExpectedSHA256 *string `json:"expectedSha256"`
		Cursor         string  `json:"cursor"`
		Limit          int     `json:"limit"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		write(w, 400, map[string]string{"code": "invalid_request", "error": err.Error()})
		return
	}
	if request.BootID != "boot-project" || request.InstanceID != fixture.instance.Load().(string) {
		write(w, 409, map[string]string{"code": "instance_changed", "error": "The original guest service no longer exists."})
		return
	}
	if request.Root != "repository" || (request.RootIdentity != "inode:project-root" && r.URL.Path != "/v1/project-files/inspect") {
		write(w, 409, map[string]string{"code": "project_changed", "error": "The host changed the original project root."})
		return
	}
	if code := fixture.forcedError.Load().(string); code != "" {
		status := map[string]int{"disk_full": 507, "not_text": 415, "file_conflict": 409}[code]
		write(w, status, map[string]string{"code": code, "error": "Guest file operation failed: " + code})
		return
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	switch r.URL.Path {
	case "/v1/project-files/inspect":
		write(w, 200, guestproto.ProjectFileRoot{Root: request.Root, RootIdentity: "inode:project-root"})
	case "/v1/project-files/write":
		if request.ExpectedSHA256 == nil {
			write(w, 400, map[string]string{"code": "invalid_request", "error": "The reviewed hash is required."})
			return
		}
		fixture.writes.Add(1)
		digest := sha256.Sum256([]byte(request.Content))
		file := guestproto.ProjectFile{Path: request.Path, Content: request.Content, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(request.Content)), ModifiedAt: time.Now().UTC()}
		fixture.files[request.Path] = file
		write(w, 200, file)
	case "/v1/project-files/read":
		file, found := fixture.files[request.Path]
		if !found {
			write(w, 404, map[string]string{"code": "not_found", "error": "The file does not exist."})
			return
		}
		write(w, 200, file)
	case "/v1/project-files/mkdir":
		write(w, 201, guestproto.ProjectFileEntry{Name: filepath.Base(request.Path), Path: request.Path, Kind: "folder", ModifiedAt: time.Now().UTC()})
	case "/v1/project-files/list":
		write(w, 200, guestproto.ProjectFileListing{Entries: []guestproto.ProjectFileEntry{}})
	default:
		http.NotFound(w, r)
	}
}

func projectHTTPCall(t *testing.T, handler http.Handler, hostID, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body := ""
	if raw, ok := value.(string); ok {
		body = raw
	} else if value != nil {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		body = string(data)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-AXV-Host-ID", hostID)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertProjectHTTPProblem(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var problem compute.Error
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != status || problem.Code != code || problem.Message == "" {
		t.Fatalf("expected %d/%s, got HTTP %d: %s, %v", status, code, response.Code, response.Body, err)
	}
}

func (fixture *projectHTTPFixture) registerAndBind(t *testing.T) compute.ProjectBindingResult {
	t.Helper()
	volume, err := fixture.service.Store.Volume(t.Context(), fixture.machine.Spec.VolumeID)
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(fixture.service.Service)
	response := projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", "/v1/projects", compute.RegisterProjectRequest{RequestID: "register-http-project", VolumeID: volume.ID, VolumeRevision: volume.Revision, MachineID: fixture.machine.ID, Root: "repository", Name: "HTTP project"})
	var registered struct {
		Project compute.Project `json:"project"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &registered); err != nil || response.Code != 201 || registered.Project.ID == "" || registered.Project.RootIdentity != "inode:project-root" {
		t.Fatalf("registration: HTTP %d: %s, %v", response.Code, response.Body, err)
	}
	response = projectHTTPCall(t, handler, fixture.service.Store.HostID, "GET", "/v1/projects/"+registered.Project.ID+"/binding", nil)
	var binding compute.ProjectBindingResult
	if err := json.Unmarshal(response.Body.Bytes(), &binding); err != nil || response.Code != 200 || binding.Project.ID != registered.Project.ID || binding.Binding.ProjectID != registered.Project.ID || binding.Binding.VolumeID != volume.ID || binding.Binding.VolumeRevision != volume.Revision || binding.Binding.MachineID != fixture.machine.ID || binding.Binding.InstanceID != "instance-original" || binding.Binding.BootID != "boot-project" {
		t.Fatalf("binding: HTTP %d: %s, %v", response.Code, response.Body, err)
	}
	return binding
}

func TestVMProjectHTTPRegistersBindsAndTransfersLargeFile(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	binding := fixture.registerAndBind(t)
	handler := Handler(fixture.service.Service)
	base := "/v1/projects/" + binding.Project.ID
	content := strings.Repeat("large project text\n", 5000)
	expected := ""
	request := compute.ProjectFileRequest{Binding: binding.Binding, Path: "src/large.txt", Content: content, ExpectedSHA256: &expected}
	response := projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", base+"/files/write", request)
	var written struct {
		compute.ProjectBindingResult
		guestproto.ProjectFile
	}
	if err := json.Unmarshal(response.Body.Bytes(), &written); err != nil || response.Code != 200 || written.Content != content || written.Binding != binding.Binding || written.Size != int64(len(content)) {
		t.Fatalf("large write: HTTP %d: %s, %v", response.Code, response.Body, err)
	}
	response = projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", base+"/files/read", compute.ProjectFileRequest{Binding: binding.Binding, Path: request.Path})
	var read guestproto.ProjectFile
	if err := json.Unmarshal(response.Body.Bytes(), &read); err != nil || response.Code != 200 || read.Content != content || read.SHA256 != written.SHA256 {
		t.Fatalf("large read: HTTP %d: %s, %v", response.Code, response.Body, err)
	}
	// An explicit empty string is a valid edit, distinct from missing content.
	request.Content, expected = "", written.SHA256
	response = projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", base+"/files/write", request)
	if err := json.Unmarshal(response.Body.Bytes(), &written); err != nil || response.Code != 200 || written.Content != "" || written.Size != 0 {
		t.Fatalf("explicit empty write: HTTP %d: %s, %v", response.Code, response.Body, err)
	}
	response = projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", base+"/files/mkdir", compute.ProjectFileRequest{Binding: binding.Binding, Path: "new-folder"})
	var folder guestproto.ProjectFileEntry
	if err := json.Unmarshal(response.Body.Bytes(), &folder); err != nil || response.Code != 200 || folder.Path != "new-folder" || folder.Kind != "folder" {
		t.Fatalf("guest mkdir201 was not accepted: HTTP %d: %s, %v", response.Code, response.Body, err)
	}
	response = projectHTTPCall(t, handler, fixture.service.Store.HostID, "GET", "/v1/projects", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), binding.Project.ID) || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("project listing/headers: HTTP %d: %s", response.Code, response.Body)
	}
}

func TestVMProjectHTTPRequiresOriginalHostBeforeLookupOrGuestAccess(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	handler := Handler(fixture.service.Service)
	for _, hostID := range []string{"", "replacement-host"} {
		for _, route := range []struct{ method, path string }{{"GET", "/v1/projects"}, {"GET", "/v1/projects/missing"}, {"GET", "/v1/projects/missing/binding"}, {"POST", "/v1/projects"}, {"POST", "/v1/projects/missing/files/write"}} {
			assertProjectHTTPProblem(t, projectHTTPCall(t, handler, hostID, route.method, route.path, `{}`), 409, "binding_changed")
		}
	}
	if fixture.healthCalls.Load() != 0 || fixture.fileCalls.Load() != 0 {
		t.Fatal("missing/wrong host identity reached the guest")
	}
}

func TestVMProjectHTTPPropagatesGuestFileErrors(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	binding := fixture.registerAndBind(t)
	for code, status := range map[string]int{"disk_full": 507, "not_text": 415, "file_conflict": 409} {
		t.Run(code, func(t *testing.T) {
			fixture.forcedError.Store(code)
			expected := ""
			response := projectHTTPCall(t, Handler(fixture.service.Service), fixture.service.Store.HostID, "POST", "/v1/projects/"+binding.Project.ID+"/files/write", compute.ProjectFileRequest{Binding: binding.Binding, Path: "file", Content: "text", ExpectedSHA256: &expected})
			assertProjectHTTPProblem(t, response, status, code)
		})
	}
	if fixture.writes.Load() != 0 {
		t.Fatal("a rejected guest file operation published a write")
	}
}

func TestVMProjectHTTPRejectsMalformedAndMismatchedWriteEnvelopes(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	binding := fixture.registerAndBind(t)
	handler := Handler(fixture.service.Service)
	path := "/v1/projects/" + binding.Project.ID + "/files/write"
	expected := ""
	valid := compute.ProjectFileRequest{Binding: binding.Binding, Path: "file", Content: "text", ExpectedSHA256: &expected}
	data, _ := json.Marshal(valid)
	body := string(data)
	healthCalls, fileCalls := fixture.healthCalls.Load(), fixture.fileCalls.Load()
	for _, malformed := range []string{"null", "[]", body + " {}", strings.TrimSuffix(body, "}") + `,"unknown":true}`, strings.TrimSuffix(body, "}") + `,"limit":0}`, strings.Replace(body, `,"expectedSha256":""`, "", 1), strings.Replace(body, `"expectedSha256":""`, `"expectedSha256":null`, 1), strings.Replace(body, `,"content":"text"`, "", 1), strings.Replace(body, `"content":"text"`, `"content":null`, 1)} {
		assertProjectHTTPProblem(t, projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", path, malformed), 400, "invalid_request")
	}
	for _, invalid := range []string{string([]byte{'"', 0xff, '"'}), `"\ud800"`, `"\udc00"`, `"\ud800\u0041"`} {
		assertProjectHTTPProblem(t, projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", path, strings.Replace(body, `"content":"text"`, `"content":`+invalid, 1)), 400, "invalid_request")
		assertProjectHTTPProblem(t, projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", "/v1/projects", `{"requestId":"unicode-registration","volumeId":`+invalid+`,"volumeRevision":1,"machineId":"vm-original","root":"repo"}`), 400, "invalid_request")
	}
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", path+"?path=outside", valid), 400, "invalid_request")
	mismatch := valid
	mismatch.Binding.HostID = "another-host"
	assertProjectHTTPProblem(t, projectHTTPCall(t, handler, fixture.service.Store.HostID, "POST", path, mismatch), 409, "binding_changed")
	request := httptest.NewRequest("POST", path, strings.NewReader(body))
	request.Header.Set("X-AXV-Host-ID", fixture.service.Store.HostID)
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertProjectHTTPProblem(t, response, 400, "invalid_request")
	if fixture.healthCalls.Load() != healthCalls || fixture.fileCalls.Load() != fileCalls {
		t.Fatal("invalid file envelope reached the guest")
	}
}

func TestVMProjectHTTPRejectsSameBootServiceReplacementAndHandshakeRace(t *testing.T) {
	for _, afterHealth := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-health", true: "after-health"}[afterHealth], func(t *testing.T) {
			fixture := newProjectHTTPFixture(t)
			binding := fixture.registerAndBind(t)
			previousCalls := fixture.fileCalls.Load()
			if afterHealth {
				fixture.changeAfterHealth.Store(true)
			} else {
				fixture.instance.Store("instance-replacement")
			}
			expected := ""
			response := projectHTTPCall(t, Handler(fixture.service.Service), fixture.service.Store.HostID, "POST", "/v1/projects/"+binding.Project.ID+"/files/write", compute.ProjectFileRequest{Binding: binding.Binding, Path: "file", Content: "stale write", ExpectedSHA256: &expected})
			code := "binding_changed"
			if afterHealth {
				code = "instance_changed"
			}
			assertProjectHTTPProblem(t, response, 409, code)
			if fixture.writes.Load() != 0 || (!afterHealth && fixture.fileCalls.Load() != previousCalls) {
				t.Fatal("stale service binding executed a write")
			}
			if afterHealth && fixture.fileCalls.Load() != previousCalls+1 {
				t.Fatal("the handshake race did not reach the independently fenced guest")
			}
		})
	}
}

func TestVMProjectGatewayKeepsLocalPeerOriginAndHostBoundaries(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	binding := fixture.registerAndBind(t)
	dir, err := os.MkdirTemp("", "axv-project-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	host := Handler(fixture.service.Service)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			// Provider workers are unnecessary for this already-running fixture;
			// expose its real identity as ready for connection verification.
			runtime := fixture.service.Runtime()
			runtime.Ready = true
			write(w, 200, runtime)
			return
		}
		host.ServeHTTP(w, r)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	gateway := computegateway.NewGateway("", computegateway.GatewayOptions{})
	gateway.Bind(socket, fixture.service.Store.HostID, fixture.service.Config.Fingerprint())
	t.Cleanup(gateway.Close)
	expected := ""
	data, _ := json.Marshal(compute.ProjectFileRequest{Binding: binding.Binding, Path: "file", Content: "gateway write", ExpectedSHA256: &expected})
	for _, test := range []struct {
		name, host, peer, origin, hostID string
		status                           int
		code                             string
	}{
		{"missing-origin", "localhost:3001", "127.0.0.1:12345", "", fixture.service.Store.HostID, 403, "compute_forbidden"},
		{"cross-origin", "localhost:3001", "127.0.0.1:12345", "https://example.com", fixture.service.Store.HostID, 403, "compute_forbidden"},
		{"remote-peer", "localhost:3001", "192.0.2.1:12345", "http://localhost:3001", fixture.service.Store.HostID, 403, "compute_forbidden"},
		{"remote-host", "example.com:3001", "127.0.0.1:12345", "http://example.com:3001", fixture.service.Store.HostID, 403, "compute_forbidden"},
		{"missing-host-binding", "localhost:3001", "127.0.0.1:12345", "http://localhost:3001", "", 409, "binding_changed"},
		{"wrong-host-binding", "localhost:3001", "127.0.0.1:12345", "http://localhost:3001", "replacement-host", 409, "binding_changed"},
		{"local-same-origin", "localhost:3001", "127.0.0.1:12345", "http://localhost:3001", fixture.service.Store.HostID, 200, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/compute/projects/"+binding.Project.ID+"/files/write", strings.NewReader(string(data)))
			request.Host, request.RemoteAddr = test.host, test.peer
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-AXV-Host-ID", test.hostID)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Forwarded-For", "127.0.0.1")
			request.Header.Set("X-Forwarded-Host", "localhost:3001")
			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, request)
			if test.code != "" {
				assertProjectHTTPProblem(t, response, test.status, test.code)
			} else if response.Code != 200 || !strings.Contains(response.Body.String(), "gateway write") {
				t.Fatalf("same-origin request failed: HTTP %d: %s", response.Code, response.Body)
			}
		})
	}
	if fixture.writes.Load() != 1 {
		t.Fatalf("forbidden gateway mutations reached guest; writes=%d", fixture.writes.Load())
	}
	// The desktop gateway intentionally retains the same registered project and
	// binding throughout the request; it does not obtain a replacement binding.
	project, err := fixture.service.Store.Project(context.Background(), binding.Project.ID)
	if err != nil || project.VolumeID != binding.Project.VolumeID || project.RootIdentity != binding.Project.RootIdentity {
		t.Fatalf("gateway changed persistent project identity: %+v, %v", project, err)
	}
}
