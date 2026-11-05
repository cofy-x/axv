package notifications

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func TestSourceFailureRemainsVisibleBesideHealthySourceAndRecovers(t *testing.T) {
	for _, healthyFirst := range []bool{false, true} {
		name := "failure-first"
		if healthyFirst {
			name = "healthy-first"
		}
		t.Run(name, func(t *testing.T) {
			clock := time.Now().UTC()
			failed := true
			unavailable := Source{Name: "unavailable", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
				if failed {
					return nil, errors.New("private source failure")
				}
				return []notificationevent.Batch{{SourceID: "recovered-source", StoreID: "source-store"}}, nil
			}}
			healthy := Source{Name: "healthy", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
				return []notificationevent.Batch{{SourceID: "healthy-source", StoreID: "source-store", First: 1, Next: 1,
					Events: []notificationevent.Event{notificationSourceEvent(1, "completed", "complete", "source-store", clock.Add(time.Second))}}}, nil
			}}
			sources := []Source{unavailable, healthy}
			if healthyFirst {
				sources = []Source{healthy, unavailable}
			}
			m := openNotificationTest(t, Options{Sources: sources})
			m.now = func() time.Time { return clock }
			enableNotificationTest(t, m)
			m.collect(context.Background())
			s := m.Snapshot()
			if s.Intake.Error == "" || s.Intake.Gap || len(s.Events) != 1 || s.Events[0].Kind != "agent_succeeded" || m.state.Checkpoints["healthy-source"].Sequence != 1 {
				t.Fatalf("healthy source masked a failed intake or lost completed event: %+v", s)
			}
			if _, exists := m.state.Checkpoints["recovered-source"]; exists {
				t.Fatal("failed source acquired a fabricated checkpoint")
			}
			failed = false
			m.collect(context.Background())
			s = m.Snapshot()
			if s.Intake.Error != "" || s.Intake.Gap || len(s.Events) != 1 {
				t.Fatalf("successful recovery failed to clear transient error or duplicated history: %+v", s)
			}
		})
	}
}

func TestEnableCutoffExcludesUnobservedEventsFromDisabledPeriods(t *testing.T) {
	ctx := context.Background()
	start := time.Now().UTC()
	clock := start
	batch := notificationevent.Batch{SourceID: "codex:run", StoreID: "work-store", First: 1, Next: 1,
		Events: []notificationevent.Event{notificationSourceEvent(1, "failed", "before-enable", "work-store", start.Add(time.Second))}}
	m := openNotificationTest(t, Options{Sources: []Source{{Name: "codex", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
		return []notificationevent.Batch{batch}, nil
	}}}})
	m.now = func() time.Time { return clock }
	// This event was never polled while disabled. Enabling still must not
	// retroactively queue it from the independent producer journal.
	clock = start.Add(10 * time.Second)
	enableNotificationTest(t, m)
	m.collect(ctx)
	if s := m.Snapshot(); len(s.Events) != 0 || m.state.Checkpoints[batch.SourceID].Sequence != 1 {
		t.Fatalf("first enable replayed an old disabled-period event: %+v", s)
	}
	batch.Next = 2
	batch.Events = append(batch.Events, notificationSourceEvent(2, "completed", "while-enabled", "work-store", start.Add(12*time.Second)))
	m.collect(ctx)
	if s := m.Snapshot(); len(s.Events) != 1 || s.Events[0].Kind != "agent_succeeded" {
		t.Fatalf("new enabled observation not queued: %+v", s)
	}
	clock = start.Add(13 * time.Second)
	snapshot := m.Snapshot()
	if err := m.Disable(snapshot.StoreID, snapshot.Revision); err != nil {
		t.Fatal(err)
	}
	batch.Next = 3
	batch.Events = append(batch.Events, notificationSourceEvent(3, "failed", "while-disabled", "work-store", start.Add(14*time.Second)))
	// No polling occurs during this disabled interval either.
	clock = start.Add(20 * time.Second)
	enableNotificationTest(t, m)
	m.collect(ctx)
	if s := m.Snapshot(); len(s.Events) != 1 || s.Events[0].Status != "disabled" || m.state.Checkpoints[batch.SourceID].Sequence != 3 {
		t.Fatalf("re-enable replayed disabled observations or retargeted old outbox: %+v", s)
	}
	batch.Next = 4
	batch.Events = append(batch.Events, notificationSourceEvent(4, "failed", "after-reenable", "work-store", start.Add(21*time.Second)))
	m.collect(ctx)
	if s := m.Snapshot(); len(s.Events) != 2 || s.Events[0].Status != "pending" || s.Events[0].Target.EventID != "after-reenable" {
		t.Fatalf("new event after re-enable not queued: %+v", s)
	}
}
