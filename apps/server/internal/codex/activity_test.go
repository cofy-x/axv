//go:build darwin || linux

package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func writeServiceActivity(t *testing.T, manager *Manager, run storage.CodexSession, state, eventID string, sequence uint64) {
	t.Helper()
	dir := manager.runDirectory(run.RunID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	activity := runner.Activity{
		RunID: run.RunID, ThreadID: run.ConversationID, State: state,
		EventID: eventID, Sequence: sequence, TurnID: "turn-fixture", UpdatedAt: time.Now().UTC(),
	}
	if err := jsonfile.Write(filepath.Join(dir, "activity.json"), activity, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestActivityReviewPreservesNewEventsAndSurvivesManagerRestart(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	run := serviceRunningRun(t, manager, "activity-review")
	first, second := strings.Repeat("a", 32), strings.Repeat("b", 32)
	writeServiceActivity(t, manager, run, runner.ActivityFailed, first, 2)
	if activity := manager.describe(run).Activity; !activity.NeedsAttention || activity.Sequence != 2 || activity.State != runner.ActivityFailed {
		t.Fatalf("failed turn was not discoverable without a terminal: %+v", activity)
	}
	reviewed, err := manager.ReviewActivity(context.Background(), run.ID, run.RunID, first)
	if err != nil || reviewed.Activity.NeedsAttention || reviewed.Status != "running" || reviewed.RunID != run.RunID {
		t.Fatalf("review affected the process or failed to save: %+v %v", reviewed, err)
	}
	if _, err := manager.ReviewActivity(context.Background(), run.ID, run.RunID, first); err != nil {
		t.Fatalf("matching review retry failed: %v", err)
	}
	reopened, err := New(manager.store, manager.settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	if activity := reopened.describe(run).Activity; activity.NeedsAttention || activity.EventID != first {
		t.Fatalf("service restart lost the reviewed event: %+v", activity)
	}
	writeServiceActivity(t, manager, run, runner.ActivityInterrupted, second, 4)
	_, err = manager.ReviewActivity(context.Background(), run.ID, run.RunID, first)
	requireServiceError(t, err, Conflict, "activity_changed")
	if activity := reopened.describe(run).Activity; !activity.NeedsAttention || activity.EventID != second {
		t.Fatalf("late review hid newer activity: %+v", activity)
	}
	if review, err := manager.readActivityReview(run.RunID); err != nil || review.EventID != first {
		t.Fatalf("stale review replaced the last accepted review: %+v %v", review, err)
	}
}

func TestActivitySuccessNeedsNoAttentionOrDismissalRecord(t *testing.T) {
	for _, record := range []string{"missing", "same event", "stale", "corrupt"} {
		t.Run(record, func(t *testing.T) {
			manager := testManager(t)
			rejectServiceHostWork(t, manager)
			run := serviceRunningRun(t, manager, "activity-success")
			event, previous := strings.Repeat("a", 32), strings.Repeat("b", 32)
			writeServiceActivity(t, manager, run, runner.ActivityCompleted, event, 4)
			path := filepath.Join(manager.runDirectory(run.RunID), "activity-review.json")
			var saved []byte
			switch record {
			case "same event", "stale":
				reviewed := event
				if record == "stale" {
					reviewed = previous
				}
				saved = []byte(`{"runId":"` + run.RunID + `","eventId":"` + reviewed + `"}`)
			case "corrupt":
				saved = []byte(`{"runId":"foreign","eventId":"invalid"}`)
			}
			if saved != nil {
				if err := os.WriteFile(path, saved, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, beforeErr := os.Lstat(path)
			if activity := manager.describe(run).Activity; activity.State != runner.ActivityCompleted || activity.NeedsAttention || activity.Reason != "" || activity.EventID != event {
				t.Fatalf("successful turn required attention or a dismissal record: %+v", activity)
			}
			_, err := manager.ReviewActivity(context.Background(), run.ID, run.RunID, event)
			requireServiceError(t, err, Conflict, "activity_not_actionable")
			_, err = manager.ReviewActivity(context.Background(), run.ID, run.RunID, previous)
			requireServiceError(t, err, Conflict, "activity_changed")
			if saved == nil {
				if !os.IsNotExist(beforeErr) {
					t.Fatalf("expected no initial dismissal record: %v", beforeErr)
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("successful turn created a dismissal record: %v", err)
				}
			} else {
				after, err := os.Lstat(path)
				if beforeErr != nil || err != nil || !os.SameFile(before, after) {
					t.Fatalf("successful turn replaced an existing dismissal record: %v, %v", beforeErr, err)
				}
				if data, err := os.ReadFile(path); err != nil || string(data) != string(saved) {
					t.Fatalf("successful turn changed an existing dismissal record: %s, %v", data, err)
				}
			}
			ended, err := manager.store.UpdateCodexRun(context.Background(), run.RunID, "ended", "Session ended.")
			if err != nil {
				t.Fatal(err)
			}
			if activity := manager.describe(ended).Activity; activity.State != runner.ActivityCompleted || activity.NeedsAttention || activity.Reason != "" {
				t.Fatalf("ending the process changed a successful turn into an alert: %+v", activity)
			}
		})
	}
}

func TestActivityReviewNeverAnswersPendingRequestsOrAnotherRun(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	run := serviceRunningRun(t, manager, "activity-pending")
	other, _, err := manager.store.ReserveCodexRun(context.Background(), storage.CodexRunInput{
		Mode: "new", RequestID: "activity-other", ProjectPath: "/other/project", HistoryHome: "/other/history",
		NamePrefix: manager.namePrefix, Socket: manager.socket, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	event := strings.Repeat("c", 32)
	for _, state := range []string{runner.ActivityWaitingApproval, runner.ActivityWaitingInput} {
		writeServiceActivity(t, manager, run, state, event, 3)
		_, err := manager.ReviewActivity(context.Background(), run.ID, run.RunID, event)
		requireServiceError(t, err, Conflict, "activity_pending")
		if activity := manager.describe(run).Activity; !activity.NeedsAttention || activity.State != state {
			t.Fatalf("review dismissed an unanswered request: %+v", activity)
		}
	}
	writeServiceActivity(t, manager, other, runner.ActivityFailed, event, 1)
	if _, err := manager.ReviewActivity(context.Background(), run.ID, other.RunID, event); err == nil {
		t.Fatal("review selected another conversation's run")
	}
	if _, err := os.Stat(filepath.Join(manager.runDirectory(other.RunID), "activity-review.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected review wrote another run's data: %v", err)
	}
	if _, err := manager.ReviewActivity(context.Background(), run.ID, run.RunID, "not-an-event"); err == nil {
		t.Fatal("malformed activity event accepted")
	}
}

func TestActivityUncertaintyPreservesSnapshotsAndDamagedReview(t *testing.T) {
	manager := testManager(t)
	run := serviceRunningRun(t, manager, "activity-uncertain")
	if activity := manager.describe(run).Activity; activity.State != runner.ActivityUnknown || activity.NeedsAttention || activity.Reason == "" {
		t.Fatalf("missing observer state invented activity: %+v", activity)
	}
	event := strings.Repeat("d", 32)
	writeServiceActivity(t, manager, run, runner.ActivityWorking, event, 1)
	uncertain := run
	uncertain.Reason = "The runtime could not be inspected."
	release, err := manager.runGates.acquire(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	activity := manager.describeObserved(uncertain).Activity
	release()
	if activity.State != runner.ActivityUnknown || activity.Reason == "" {
		t.Fatalf("uncertain runtime was presented as confirmed work: %+v", activity)
	}
	ended, err := manager.store.UpdateCodexRun(context.Background(), run.RunID, "ended", "Session ended.")
	if err != nil {
		t.Fatal(err)
	}
	if activity := manager.describe(ended).Activity; activity.State != runner.ActivityUnknown || activity.NeedsAttention || activity.Reason == "" {
		t.Fatalf("ended run remained working: %+v", activity)
	}
	writeServiceActivity(t, manager, run, runner.ActivityFailed, event, 2)
	if activity := manager.describe(ended).Activity; activity.State != runner.ActivityFailed || !activity.NeedsAttention {
		t.Fatalf("ending the process erased its failed turn alert: %+v", activity)
	}
	path := filepath.Join(manager.runDirectory(run.RunID), "activity-review.json")
	const damaged = `{"runId":"foreign","eventId":"not-an-event"}`
	if err := os.WriteFile(path, []byte(damaged), 0600); err != nil {
		t.Fatal(err)
	}
	activity = manager.describe(run).Activity
	if !activity.NeedsAttention || activity.Reason == "" {
		t.Fatalf("damaged review hid the event or its error: %+v", activity)
	}
	_, err = manager.ReviewActivity(context.Background(), run.ID, run.RunID, event)
	requireServiceError(t, err, Unavailable, "activity_review_unavailable")
	if kept, err := os.ReadFile(path); err != nil || string(kept) != damaged {
		t.Fatalf("damaged review was silently reset: %s %v", kept, err)
	}
}

func TestActivityUngatedDescriptionRefreshesRetainedLifecycle(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	running := serviceRunningRun(t, manager, "activity-stale-lifecycle")
	writeServiceActivity(t, manager, running, runner.ActivityWaitingApproval, strings.Repeat("c", 32), 2)
	ended, err := manager.store.UpdateCodexRun(context.Background(), running.RunID, "ended", "Session ended.")
	if err != nil {
		t.Fatal(err)
	}
	current := manager.describe(ended)
	stale := manager.describe(running)
	if stale.Status != "ended" || stale.Activity.State != runner.ActivityUnknown || stale.Activity.NeedsAttention ||
		stale.Activity.Observation.Revision <= current.Activity.Observation.Revision {
		t.Fatalf("retained running record revived ended activity: current=%+v, stale=%+v", current, stale)
	}
	// A transient error retained by an ungated caller is not a new runtime
	// inspection. It cannot replace the current persisted lifecycle either.
	running.Reason = "An old inspection failed."
	if stale := manager.describe(running); stale.Status != "ended" || stale.Reason != ended.Reason {
		t.Fatalf("retained transient reason replaced current lifecycle: %+v", stale)
	}
}

func TestActivityDelayedListCannotRepublishAnOlderInspection(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery; runtime inspection is injected")
	}
	for _, inspectFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "ended run", true: "recovered inspection"}[inspectFailure], func(t *testing.T) {
			manager := testManager(t)
			ctx := context.Background()
			serviceRunningRun(t, manager, "activity-delay-first")
			other, _, err := manager.store.ReserveCodexRun(ctx, storage.CodexRunInput{
				Mode: "new", RequestID: "activity-delay-second", ProjectPath: "/other/project", HistoryHome: "/service/history",
				NamePrefix: manager.namePrefix, Socket: manager.socket, Cols: 80, Rows: 24,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.store.BindCodexConversation(ctx, other.RunID, "11234567-89ab-cdef-0123-456789abcdef", other.HistoryHome); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.store.StartCodexRun(ctx, other.RunID); err != nil {
				t.Fatal(err)
			}
			runs, err := manager.store.CodexSessions(ctx)
			if err != nil || len(runs) != 2 {
				t.Fatalf("runs = %+v, %v", runs, err)
			}
			first, second := runs[0], runs[1]
			writeServiceActivity(t, manager, first, runner.ActivityWorking, strings.Repeat("d", 32), 2)
			writeServiceActivity(t, manager, second, runner.ActivityWorking, strings.Repeat("e", 32), 2)
			entered, unblock := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(unblock) }) }
			defer release()
			var firstCalls atomic.Int32
			manager.run = func(ctx context.Context, _ string, args ...string) (string, error) {
				if slices.Contains(args, "="+first.TmuxSession+":0") {
					if firstCalls.Add(1) == 1 && inspectFailure {
						return "", errors.New("fixture inspection unavailable")
					}
					return first.TmuxSession + "\t0\t0:", nil
				}
				if slices.Contains(args, "="+second.TmuxSession+":0") {
					close(entered)
					select {
					case <-unblock:
						return second.TmuxSession + "\t0\t0:", nil
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
				return "", errors.New("unexpected fixture command")
			}
			type listResult struct {
				sessions []Session
				err      error
			}
			listed := make(chan listResult, 1)
			go func() {
				sessions, err := manager.List(ctx)
				listed <- listResult{sessions, err}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("list did not reach the delayed second inspection")
			}
			var current Session
			if inspectFailure {
				current, err = manager.Get(ctx, first.ID)
				if err != nil || current.Activity.State != runner.ActivityWorking || current.Reason != "" {
					t.Fatalf("healthy recovery = %+v, %v", current, err)
				}
			} else {
				ended, err := manager.store.UpdateCodexRun(ctx, first.RunID, "ended", "Session ended.")
				if err != nil {
					t.Fatal(err)
				}
				current = manager.describe(ended)
			}
			release()
			select {
			case result := <-listed:
				if result.err != nil || len(result.sessions) != 2 {
					t.Fatalf("delayed list = %+v, %v", result.sessions, result.err)
				}
				previous := result.sessions[0]
				if previous.RunID != first.RunID || previous.Activity.Observation.Revision >= current.Activity.Observation.Revision {
					t.Fatalf("delayed list stamped an older inspection after current state: previous=%+v, current=%+v", previous, current)
				}
				if inspectFailure && (previous.Reason == "" || previous.Activity.State != runner.ActivityUnknown) {
					t.Fatalf("ordered snapshot lost its transient inspection error: %+v", previous)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("delayed list did not finish")
			}
		})
	}
}

func TestActivityReviewRetainsExactPreviousRunAfterContinuation(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	ctx := context.Background()
	previous := serviceRunningRun(t, manager, "activity-previous")
	previous, err := manager.store.UpdateCodexRun(ctx, previous.RunID, "ended", "")
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := manager.store.ReserveCodexRun(ctx, storage.CodexRunInput{
		Mode: "resume", SessionID: previous.ID, ExpectedRunID: previous.RunID,
		RequestID: "activity-continuation", ProjectPath: previous.ProjectPath, HistoryHome: previous.HistoryHome,
		NamePrefix: manager.namePrefix, Socket: manager.socket, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	event := strings.Repeat("e", 32)
	writeServiceActivity(t, manager, previous, runner.ActivityInterrupted, event, 2)
	writeServiceActivity(t, manager, next, runner.ActivityWaitingApproval, strings.Repeat("f", 32), 1)
	_, err = manager.ReviewActivity(ctx, previous.ID, previous.RunID, event)
	if err != nil {
		t.Fatalf("could not review exact original run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manager.runDirectory(next.RunID), "activity-review.json")); !os.IsNotExist(err) {
		t.Fatalf("old review wrote to replacement run: %v", err)
	}
}

func TestActivityObservationOrdersDiagnosticsAndRecoveryWithoutProviderChanges(t *testing.T) {
	manager := testManager(t)
	run := serviceRunningRun(t, manager, "activity-read-order")
	event := strings.Repeat("a", 32)
	writeServiceActivity(t, manager, run, runner.ActivityFailed, event, 2)
	first := manager.describe(run).Activity
	path := filepath.Join(manager.runDirectory(run.RunID), "activity-review.json")
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	damaged := manager.describe(run).Activity
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	recovered := manager.describe(run).Activity
	if first.Observation.InstanceID == "" || first.Observation.Revision == 0 ||
		first.Observation.InstanceID != damaged.Observation.InstanceID || damaged.Observation.InstanceID != recovered.Observation.InstanceID ||
		first.Observation.Revision >= damaged.Observation.Revision || damaged.Observation.Revision >= recovered.Observation.Revision ||
		first.Sequence != damaged.Sequence || damaged.Sequence != recovered.Sequence || damaged.Reason == "" || recovered.Reason != "" {
		t.Fatalf("service reads could not order diagnostics at one provider event: %+v %+v %+v", first, damaged, recovered)
	}
	if err := os.Remove(filepath.Join(manager.runDirectory(run.RunID), "activity.json")); err != nil {
		t.Fatal(err)
	}
	unknown := manager.describe(run).Activity
	if unknown.State != runner.ActivityUnknown || unknown.Observation.Revision <= recovered.Observation.Revision {
		t.Fatalf("unknown snapshot lacked ordered observation: %+v", unknown)
	}
	reopened, err := New(manager.store, manager.settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	if next := reopened.describe(run).Activity; next.Observation.InstanceID == first.Observation.InstanceID || next.Observation.Revision == 0 {
		t.Fatalf("a new service reused the old observation clock: %+v", next)
	}
}

func TestActivityReviewRejectsUnsafeOrAmbiguousRecordsWithoutReplacement(t *testing.T) {
	for _, kind := range []string{"duplicate", "case alias", "oversized", "public", "symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			manager := testManager(t)
			rejectServiceHostWork(t, manager)
			run := serviceRunningRun(t, manager, "activity-record-"+kind)
			event := strings.Repeat("b", 32)
			writeServiceActivity(t, manager, run, runner.ActivityFailed, event, 2)
			path := filepath.Join(manager.runDirectory(run.RunID), "activity-review.json")
			valid := `{"runId":"` + run.RunID + `","eventId":"` + event + `"}`
			var err error
			switch kind {
			case "duplicate":
				err = os.WriteFile(path, []byte(`{"runId":"foreign","runId":"`+run.RunID+`","eventId":"`+event+`"}`), 0600)
			case "case alias":
				err = os.WriteFile(path, []byte(strings.Replace(valid, "eventId", "EventId", 1)), 0600)
			case "oversized":
				err = os.WriteFile(path, []byte(valid+strings.Repeat(" ", 4096)), 0600)
			case "public":
				err = os.WriteFile(path, []byte(valid), 0600)
				if err == nil {
					err = os.Chmod(path, 0644)
				}
			case "symlink":
				err = os.Symlink(filepath.Join(manager.runDirectory(run.RunID), "activity.json"), path)
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if activity := manager.describe(run).Activity; !activity.NeedsAttention || activity.Reason == "" {
				t.Fatalf("unsafe review hid its diagnostic or pending event: %+v", activity)
			}
			_, err = manager.ReviewActivity(context.Background(), run.ID, run.RunID, event)
			requireServiceError(t, err, Unavailable, "activity_review_unavailable")
			if after, err := os.Lstat(path); err != nil || !os.SameFile(before, after) {
				t.Fatalf("unsafe review was replaced: %v", err)
			}
		})
	}
}
