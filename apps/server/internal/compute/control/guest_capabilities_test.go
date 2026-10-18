package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

type lifecycleDriver struct {
	configurationDriver
	status              string
	guestCalls          int
	initializationError error
}

func (d *lifecycleDriver) Start(context.Context, compute.Machine, compute.Image, compute.Volume) error {
	d.status = "running"
	return nil
}
func (d *lifecycleDriver) Stop(context.Context, compute.Machine) error {
	d.status = "stopped"
	return nil
}
func (d *lifecycleDriver) Inspect(context.Context, compute.Machine) (compute.Observation, error) {
	return compute.Observation{Status: d.status}, nil
}
func (d *lifecycleDriver) DialGuest(ctx context.Context, m compute.Machine) (net.Conn, error) {
	d.guestCalls++
	return d.configurationDriver.DialGuest(ctx, m)
}
func (d *lifecycleDriver) PrepareGuest(context.Context, compute.Machine, compute.Image, compute.Volume) error {
	return d.initializationError
}

func guestFixture(t *testing.T, contract *guestimage.Contract) (*Service, *lifecycleDriver, compute.Result) {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	config := compute.Config{Capacity: compute.Capacity{Resources: compute.Resources{CPUs: 4, MemoryMiB: 4096, DiskGiB: 32}, Machines: 4, Workers: 1}, Images: []compute.Image{{ID: "fixture-image", Provider: "lima", Arch: runtime.GOARCH, Guest: contract}}}
	if err = store.SeedImages(context.Background(), config.Images); err != nil {
		t.Fatal(err)
	}
	d := &lifecycleDriver{status: "stopped"}
	service := &Service{store: store, config: config, drivers: map[string]compute.Driver{"lima": d}}
	result, err := service.Create(context.Background(), compute.CreateRequest{RequestID: "guest-fixture-create", Spec: compute.Spec{Name: "Test VM", Provider: "lima", ImageID: "fixture-image", Kind: "development", Resources: compute.Resources{CPUs: 1, MemoryMiB: 512, DiskGiB: 8}, ProjectGiB: 1, RetainVolume: true}, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	return service, d, result
}
func runClaimed(t *testing.T, s *Service) compute.Machine {
	t.Helper()
	ctx := context.Background()
	op, found, err := s.store.Claim(ctx)
	if err != nil || !found {
		t.Fatalf("claim %v %v", found, err)
	}
	observed, err := s.perform(ctx, op)
	if err != nil {
		t.Fatalf("%s failed: %v", op.Action, err)
	}
	if err = s.store.Finish(ctx, op, observed, nil); err != nil {
		t.Fatal(err)
	}
	m, err := s.store.Machine(ctx, op.MachineID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func TestBasicLifecycleDoesNotDependOnGuest(t *testing.T) {
	for _, contract := range []*guestimage.Contract{nil, {ImageVersion: 1, ProtocolVersion: guestproto.Version + 1, Capabilities: []string{"exec"}}} {
		name := "plain"
		if contract != nil {
			name = "incompatible"
		}
		t.Run(name, func(t *testing.T) {
			service, d, _ := guestFixture(t, contract)
			m := runClaimed(t, service)
			if m.Status != "running" || m.Ready {
				t.Fatalf("basic start: %+v", m)
			}
			for i, action := range []string{"stop", "start", "restart", "delete"} {
				if _, err := service.store.Action(context.Background(), service.config.Capacity, m.ID, compute.ActionRequest{RequestID: fmt.Sprintf("lifecycle-%d", i), Revision: m.Revision, Action: action}); err != nil {
					t.Fatal(err)
				}
				m = runClaimed(t, service)
			}
			if m.Status != "deleted" || d.guestCalls != 0 {
				t.Fatalf("guest was used for basic lifecycle: calls=%d machine=%+v", d.guestCalls, m)
			}
		})
	}
}
func TestRuntimeCapabilitiesAreAnIntersectionAndFailureDoesNotFailVM(t *testing.T) {
	contract := &guestimage.Contract{ImageVersion: 1, ProtocolVersion: guestproto.Version, Capabilities: []string{"exec", "terminals"}}
	service, d, result := guestFixture(t, contract)
	var protocol atomic.Int32
	protocol.Store(guestproto.Version)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(guestproto.Health{Version: int(protocol.Load()), MachineID: result.Machine.ID, Ready: true, Capabilities: []string{"exec", "ssh"}})
	}))
	defer server.Close()
	d.address = strings.TrimPrefix(server.URL, "http://")
	m := runClaimed(t, service)
	if !m.Ready || len(m.GuestCapabilities) != 1 || m.GuestCapabilities[0] != "exec" {
		t.Fatalf("unconfirmed capability published: %+v", m)
	}
	if err := service.RequireGuest(context.Background(), m, "exec"); err != nil {
		t.Fatal(err)
	}
	for _, capability := range []string{"ssh", "terminals"} {
		if err := service.RequireGuest(context.Background(), m, capability); compute.ErrorCode(err) != "unsupported" {
			t.Fatalf("%s available: %v", capability, err)
		}
	}
	protocol.Store(guestproto.Version + 1)
	var incompatible compute.Observation
	service.observeGuest(context.Background(), m, d, &incompatible)
	if incompatible.Ready || incompatible.GuestState != "incompatible" || !strings.Contains(incompatible.GuestReason, "protocol") {
		t.Fatalf("runtime protocol mismatch lost its diagnosis: %+v", incompatible)
	}
	server.Close()
	d.initializationError = fmt.Errorf("missing preinstalled relay; rebuild image")
	if _, err := service.store.Action(context.Background(), service.config.Capacity, m.ID, compute.ActionRequest{RequestID: "start-with-broken-guest", Revision: m.Revision, Action: "restart"}); err != nil {
		t.Fatal(err)
	}
	m = runClaimed(t, service)
	if m.Status != "running" || m.Ready || !strings.Contains(m.GuestReason, "missing preinstalled relay") {
		t.Fatalf("guest failure corrupted lifecycle or lost diagnosis: %+v", m)
	}
}
