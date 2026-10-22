package control

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type pendingCopyDriver struct {
	*projectCopyDriver
	copyVolume   func(context.Context, compute.Volume, compute.Volume) error
	deleteVolume func(context.Context, compute.Volume) error
}

func (d *pendingCopyDriver) CopyVolume(ctx context.Context, source, target compute.Volume) error {
	d.copies++
	return d.copyVolume(ctx, source, target)
}

func (d *pendingCopyDriver) DeleteVolume(ctx context.Context, target compute.Volume) error {
	return d.deleteVolume(ctx, target)
}

func pendingCopyFixture(t *testing.T) (*projectFixture, compute.BackupProjectRequest, compute.VolumeResult, operationRecord, *pendingCopyDriver) {
	t.Helper()
	f, binding, base := backupFixture(t)
	req := stopBackupSource(t, f, binding)
	accepted, err := f.service.BackupProject(context.Background(), binding.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	op, found, err := f.service.store.Claim(context.Background())
	if err != nil || !found || op.ID != accepted.Operation.ID {
		t.Fatalf("claim pending copy: %+v %v", op, err)
	}
	driver := &pendingCopyDriver{projectCopyDriver: base}
	f.service.drivers[accepted.Volume.Provider] = driver
	return f, req, accepted, op, driver
}

func TestPendingCopyKeepsOriginalReceiptReservationsAndLocksAcrossWorkDeadlines(t *testing.T) {
	f, req, accepted, op, driver := pendingCopyFixture(t)
	ctx := context.Background()
	driver.copyVolume = func(work context.Context, source, target compute.Volume) error {
		current, err := f.service.store.Operation(ctx, op.ID)
		if err != nil || current.Status != "running" || current.RequestID != accepted.Operation.RequestID || source.ID == target.ID || target.ID != accepted.Volume.ID {
			t.Fatalf("pending copy changed its accepted identity: %+v %v", current, err)
		}
		volume, err := f.service.store.Volume(ctx, target.ID)
		if err != nil || volume.Status != "copying" || volume.TransientGiB != source.SizeGiB {
			t.Fatalf("pending copy lost its staging reservation: %+v %v", volume, err)
		}
		if _, err := f.service.Action(ctx, req.MachineID, compute.ActionRequest{RequestID: fmt.Sprintf("start-pending-copy-%d", driver.copies), Revision: req.MachineRevision, Action: "start"}); compute.ErrorCode(err) != "operation_conflict" {
			t.Fatalf("pending copy released its source VM lock: %v", err)
		}
		if driver.copies <= 2 {
			<-work.Done()
			return compute.Problem("provider_pending", "the previous confirmed writer still holds the copy lock")
		}
		return nil
	}
	observed, err := f.service.performOperation(ctx, op, 50*time.Millisecond, time.Millisecond)
	if err != nil || driver.copies != 3 {
		t.Fatalf("copy did not survive multiple work deadlines: calls=%d error=%v", driver.copies, err)
	}
	if err := f.service.store.Finish(ctx, op, observed, nil); err != nil {
		t.Fatal(err)
	}
	completed, err := f.service.Operation(ctx, op.ID)
	if err != nil || completed.Status != "succeeded" || completed.RequestID != accepted.Operation.RequestID {
		t.Fatalf("pending completion replaced its original receipt: %+v %v", completed, err)
	}
	var count int
	if err := f.service.store.db.QueryRowContext(ctx, "SELECT count(*) FROM operations WHERE request_id=?", accepted.Operation.RequestID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pending recovery created another operation: count=%d %v", count, err)
	}
}

func TestPendingCopyHostShutdownPreservesRunningIntentForRecovery(t *testing.T) {
	f, _, accepted, op, driver := pendingCopyFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	driver.copyVolume = func(work context.Context, _, _ compute.Volume) error {
		cancel()
		<-work.Done()
		return compute.Problem("provider_pending", "previous writer still owns the copy lock")
	}
	if _, err := f.service.performOperation(ctx, op, time.Second, time.Millisecond); compute.ErrorCode(err) != "provider_pending" || driver.copies != 1 {
		t.Fatalf("host shutdown replayed the copy: calls=%d %v", driver.copies, err)
	}
	current, err := f.service.store.Operation(context.Background(), op.ID)
	if err != nil || current.Status != "running" {
		t.Fatalf("shutdown finalized a pending operation: %+v %v", current, err)
	}
	if err := f.service.store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := f.service.store.Claim(context.Background())
	if err != nil || !found || recovered.ID != accepted.Operation.ID {
		t.Fatalf("shutdown recovery lost its original receipt: %+v %v", recovered, err)
	}
}

func TestProviderPendingDoesNotReplayOtherActionsOrUnconfirmedCopyErrors(t *testing.T) {
	for _, otherAction := range []bool{false, true} {
		t.Run(fmt.Sprintf("other-action-%t", otherAction), func(t *testing.T) {
			f, _, _, op, driver := pendingCopyFixture(t)
			failure := errors.New("unconfirmed disk copy failure")
			driver.copyVolume = func(context.Context, compute.Volume, compute.Volume) error { return failure }
			deletes := 0
			driver.deleteVolume = func(context.Context, compute.Volume) error {
				deletes++
				return compute.Problem("provider_pending", "a writer still owns the target")
			}
			if otherAction {
				op.Action = "delete-volume"
			}
			_, err := f.service.performOperation(context.Background(), op, time.Second, time.Millisecond)
			if otherAction {
				if compute.ErrorCode(err) != "provider_pending" || deletes != 1 {
					t.Fatalf("a different provider action was replayed: deletes=%d %v", deletes, err)
				}
			} else if !errors.Is(err, failure) || driver.copies != 1 {
				t.Fatalf("an unconfirmed copy failure was replayed: copies=%d %v", driver.copies, err)
			}
		})
	}
}
