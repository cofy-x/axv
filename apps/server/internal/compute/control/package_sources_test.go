package control

import (
	"context"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type packageDriver struct {
	*lifecycleDriver
	sources []string
}

func (d *packageDriver) ConfigurePackageSource(_ context.Context, _ compute.Machine, source string) error {
	d.sources = append(d.sources, source)
	return nil
}

func TestPackageSourcesAreExplicitSerializedAndReplayBound(t *testing.T) {
	s, base, _ := guestFixture(t, nil)
	d := &packageDriver{lifecycleDriver: base}
	s.drivers["lima"] = d
	m := runClaimed(t, s)
	if len(d.sources) != 0 {
		t.Fatal("Start configured sources")
	}
	ctx := context.Background()
	req := compute.ActionRequest{RequestID: "explicit-package-source", Revision: m.Revision, Action: "package-source", PackageSource: "aliyun"}
	accepted, err := s.store.Action(ctx, s.config.Capacity, m.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Operation.PackageSource != "aliyun" {
		t.Fatal("choice not persisted")
	}
	if _, err = s.store.Action(ctx, s.config.Capacity, m.ID, compute.ActionRequest{RequestID: "concurrent-stop-request", Revision: accepted.Machine.Revision, Action: "stop"}); compute.ErrorCode(err) != "operation_conflict" {
		t.Fatal("maintenance not locked", err)
	}
	m = runClaimed(t, s)
	if m.Status != "running" || m.Ready || len(d.sources) != 1 || d.sources[0] != "aliyun" || d.guestCalls != 0 {
		t.Fatalf("basic maintenance used guest: %+v %+v", m, d)
	}
	again, err := s.store.Action(ctx, s.config.Capacity, m.ID, req)
	if err != nil || again.Operation.ID != accepted.Operation.ID || again.Operation.Status != "succeeded" {
		t.Fatal("lost replay", err)
	}
	req.PackageSource = "official"
	if _, err = s.store.Action(ctx, s.config.Capacity, m.ID, req); compute.ErrorCode(err) != "idempotency_conflict" {
		t.Fatal("choice changed on replay", err)
	}
	if _, err = s.store.Action(ctx, s.config.Capacity, m.ID, compute.ActionRequest{RequestID: "restart-with-invalid-source", Revision: m.Revision, Action: "restart", PackageSource: "aliyun"}); compute.ErrorCode(err) != "invalid_request" {
		t.Fatal("Start accepted source change", err)
	}
	if _, err = s.store.Action(ctx, s.config.Capacity, m.ID, compute.ActionRequest{RequestID: "stop-before-package-source", Revision: m.Revision, Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	m = runClaimed(t, s)
	if _, err = s.store.Action(ctx, s.config.Capacity, m.ID, compute.ActionRequest{RequestID: "stopped-package-source", Revision: m.Revision, Action: "package-source", PackageSource: "official"}); compute.ErrorCode(err) != "unavailable" {
		t.Fatal("stopped VM mutated", err)
	}
}
