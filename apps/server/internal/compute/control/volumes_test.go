package control

import (
	"context"
	"fmt"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestVolumeCopyOwnsItsStoppedSourceVM(t *testing.T) {
	s, c, m := configurationFixture(t)
	ctx := context.Background()
	result, err := s.CreateVolume(ctx, c, compute.CreateVolumeRequest{RequestID: "copy-source", Name: "Independent copy", SourceVolumeID: m.Spec.VolumeID, Retain: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Action(ctx, c.Capacity, m.ID, compute.ActionRequest{RequestID: "start-during-copy", Revision: m.Revision, Action: "start"})
	if compute.ErrorCode(err) != "operation_conflict" {
		t.Fatalf("source VM started during its disk copy: %v", err)
	}
	op, found, err := s.Claim(ctx)
	if err != nil || !found || op.ID != result.Operation.ID {
		t.Fatalf("claim: %v %v", found, err)
	}
	if err = s.Finish(ctx, op, compute.Observation{}, fmt.Errorf("copy interrupted")); err != nil {
		t.Fatal(err)
	}
	failed, _ := s.Volume(ctx, result.Volume.ID)
	if failed.Status != "failed" || failed.TransientGiB == 0 {
		t.Fatalf("incomplete copy lost its temporary allocation: %+v", failed)
	}
	_, err = s.VolumeAction(ctx, c, failed.ID, compute.VolumeRequest{RequestID: "attach-failed-copy", Revision: failed.Revision, Action: "attach", MachineID: m.ID, MachineRevision: m.Revision})
	if compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("incomplete copy became attachable: %v", err)
	}
	if _, err = s.Action(ctx, c.Capacity, m.ID, compute.ActionRequest{RequestID: "start-after-copy-failure", Revision: m.Revision, Action: "start"}); err != nil {
		t.Fatal("copy failure held the healthy source hostage", err)
	}
}

func TestDetachingProjectStorageDoesNotPermitUnbackedStartup(t *testing.T) {
	s, c, m := configurationFixture(t)
	ctx := context.Background()
	v, _ := s.Volume(ctx, m.Spec.VolumeID)
	result, err := s.VolumeAction(ctx, c, v.ID, compute.VolumeRequest{RequestID: "detach-project", Revision: v.Revision, Action: "detach", MachineRevision: m.Revision})
	if err != nil {
		t.Fatal(err)
	}
	op, _, _ := s.Claim(ctx)
	target := v
	target.MachineID = ""
	if err = s.Finish(ctx, op, compute.Observation{Volume: &target}, nil); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Machine(ctx, m.ID)
	v, _ = s.Volume(ctx, v.ID)
	if m.Spec.VolumeID != "" || v.MachineID != "" || v.Status != "ready" {
		t.Fatalf("detach did not commit both owners: %+v %+v", m, v)
	}
	_, err = s.Action(ctx, c.Capacity, m.ID, compute.ActionRequest{RequestID: "start-without-project", Revision: m.Revision, Action: "start"})
	if compute.ErrorCode(err) != "volume_conflict" {
		t.Fatalf("VM started without its required project filesystem: %v", err)
	}
	if _, err = s.VolumeAction(ctx, c, v.ID, compute.VolumeRequest{RequestID: "stale-volume-attach", Revision: result.Volume.Revision, Action: "attach", MachineID: m.ID, MachineRevision: m.Revision}); compute.ErrorCode(err) != "revision_conflict" {
		t.Fatalf("stale attachment edit was accepted: %v", err)
	}
}
