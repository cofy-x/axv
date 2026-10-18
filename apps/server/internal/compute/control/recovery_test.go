package control

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestCreateRechecksAnImageRetiredAfterTheCatalogSnapshot(t *testing.T) {
	s, c, m := configurationFixture(t)
	ctx := context.Background()
	spare := c.Images[0]
	spare.ID = "retired-image"
	c.Images = append(c.Images, spare)
	if err := s.SeedImages(ctx, c.Images); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteImage(ctx, spare.ID, "retire-before-create", 1); err != nil {
		t.Fatal(err)
	}
	spec := m.Spec
	spec.Name = "Retired image VM"
	spec.ImageID, spec.VolumeID = spare.ID, ""
	_, err := s.Create(ctx, c, compute.CreateRequest{RequestID: "create-from-stale-catalog", Spec: spec})
	if compute.ErrorCode(err) != "image_conflict" {
		t.Fatalf("stale catalog admitted a deleting image: %v", err)
	}
	machines, _ := s.Machines(ctx)
	if len(machines) != 1 {
		t.Fatal("rejected creation left a VM or reservation")
	}
}

func TestCompletedOperationCannotOverwriteLaterConfiguration(t *testing.T) {
	s, c, m := configurationFixture(t)
	ctx := context.Background()
	var first operationRecord
	for index, name := range []string{"First edit", "Second edit"} {
		spec := m.Spec
		spec.Name = name
		result, err := s.Update(ctx, c, m.ID, compute.UpdateRequest{RequestID: fmt.Sprintf("configuration-%d", index), Revision: m.Revision, Spec: spec})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Finish(ctx, operationRecord{Operation: result.Operation}, compute.Observation{Status: "stopped"}, nil); compute.ErrorCode(err) != "operation_conflict" {
			t.Fatalf("unclaimed operation completed: %v", err)
		}
		op, _, err := s.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			first = op
		}
		if err = s.Finish(ctx, op, compute.Observation{Status: "stopped"}, nil); err != nil {
			t.Fatal(err)
		}
		m, _ = s.Machine(ctx, m.ID)
	}
	before := fingerprint(m)
	if err := s.Finish(ctx, first, compute.Observation{Status: "running", Ready: true}, fmt.Errorf("late result")); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Machine(ctx, m.ID)
	if before != fingerprint(after) || after.Spec.Name != "Second edit" {
		t.Fatal("repeated completion reverted newer configuration or state")
	}
}

func TestUncertainAttachmentProtectsBothOwnersUntilExplicitRetry(t *testing.T) {
	s, c, m := configurationFixture(t)
	ctx := context.Background()
	v, _ := s.Volume(ctx, m.Spec.VolumeID)
	if _, err := s.VolumeAction(ctx, c, v.ID, compute.VolumeRequest{RequestID: "detach-for-attachment", Revision: v.Revision, Action: "detach", MachineRevision: m.Revision}); err != nil {
		t.Fatal(err)
	}
	op, _, _ := s.Claim(ctx)
	target := v
	target.MachineID = ""
	if err := s.Finish(ctx, op, compute.Observation{Volume: &target}, nil); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Machine(ctx, m.ID)
	v, _ = s.Volume(ctx, v.ID)
	req := compute.VolumeRequest{RequestID: "uncertain-attachment", Revision: v.Revision, Action: "attach", MachineID: m.ID, MachineRevision: m.Revision}
	if _, err := s.VolumeAction(ctx, c, v.ID, req); err != nil {
		t.Fatal(err)
	}
	op, _, _ = s.Claim(ctx)
	if err := s.Finish(ctx, op, compute.Observation{}, fmt.Errorf("provider response lost after edit")); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Volume(ctx, v.ID)
	if _, err := s.DeleteVolume(ctx, v.ID, "delete-uncertain-volume", v.Revision); compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("uncertain attachment lost its volume: %v", err)
	}
	if _, err := s.Action(ctx, c.Capacity, m.ID, compute.ActionRequest{RequestID: "delete-uncertain-vm", Revision: m.Revision, Action: "delete"}); compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("uncertain attachment lost its VM: %v", err)
	}
	req.RequestID, req.Revision = "retry-attachment", v.Revision
	if _, err := s.VolumeAction(ctx, c, v.ID, req); err != nil {
		t.Fatal(err)
	}
	op, _, _ = s.Claim(ctx)
	target = v
	target.MachineID = m.ID
	if err := s.Finish(ctx, op, compute.Observation{Volume: &target}, nil); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Machine(ctx, m.ID)
	v, _ = s.Volume(ctx, v.ID)
	if m.Spec.VolumeID != v.ID || v.PendingMachineID != "" || v.Status != "ready" {
		t.Fatalf("explicit retry did not resolve both owners: %+v %+v", m, v)
	}
}

func TestActivityKeepsOldActiveOperationsBeyondHistoryLimit(t *testing.T) {
	s, _, _ := configurationFixture(t)
	ctx := context.Background()
	active := newOperation("old-active-request", "", "import-image")
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := insertOperation(ctx, tx, active, "active", "old-active-image"); err != nil {
			return err
		}
		for index := 0; index < 205; index++ {
			op := newOperation(fmt.Sprintf("history-request-%d", index), "", "import-image")
			op.Status = "succeeded"
			if err := insertOperation(ctx, tx, op, "done", fmt.Sprintf("history-image-%d", index)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ops, err := s.Operations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range ops {
		found = found || op.ID == active.ID
	}
	if !found || len(ops) != 201 {
		t.Fatalf("history pagination hid an active resource lock: %d records, found=%t", len(ops), found)
	}
	old, err := s.OperationByRequestID(ctx, "initial-create")
	if err != nil || old.Status != "succeeded" {
		t.Fatalf("request recovery lost an operation outside recent activity: %+v, %v", old, err)
	}
	if _, err := s.OperationByRequestID(ctx, "unknown-request"); compute.ErrorCode(err) != "not_found" {
		t.Fatalf("unknown request did not remain unknown: %v", err)
	}
}
