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
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func configurationFixture(t *testing.T) (*Store, compute.Config, compute.Machine) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	c := compute.Config{Capacity: compute.Capacity{Resources: compute.Resources{CPUs: 4, MemoryMiB: 4096, DiskGiB: 20}, Machines: 4, Workers: 1}, Images: []compute.Image{{ID: "test-image", Provider: "lima", Arch: runtime.GOARCH, Guest: &guestimage.Contract{ImageVersion: 1, ProtocolVersion: guestproto.Version, Capabilities: guestimage.Capabilities()}}}}
	if err = s.SeedImages(context.Background(), c.Images); err != nil {
		t.Fatal(err)
	}
	result, err := s.Create(context.Background(), c, compute.CreateRequest{RequestID: "initial-create", Spec: compute.Spec{Name: "Development", Provider: "lima", ImageID: "test-image", Kind: "development", Resources: compute.Resources{CPUs: 1, MemoryMiB: 1024, DiskGiB: 8}, ProjectGiB: 1, RetainVolume: true}})
	if err != nil {
		t.Fatal(err)
	}
	operation, found, err := s.Claim(context.Background())
	if err != nil || !found || operation.ID != result.Operation.ID {
		t.Fatalf("claim: %v %v", found, err)
	}
	if err = s.Finish(context.Background(), operation, compute.Observation{Status: "stopped"}, nil); err != nil {
		t.Fatal(err)
	}
	m, err := s.Machine(context.Background(), result.Machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, m
}

func TestUncertainConfigurationRetainsCapacityAndRevisionBoundary(t *testing.T) {
	s, c, m := configurationFixture(t)
	ctx := context.Background()
	target := m.Spec
	target.Name, target.Resources.DiskGiB = "Expanded", 18
	req := compute.UpdateRequest{RequestID: "grow-request", Revision: m.Revision, Spec: target}
	result, err := s.Update(ctx, c, m.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Machine.Spec.Name != m.Spec.Name || result.Machine.PendingSpec == nil {
		t.Fatal("unapplied configuration replaced the applied specification")
	}
	op, _, err := s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(ctx, op, compute.Observation{Status: "stopped"}, fmt.Errorf("growth was interrupted")); err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Update(ctx, c, m.ID, req)
	if err != nil || duplicate.Operation.ID != result.Operation.ID || duplicate.Operation.Status != "failed" {
		t.Fatalf("uncertain request was not replayed: %+v %v", duplicate, err)
	}
	_, err = s.Create(ctx, c, compute.CreateRequest{RequestID: "capacity-after-growth", Spec: compute.Spec{Name: "Other", Provider: "lima", ImageID: "test-image", Kind: "development", Resources: compute.Resources{CPUs: 1, MemoryMiB: 1024, DiskGiB: 2}, ProjectGiB: 1, RetainVolume: true}})
	if compute.ErrorCode(err) != "capacity_exceeded" {
		t.Fatalf("uncertain disk growth released its reservation: %v", err)
	}
	pending, _ := s.Machine(ctx, m.ID)
	_, err = s.Action(ctx, c.Capacity, m.ID, compute.ActionRequest{RequestID: "start-pending", Revision: pending.Revision, Action: "start"})
	if compute.ErrorCode(err) != "configuration_conflict" {
		t.Fatalf("pending VM started: %v", err)
	}
	_, err = s.Update(ctx, c, m.ID, compute.UpdateRequest{RequestID: "shrink-pending", Revision: pending.Revision, Spec: m.Spec})
	if compute.ErrorCode(err) != "invalid_request" {
		t.Fatalf("uncertain growth was shrunk: %v", err)
	}
	result, err = s.Update(ctx, c, m.ID, compute.UpdateRequest{RequestID: "retry-growth", Revision: pending.Revision, Spec: target})
	if err != nil {
		t.Fatal(err)
	}
	op, _, _ = s.Claim(ctx)
	if err = s.Finish(ctx, op, compute.Observation{Status: "stopped"}, nil); err != nil {
		t.Fatal(err)
	}
	applied, _ := s.Machine(ctx, m.ID)
	if applied.PendingSpec != nil || applied.Spec.Resources.DiskGiB != 18 || applied.Spec.Name != "Expanded" {
		t.Fatalf("configuration was not applied: %+v", applied)
	}
	_, err = s.Update(ctx, c, m.ID, compute.UpdateRequest{RequestID: "stale-after-apply", Revision: result.Machine.Revision, Spec: target})
	if compute.ErrorCode(err) != "revision_conflict" {
		t.Fatalf("a view of the old applied settings remained current after cutover: %v", err)
	}
}

type configurationDriver struct {
	address, machineID string
	stops              int
}

func (d *configurationDriver) Probe(context.Context) compute.Capability {
	return compute.Capability{Available: true}
}
func (d *configurationDriver) Ensure(context.Context, compute.Machine, compute.Image, compute.Volume) error {
	return nil
}
func (d *configurationDriver) Start(context.Context, compute.Machine, compute.Image, compute.Volume) error {
	return nil
}
func (d *configurationDriver) Stop(context.Context, compute.Machine) error        { d.stops++; return nil }
func (d *configurationDriver) Delete(context.Context, compute.Machine) error      { return nil }
func (d *configurationDriver) DeleteVolume(context.Context, compute.Volume) error { return nil }
func (d *configurationDriver) Inspect(context.Context, compute.Machine) (compute.Observation, error) {
	return compute.Observation{Status: "running"}, nil
}
func (d *configurationDriver) DialGuest(ctx context.Context, _ compute.Machine) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "tcp", d.address)
}
func (d *configurationDriver) GuestToken(compute.Machine) (string, error) { return "test-token", nil }

func TestInterruptedRestartDoesNotStopTheAlreadyRestartedGuest(t *testing.T) {
	s, c, m := configurationFixture(t)
	ctx := context.Background()
	result, err := s.Action(ctx, c.Capacity, m.ID, compute.ActionRequest{RequestID: "restart-once", Revision: m.Revision, Action: "restart"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = s.Claim(ctx)
	if err = s.Phase(ctx, result.Operation.ID, "starting"); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	op, found, err := s.Claim(ctx)
	if err != nil || !found || op.Phase != "starting" {
		t.Fatalf("restart phase was lost: %+v %v", op, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"version": guestproto.Version, "machineId": m.ID, "bootId": "new-boot", "ready": true})
	}))
	defer server.Close()
	d := &configurationDriver{address: strings.TrimPrefix(server.URL, "http://"), machineID: m.ID}
	service := &Service{store: s, config: c, drivers: map[string]compute.Driver{"lima": d}}
	observed, err := service.perform(ctx, op)
	if err != nil || !observed.Ready || d.stops != 0 {
		t.Fatalf("resumed restart stopped live work again: %+v %v, stops=%d", observed, err, d.stops)
	}
}
