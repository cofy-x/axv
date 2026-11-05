package control

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func taskEvents(t *testing.T, f *taskFixture, after uint64) notificationevent.Batch {
	t.Helper()
	batch, err := f.service.NotificationEvents(t.Context(), after, f.bound.Binding.HostID)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestTaskNotificationsRecoverUnknownAndFinalOutcomeAcrossHostRestart(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "notification-private-request")
	f.reconcile(t, accepted.Task.ID)
	if batch := taskEvents(t, f, 0); len(batch.Events) != 0 || batch.Next != 0 {
		t.Fatalf("acceptance became an outcome: %+v", batch)
	}
	f.mu.Lock()
	f.offline = true
	f.mu.Unlock()
	f.reconcile(t, accepted.Task.ID)
	f.reconcile(t, accepted.Task.ID)
	unknown := taskEvents(t, f, 0)
	if len(unknown.Events) != 1 || unknown.Events[0].State != "unknown" || unknown.Gap || unknown.Next != 1 {
		t.Fatalf("uncertainty was lost or duplicated: %+v", unknown)
	}
	directory, hostID := f.service.store.directory, f.service.store.HostID
	if err := f.service.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	f.service.store = reopened
	recovered := taskEvents(t, f, 0)
	if recovered.StoreID != hostID || recovered.SourceID != "compute:"+hostID || len(recovered.Events) != 1 || recovered.Events[0].ID != unknown.Events[0].ID {
		t.Fatalf("host restart lost the original outcome identity: %+v", recovered)
	}
	f.mu.Lock()
	f.offline = false
	f.mu.Unlock()
	f.finish(accepted.Attempt.ID, []byte("private-command-output"))
	f.reconcile(t, accepted.Task.ID)
	f.reconcile(t, accepted.Task.ID)
	final := taskEvents(t, f, unknown.Next)
	if len(final.Events) != 1 || final.Events[0].State != "succeeded" || final.Next != 2 {
		t.Fatalf("verified final outcome was lost or duplicated: %+v", final)
	}
	target := final.Events[0].Target
	if target.Kind != "task" || target.StoreID != hostID || target.HostID != hostID || target.ProjectID != f.bound.Project.ID || target.TaskID != accepted.Task.ID || target.AttemptID != accepted.Attempt.ID || target.EventID != final.Events[0].ID {
		t.Fatalf("outcome omitted original ownership: %+v", target)
	}
	encoded, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-command-output", "notification-private-request", "printf", "rootIdentity", "reason", "binding"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("notification exposed private execution data %q: %s", private, encoded)
		}
	}
	resolution, err := f.service.ResolveNotification(t.Context(), target)
	if err != nil || !resolution.Available || !resolution.Current || resolution.State != "succeeded" || resolution.Target != target {
		t.Fatalf("original final attempt cannot be resolved: %+v %v", resolution, err)
	}
	if _, err := f.service.RetryProjectTask(t.Context(), f.bound.Project.ID, accepted.Task.ID, compute.RetryProjectTaskRequest{RequestID: "notification-retry", Binding: f.bound.Binding}); err != nil {
		t.Fatal(err)
	}
	resolution, err = f.service.ResolveNotification(t.Context(), target)
	if err != nil || !resolution.Available || resolution.Current || resolution.State != "succeeded" || resolution.Target.AttemptID != accepted.Attempt.ID {
		t.Fatalf("notification was redirected to a retry: %+v %v", resolution, err)
	}
	wrongProject := target
	wrongProject.ProjectID = "another-project"
	resolution, err = f.service.ResolveNotification(t.Context(), wrongProject)
	if err != nil || resolution.Available {
		t.Fatalf("notification resolved through another project: %+v %v", resolution, err)
	}
	wrongHost := target
	wrongHost.HostID = "another-host"
	if _, err := f.service.ResolveNotification(t.Context(), wrongHost); compute.ErrorCode(err) != "binding_changed" {
		t.Fatalf("notification resolved through another host: %v", err)
	}
}

func TestTaskNotificationWriteFailureRollsBackAttemptTransition(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "notification-atomic-outcome")
	if _, err := f.service.store.db.Exec(`CREATE TRIGGER fail_notification BEFORE INSERT ON task_notification_events
BEGIN SELECT RAISE(FAIL, 'notification storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.saveTaskFailure(t.Context(), accepted.Attempt.ID, "failed", "private provider diagnostic"); err == nil {
		t.Fatal("failed notification write was silently ignored")
	}
	result, err := f.service.store.taskResult(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID)
	if err != nil || result.Attempt.Status != "accepted" || result.Attempt.FinishedAt != nil || len(taskEvents(t, f, 0).Events) != 0 {
		t.Fatalf("owner state escaped its outcome transaction: %+v %v", result, err)
	}
	if _, err := f.service.store.db.Exec("DROP TRIGGER fail_notification"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.saveTaskFailure(t.Context(), accepted.Attempt.ID, "failed", "private provider diagnostic"); err != nil {
		t.Fatal(err)
	}
	if batch := taskEvents(t, f, 0); len(batch.Events) != 1 || batch.Events[0].State != "failed" {
		t.Fatalf("owner recovery did not capture the outcome: %+v", batch)
	}
	if err := f.service.store.saveTaskFailure(t.Context(), accepted.Attempt.ID, "unknown", "log synchronization is unavailable"); err != nil {
		t.Fatal(err)
	}
	if batch := taskEvents(t, f, 0); len(batch.Events) != 1 || batch.Events[0].State != "failed" {
		t.Fatalf("later synchronization failure changed the final outcome: %+v", batch)
	}
}

func TestTaskNotificationRetentionReportsGapAndPreservesPageCursor(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "notification-retention")
	err := f.service.store.transaction(t.Context(), func(tx *sql.Tx) error {
		attempt := accepted.Attempt
		for index := 0; index < taskNotificationRetention+3; index++ {
			attempt.Status = "unknown"
			if err := writeAttempt(t.Context(), tx, attempt); err != nil {
				return err
			}
			attempt.Status = "running"
			if err := writeAttempt(t.Context(), tx, attempt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first := taskEvents(t, f, 0)
	if !first.Gap || first.First != 4 || len(first.Events) != taskNotificationBatchSize || first.Next != first.Events[len(first.Events)-1].Sequence {
		t.Fatalf("retained page concealed a gap or skipped events: %+v", first)
	}
	count, cursor := len(first.Events), first.Next
	for {
		next := taskEvents(t, f, cursor)
		if next.Gap {
			t.Fatalf("a current checkpoint acquired a retention gap: %+v", next)
		}
		if len(next.Events) == 0 {
			if next.Next != cursor {
				t.Fatalf("empty page moved the completed checkpoint: %+v", next)
			}
			break
		}
		for _, event := range next.Events {
			if event.Sequence <= cursor {
				t.Fatalf("page repeated a consumed sequence: %+v", event)
			}
			cursor = event.Sequence
		}
		count += len(next.Events)
		if next.Next != cursor {
			t.Fatalf("page cursor did not cover exactly the returned events: %+v", next)
		}
	}
	if count != taskNotificationRetention || cursor != taskNotificationRetention+3 {
		t.Fatalf("retention lost a retained page: count=%d cursor=%d", count, cursor)
	}
	replacement, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.HostID == first.StoreID {
		t.Fatal("replacement task store reused the previous cursor identity")
	}
}
