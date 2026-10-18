package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

type serviceFixture struct {
	*control.Service
	Store   *control.Store
	Config  compute.Config
	Drivers map[string]compute.Driver
}

func (fixture *serviceFixture) rebuild() {
	fixture.Service = control.NewService(control.Options{Store: fixture.Store, Config: fixture.Config, Drivers: fixture.Drivers})
}

func operationService(t *testing.T) (*serviceFixture, compute.Spec) {
	t.Helper()
	store, err := control.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	config := compute.Config{
		Capacity: compute.Capacity{Resources: compute.Resources{CPUs: 4, MemoryMiB: 4096, DiskGiB: 100}, Machines: 4, Workers: 1},
		Images:   []compute.Image{{ID: "test-image", Provider: "lima", Arch: runtime.GOARCH, Guest: &guestimage.Contract{ImageVersion: 1, ProtocolVersion: guestproto.Version, Capabilities: guestimage.Capabilities()}}},
	}
	if err := store.SeedImages(context.Background(), config.Images); err != nil {
		t.Fatal(err)
	}
	spec := compute.Spec{Name: "Sample VM", Provider: "lima", ImageID: "test-image", Kind: "development", Resources: compute.Resources{CPUs: 2, MemoryMiB: 1024, DiskGiB: 8}, ProjectGiB: 4, RetainVolume: true}
	fixture := &serviceFixture{Store: store, Config: config}
	fixture.rebuild()
	return fixture, spec
}

func TestRequestIDLookupRecoversExactAcceptedOperation(t *testing.T) {
	service, spec := operationService(t)
	requestID := "request/with spaces?and#encoding"
	created, err := service.Create(context.Background(), compute.CreateRequest{RequestID: requestID, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(service.Service)
	for _, tc := range []struct {
		id     string
		status int
	}{
		{requestID, http.StatusOK},
		{"request-not-accepted", http.StatusNotFound},
		{"short", http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/operations/by-request/"+url.PathEscape(tc.id), nil))
		if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("request %q: HTTP %d: %s", tc.id, response.Code, response.Body.String())
		}
		if tc.status == http.StatusOK {
			var op compute.Operation
			if err := json.Unmarshal(response.Body.Bytes(), &op); err != nil || op.ID != created.Operation.ID || op.RequestID != requestID {
				t.Fatalf("recovery changed the accepted identity: %+v, %v", op, err)
			}
		}
	}
}

func TestMachineNameConflictIsHTTP409AndDoesNotCreateAnotherOperation(t *testing.T) {
	service, spec := operationService(t)
	handler := Handler(service.Service)
	for index, requestID := range []string{"create-name-first", "create-name-second"} {
		spec.Name = " Sample VM "
		body, _ := json.Marshal(compute.CreateRequest{RequestID: requestID, Spec: spec})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/machines", strings.NewReader(string(body))))
		if index == 0 && response.Code != http.StatusAccepted {
			t.Fatal(response.Body.String())
		}
		if index == 1 && (response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"name_conflict"`)) {
			t.Fatalf("name conflict: HTTP %d: %s", response.Code, response.Body.String())
		}
	}
	operations, _ := service.Store.Operations(context.Background())
	if len(operations) != 1 {
		t.Fatalf("rejected duplicate left an operation: %+v", operations)
	}
}

func TestMachineCreationStartsByDefaultAndHonorsExplicitStoppedCreation(t *testing.T) {
	for _, explicitStopped := range []bool{false, true} {
		t.Run(fmt.Sprint(explicitStopped), func(t *testing.T) {
			service, spec := operationService(t)
			request := map[string]any{"requestId": "create-start-default", "spec": spec}
			if explicitStopped {
				request["start"] = false
			}
			body, _ := json.Marshal(request)
			response := httptest.NewRecorder()
			Handler(service.Service).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/machines", strings.NewReader(string(body))))
			var result compute.Result
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusAccepted {
				t.Fatalf("create: HTTP %d: %s, %v", response.Code, response.Body.String(), err)
			}
			if (result.Machine.Desired == "running") == explicitStopped || result.Machine.Reserved == explicitStopped {
				t.Fatalf("creation start default ignored: %+v", result.Machine)
			}
		})
	}
}

func TestGatewayPreservesEscapedRequestIdentity(t *testing.T) {
	service, spec := operationService(t)
	requestID := "opaque/request?with#encoding"
	created, err := service.Create(context.Background(), compute.CreateRequest{RequestID: requestID, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "axv-request-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: Handler(service.Service)}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	gateway := computegateway.NewGateway(socket, computegateway.GatewayOptions{})
	defer gateway.Close()
	request := httptest.NewRequest(http.MethodGet, "/api/compute/operations/by-request/"+url.PathEscape(requestID), nil)
	request.Host, request.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	var op compute.Operation
	if err := json.Unmarshal(response.Body.Bytes(), &op); err != nil || response.Code != http.StatusOK || op.ID != created.Operation.ID {
		t.Fatalf("gateway changed request identity: HTTP %d: %s, %v", response.Code, response.Body.String(), err)
	}
}

func TestInfoExposesTypedCreationDefaultsWithoutPrivateImagePaths(t *testing.T) {
	service, _ := operationService(t)
	response := httptest.NewRecorder()
	Handler(service.Service).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/info", nil))
	var info compute.Info
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.ProtocolVersion != compute.ProtocolVersion || info.Defaults.Spec.Resources.CPUs != 2 || info.Defaults.Spec.Resources.DiskGiB != 8 || !info.Defaults.Start || info.Defaults.Spec.ImageID != "" || len(info.Images) != 1 {
		t.Fatalf("invalid typed host defaults: %+v", info)
	}
}

func TestPackageSourceHTTPUsesDurableActionWithoutGuestReadiness(t *testing.T) {
	service, spec := operationService(t)
	ctx := context.Background()
	created, err := service.Create(ctx, compute.CreateRequest{RequestID: "http-package-create", Spec: spec, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	op, found, err := service.Store.Claim(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err = service.Store.Finish(ctx, op, compute.Observation{Status: "running", Ready: false, GuestState: "not-installed"}, nil); err != nil {
		t.Fatal(err)
	}
	machine, err := service.Store.Machine(ctx, created.Machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(service.Service)
	call := func(req compute.ActionRequest) *httptest.ResponseRecorder {
		body, _ := json.Marshal(req)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/machines/"+machine.ID+"/actions", strings.NewReader(string(body))))
		return response
	}
	req := compute.ActionRequest{RequestID: "http-package-change", Revision: machine.Revision, Action: "package-source", PackageSource: "aliyun"}
	first := call(req)
	if first.Code != http.StatusAccepted {
		t.Fatal(first.Code, first.Body.String())
	}
	var accepted compute.Result
	if err = json.Unmarshal(first.Body.Bytes(), &accepted); err != nil || accepted.Operation.PackageSource != "aliyun" {
		t.Fatal(first.Body.String(), err)
	}
	replay := call(req)
	if replay.Code != http.StatusAccepted || !strings.Contains(replay.Body.String(), accepted.Operation.ID) {
		t.Fatal("replay failed", replay.Body.String())
	}
	req.PackageSource = "official"
	changed := call(req)
	if changed.Code != http.StatusConflict {
		t.Fatal("changed request replay accepted", changed.Body.String())
	}
	req.RequestID = "http-package-invalid"
	req.PackageSource = "arbitrary"
	req.Revision = accepted.Machine.Revision
	invalid := call(req)
	if invalid.Code != http.StatusBadRequest {
		t.Fatal("unknown source accepted", invalid.Body.String())
	}
}
