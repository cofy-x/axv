package control

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func nameFixture(t *testing.T) (*Store, compute.Config, compute.Machine) {
	t.Helper()
	s, c, m := configurationFixture(t)
	c.Capacity = compute.Capacity{Resources: compute.Resources{CPUs: 100, MemoryMiB: 100 << 10, DiskGiB: 1000}, Machines: 100, Workers: 1}
	return s, c, m
}

func createNamed(ctx context.Context, s *Store, c compute.Config, base compute.Machine, requestID, name string) (compute.Result, error) {
	spec := base.Spec
	spec.Name, spec.VolumeID = name, ""
	return s.Create(ctx, c, compute.CreateRequest{RequestID: requestID, Spec: spec})
}

func TestMachineNamesNormalizeBeforeIdempotencyAndKeepExactCase(t *testing.T) {
	s, c, m := nameFixture(t)
	ctx := context.Background()
	created, err := createNamed(ctx, s, c, m, "name-with-space", " \t工作 VM\u2003")
	if err != nil || created.Machine.Spec.Name != "工作 VM" {
		t.Fatalf("normalized create: %+v, %v", created, err)
	}
	replayed, err := createNamed(ctx, s, c, m, "name-with-space", "工作 VM")
	if err != nil || replayed.Operation.ID != created.Operation.ID {
		t.Fatalf("normalized replay changed intent: %+v, %v", replayed, err)
	}
	if _, err = createNamed(ctx, s, c, m, "same-name-new-request", "工作 VM"); compute.ErrorCode(err) != "name_conflict" {
		t.Fatalf("duplicate name accepted: %v", err)
	}
	op, _, _ := s.Claim(ctx)
	if err := s.Finish(ctx, op, compute.Observation{Status: "stopped"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = createNamed(ctx, s, c, m, "case-sensitive-name", "development"); err != nil {
		t.Fatalf("case-distinct name rejected: %v", err)
	}
	for index, invalid := range []string{"", " \t ", "bad\nname", "bad\x00name", strings.Repeat("界", 27), compute.NewID("vm")} {
		if _, err = createNamed(ctx, s, c, m, fmt.Sprintf("invalid-name-%d", index), invalid); compute.ErrorCode(err) != "invalid_request" {
			t.Errorf("invalid name %q accepted: %v", invalid, err)
		}
	}
	if !compute.IsMachineID(m.ID) || compute.IsMachineID("vm-short") || compute.IsMachineID("vm-"+strings.Repeat("A", 32)) {
		t.Fatal("machine ID shape no longer matches generated immutable IDs")
	}
}

func TestConcurrentMachineNameClaimsHaveOneWinner(t *testing.T) {
	s, c, m := nameFixture(t)
	ctx := context.Background()
	const writers = 12
	var wg sync.WaitGroup
	errors := make(chan error, writers)
	start := make(chan struct{})
	for index := 0; index < writers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := createNamed(ctx, s, c, m, fmt.Sprintf("concurrent-name-%d", index), "Shared VM")
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	winners := 0
	for err := range errors {
		if err == nil {
			winners++
		} else if compute.ErrorCode(err) != "name_conflict" {
			t.Errorf("unexpected concurrent result: %v", err)
		}
	}
	machines, _ := s.Machines(ctx)
	volumes, _ := s.Volumes(ctx)
	operations, _ := s.Operations(ctx)
	if winners != 1 || len(machines) != 2 || len(volumes) != 2 || len(operations) != 2 {
		t.Fatalf("name admission left extra resources: winners=%d, machines=%d, volumes=%d, operations=%d", winners, len(machines), len(volumes), len(operations))
	}
}

func TestCreateAndRenameCompeteForTheSameNameClaim(t *testing.T) {
	s, c, m := nameFixture(t)
	ctx := context.Background()
	target := m.Spec
	target.Name = "Contested name"
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := s.Update(ctx, c, m.ID, compute.UpdateRequest{RequestID: "competing-rename", Revision: m.Revision, Spec: target})
		results <- err
	}()
	go func() {
		<-start
		_, err := createNamed(ctx, s, c, m, "competing-create", target.Name)
		results <- err
	}()
	close(start)
	winners := 0
	for range 2 {
		if err := <-results; err == nil {
			winners++
		} else if compute.ErrorCode(err) != "name_conflict" {
			t.Errorf("unexpected name-claim result: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("create and rename both claimed one name: %d winners", winners)
	}
}

func TestPendingRenameClaimsCurrentAndTargetNamesUntilResolved(t *testing.T) {
	s, c, m := nameFixture(t)
	ctx := context.Background()
	target := m.Spec
	target.Name = " Renamed "
	req := compute.UpdateRequest{RequestID: "rename-request", Revision: m.Revision, Spec: target}
	accepted, err := s.Update(ctx, c, m.ID, req)
	if err != nil || accepted.Machine.PendingSpec.Name != "Renamed" {
		t.Fatalf("rename admission: %+v, %v", accepted, err)
	}
	req.Spec.Name = "Renamed"
	replayed, err := s.Update(ctx, c, m.ID, req)
	if err != nil || replayed.Operation.ID != accepted.Operation.ID {
		t.Fatalf("rename replay: %+v, %v", replayed, err)
	}
	for _, name := range []string{m.Spec.Name, "Renamed"} {
		if _, err := createNamed(ctx, s, c, m, "claim-pending-"+name, name); compute.ErrorCode(err) != "name_conflict" {
			t.Errorf("pending name %q was not reserved: %v", name, err)
		}
	}
	op, _, _ := s.Claim(ctx)
	if err := s.Finish(ctx, op, compute.Observation{Status: "stopped"}, fmt.Errorf("configuration interrupted")); err != nil {
		t.Fatal(err)
	}
	if _, err := createNamed(ctx, s, c, m, "claim-failed-rename", "Renamed"); compute.ErrorCode(err) != "name_conflict" {
		t.Fatalf("failed rename released its target: %v", err)
	}
	other, err := createNamed(ctx, s, c, m, "other-create-request", "Other")
	if err != nil {
		t.Fatal(err)
	}
	op, _, _ = s.Claim(ctx)
	if err := s.Finish(ctx, op, compute.Observation{Status: "stopped"}, nil); err != nil {
		t.Fatal(err)
	}
	other.Machine, _ = s.Machine(ctx, other.Machine.ID)
	otherTarget := other.Machine.Spec
	otherTarget.Name = "Renamed"
	if _, err := s.Update(ctx, c, other.Machine.ID, compute.UpdateRequest{RequestID: "rename-to-pending", Revision: other.Machine.Revision, Spec: otherTarget}); compute.ErrorCode(err) != "name_conflict" {
		t.Fatalf("another rename acquired a pending target: %v", err)
	}
	m, _ = s.Machine(ctx, m.ID)
	req.RequestID, req.Revision = "retry-rename", m.Revision
	if _, err = s.Update(ctx, c, m.ID, req); err != nil {
		t.Fatal(err)
	}
	op, _, _ = s.Claim(ctx)
	if err := s.Finish(ctx, op, compute.Observation{Status: "stopped"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := createNamed(ctx, s, c, m, "reuse-old-name", m.Spec.Name); err != nil {
		t.Fatalf("completed rename retained its old name: %v", err)
	}
}

func TestDeletionReleasesMachineNameOnlyAfterCompletion(t *testing.T) {
	s, c, m := nameFixture(t)
	ctx := context.Background()
	if _, err := s.Action(ctx, c.Capacity, m.ID, compute.ActionRequest{RequestID: "delete-named-vm", Revision: m.Revision, Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	if _, err := createNamed(ctx, s, c, m, "create-during-delete", m.Spec.Name); compute.ErrorCode(err) != "name_conflict" {
		t.Fatalf("deleting VM lost its name claim: %v", err)
	}
	op, _, _ := s.Claim(ctx)
	if err := s.Finish(ctx, op, compute.Observation{Status: "deleted"}, nil); err != nil {
		t.Fatal(err)
	}
	replacement, err := createNamed(ctx, s, c, m, "create-after-delete", m.Spec.Name)
	if err != nil || replacement.Machine.ID == m.ID {
		t.Fatalf("deleted name was not reusable under a new identity: %+v, %v", replacement, err)
	}
	old, err := s.Machine(ctx, m.ID)
	if err != nil || old.Status != "deleted" {
		t.Fatalf("old identity was replaced: %+v, %v", old, err)
	}
}

func TestComputeBaselineRejectsPriorSchemaWithoutReset(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	identity := s.HostID
	var version int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("baseline: %d, %v", version, err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if rejected, err := OpenStore(dir); err == nil {
		_ = rejected.Close()
		t.Fatal("prior baseline accepted")
	}
	db, err := sql.Open("sqlite", dir+"/compute.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var preserved string
	if err := db.QueryRow("SELECT id FROM identity").Scan(&preserved); err != nil || preserved != identity {
		t.Fatalf("rejected database identity was reset: %q, %v", preserved, err)
	}
}
