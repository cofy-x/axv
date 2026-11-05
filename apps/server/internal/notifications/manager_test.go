package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

type notificationTransport func(*http.Request) (*http.Response, error)

func (transport notificationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func notificationResponse(status int, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader("private provider response"))}
}

func openNotificationTest(t *testing.T, options Options) *Manager {
	t.Helper()
	if options.Home == "" {
		options.Home = t.TempDir()
	}
	options.DisableWorker = true
	if options.Client == nil {
		options.Client = &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
			t.Fatal("unexpected external delivery")
			return nil, errors.New("fixture refused delivery")
		})}
	}
	m, err := Open(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if m.lock != nil {
			if err := m.Close(); err != nil {
				t.Error(err)
			}
			m.lock = nil
		}
	})
	return m
}

func closeNotificationTest(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m.lock = nil
}

func enableNotificationTest(t *testing.T, m *Manager) {
	t.Helper()
	s := m.Snapshot()
	if err := m.Configure(Update{StoreID: s.StoreID, Revision: s.Revision, Enabled: true,
		TokenAction: "replace", Token: "private-token-value", ReturnOrigin: "https://axv.example",
		ApplicationID: "fixture-app", DeviceID: "fixture-device", EventTypes: []string{
			"agent_waiting_approval", "agent_waiting_input", "agent_succeeded", "agent_failed", "agent_interrupted",
			"task_succeeded", "task_failed", "task_cancelled", "task_interrupted", "task_unknown",
		}}); err != nil {
		t.Fatal(err)
	}
}

func notificationSourceEvent(sequence uint64, state, id, store string, at time.Time) notificationevent.Event {
	return notificationevent.Event{ID: id, Sequence: sequence, State: state, ObservedAt: at,
		Target: notificationevent.Target{Kind: "codex", StoreID: store, SessionID: "private-session", RunID: "private-run",
			ThreadID: "private-thread", TurnID: "private-turn", EventID: id}}
}

func TestIntakeCheckpointAndOutboxCommitTogetherAndSurviveRestart(t *testing.T) {
	ctx := context.Background()
	clock := time.Now().UTC()
	batch := notificationevent.Batch{SourceID: "codex:run", StoreID: "work-store", First: 1, Next: 2,
		Events: []notificationevent.Event{
			notificationSourceEvent(1, "working", "work", "work-store", clock.Add(time.Second)),
			notificationSourceEvent(2, "completed", "complete", "work-store", clock.Add(2*time.Second)),
		}}
	options := Options{Sources: []Source{{Name: "codex", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
		return []notificationevent.Batch{batch}, nil
	}}}}
	m := openNotificationTest(t, options)
	m.now = func() time.Time { return clock }
	enableNotificationTest(t, m)
	// A destination directory makes the atomic write fail before either the
	// durable cursor or the corresponding outbox can advance.
	original := m.path + ".owned-backup"
	if err := os.Rename(m.path, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.path, 0o700); err != nil {
		t.Fatal(err)
	}
	m.collect(ctx)
	if len(m.state.Checkpoints) != 0 || len(m.Snapshot().Events) != 0 || m.Snapshot().Error == "" {
		t.Fatalf("failed transaction advanced intake: %+v", m.Snapshot())
	}
	if err := os.Remove(m.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(original, m.path); err != nil {
		t.Fatal(err)
	}
	m.collect(ctx)
	m.collect(ctx)
	snapshot := m.Snapshot()
	if m.state.Checkpoints[batch.SourceID].Sequence != 2 || len(snapshot.Events) != 1 || snapshot.Events[0].Kind != "agent_succeeded" || snapshot.Error != "" {
		t.Fatalf("durable duplicate intake = %+v, %+v", m.state.Checkpoints, snapshot)
	}
	id := snapshot.Events[0].ID
	options.Home = m.options.Home
	closeNotificationTest(t, m)
	reopened := openNotificationTest(t, options)
	reopened.collect(ctx)
	if reopened.state.Checkpoints[batch.SourceID].Sequence != 2 || len(reopened.Snapshot().Events) != 1 || reopened.Snapshot().Events[0].ID != id {
		t.Fatalf("restart duplicated or lost the original observation: %+v", reopened.Snapshot())
	}
}

func TestSendingRecoveryIsUncertainAndRetriesAreBounded(t *testing.T) {
	ctx := context.Background()
	clock := time.Now().UTC()
	calls := 0
	options := Options{Client: &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("private network error")
	})}}
	m := openNotificationTest(t, options)
	m.now = func() time.Time { return clock }
	enableNotificationTest(t, m)
	s := m.Snapshot()
	if err := m.Test(s.StoreID, s.Revision); err != nil {
		t.Fatal(err)
	}
	next := clone(m.state)
	next.Events[0].Status, next.Events[0].Attempts = "sending", 1
	if err := m.save(next); err != nil {
		t.Fatal(err)
	}
	options.Home = m.options.Home
	closeNotificationTest(t, m)
	reopened := openNotificationTest(t, options)
	current := reopened.Snapshot().Events[0]
	if current.Status != "uncertain" || current.Attempts != 1 || current.NextAttemptAt == nil || current.Error != "response_uncertain" {
		t.Fatalf("sending crash implied an outcome: %+v", current)
	}
	clock = current.NextAttemptAt.Add(-time.Second)
	reopened.now = func() time.Time { return clock }
	reopened.dispatch(ctx)
	if calls != 0 {
		t.Fatal("uncertain attempt retried before its backoff")
	}
	for index := 0; index < maxAttempts+1; index++ {
		clock = clock.Add(10 * time.Minute)
		reopened.dispatch(ctx)
	}
	current = reopened.Snapshot().Events[0]
	if calls != maxAttempts-1 || current.Status != "uncertain" || current.Attempts != maxAttempts || current.NextAttemptAt != nil {
		t.Fatalf("uncertain retries exceeded bound: calls=%d event=%+v", calls, current)
	}
}

func TestConfigurationRevisionAndDisablePreventNewDelivery(t *testing.T) {
	calls := 0
	m := openNotificationTest(t, Options{Client: &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return notificationResponse(http.StatusCreated, nil), nil
	})}})
	enableNotificationTest(t, m)
	before := m.Snapshot()
	if err := m.Test(before.StoreID, before.Revision); err != nil {
		t.Fatal(err)
	}
	if err := m.Disable(before.StoreID, before.Revision-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale configuration revision accepted: %v", err)
	}
	if err := m.Disable(before.StoreID, before.Revision); err != nil {
		t.Fatal(err)
	}
	m.dispatch(context.Background())
	after := m.Snapshot()
	if calls != 0 || after.Config.Enabled || after.Revision != before.Revision+1 || after.Events[0].Status != "disabled" {
		t.Fatalf("disabled outbox sent work: calls=%d state=%+v", calls, after)
	}
	if err := m.Test(after.StoreID, after.Revision); !errors.Is(err, ErrInput) {
		t.Fatalf("disabled test delivery accepted: %v", err)
	}
}

func TestWaitingNotificationsCoalesceAndResolveBeforeDelivery(t *testing.T) {
	clock := time.Now().UTC()
	batch := notificationevent.Batch{SourceID: "codex:run", StoreID: "work-store", First: 1, Next: 2,
		Events: []notificationevent.Event{
			notificationSourceEvent(1, "waiting_approval", "wait", "work-store", clock.Add(time.Second)),
			notificationSourceEvent(2, "working", "resolved", "work-store", clock.Add(2*time.Second)),
		}}
	m := openNotificationTest(t, Options{Sources: []Source{{Name: "codex", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
		return []notificationevent.Batch{batch}, nil
	}}}, Resolve: func(_ context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
		return notificationevent.Resolution{Available: true, Current: false, State: "working", Target: target}, nil
	}})
	m.now = func() time.Time { return clock }
	enableNotificationTest(t, m)
	m.collect(context.Background())
	if events := m.Snapshot().Events; len(events) != 1 || events[0].Status != "superseded" {
		t.Fatalf("resolution transition did not coalesce wait: %+v", events)
	}
	batch.Next = 3
	batch.Events = append(batch.Events, notificationSourceEvent(3, "waiting_input", "wait-again", "work-store", clock.Add(3*time.Second)))
	m.collect(context.Background())
	m.dispatch(context.Background())
	if events := m.Snapshot().Events; len(events) != 2 || events[0].Status != "superseded" {
		t.Fatalf("delivery ignored exact current-state resolution: %+v", events)
	}
}

func TestQuotaRetryAfterIsHonoredAndAttemptsStayBounded(t *testing.T) {
	clock := time.Now().UTC()
	calls := 0
	m := openNotificationTest(t, Options{Client: &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return notificationResponse(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"120"}}), nil
	})}})
	m.now = func() time.Time { return clock }
	enableNotificationTest(t, m)
	s := m.Snapshot()
	if err := m.Test(s.StoreID, s.Revision); err != nil {
		t.Fatal(err)
	}
	m.dispatch(context.Background())
	event := m.Snapshot().Events[0]
	if calls != 1 || event.Status != "pending" || event.Error != "quota_exhausted" || event.NextAttemptAt == nil || !event.NextAttemptAt.Equal(clock.Add(120*time.Second)) {
		t.Fatalf("quota response lost Retry-After: calls=%d event=%+v", calls, event)
	}
	clock = clock.Add(119 * time.Second)
	m.dispatch(context.Background())
	if calls != 1 {
		t.Fatal("quota retry ran before Retry-After")
	}
	for index := 0; index < maxAttempts+1; index++ {
		clock = clock.Add(3 * time.Minute)
		m.dispatch(context.Background())
	}
	event = m.Snapshot().Events[0]
	if calls != maxAttempts || event.Status != "failed" || event.NextAttemptAt != nil || event.Attempts != maxAttempts {
		t.Fatalf("quota retries exceeded bound: calls=%d event=%+v", calls, event)
	}
}

func TestProviderRedirectCannotForwardTokenAndPayloadContainsOnlySafeCopy(t *testing.T) {
	var requests int
	m := openNotificationTest(t, Options{Client: &http.Client{Transport: notificationTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != pocketAlertEndpoint || request.Method != http.MethodPost || request.Header.Get("Token") != "private-token-value" {
			t.Fatalf("credential sent outside fixed provider endpoint: %s", request.URL)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{"private-token", "private-session", "private-run", "private-thread", "private-turn", "/host/project", "private provider response"} {
			if strings.Contains(string(body), private) {
				t.Fatalf("sensitive value entered outbound payload: %s", private)
			}
		}
		if !strings.Contains(string(body), "https://axv.example/?notification=") {
			t.Fatalf("missing opaque return link: %s", body)
		}
		return notificationResponse(http.StatusTemporaryRedirect, http.Header{"Location": []string{"https://unrelated.example/leak"}}), nil
	})}, Resolve: func(_ context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
		return notificationevent.Resolution{Available: true, Current: true, State: "failed", Target: target}, nil
	}})
	enableNotificationTest(t, m)
	next := clone(m.state)
	event := notificationSourceEvent(1, "failed", "event", "work-store", time.Now().UTC())
	next.Events = append(next.Events, Notification{ID: strings.Repeat("a", 32), CreatedAt: event.ObservedAt, Kind: "agent_failed", State: event.State, Status: "pending", Target: event.Target})
	if err := m.save(next); err != nil {
		t.Fatal(err)
	}
	m.dispatch(context.Background())
	if requests != 1 || m.Snapshot().Events[0].Status != "failed" {
		t.Fatalf("redirect followed or accepted: requests=%d snapshot=%+v", requests, m.Snapshot())
	}
	data, err := json.Marshal(m.Snapshot())
	if err != nil || strings.Contains(string(data), "private-token") || strings.Contains(string(data), "private provider response") || strings.Contains(string(data), "unrelated.example") {
		t.Fatalf("credential or provider response entered public history: %s, %v", data, err)
	}
}

func TestStoreReplacementAndCorruptionRemainVisibleAndPreserveOriginalTargets(t *testing.T) {
	clock := time.Now().UTC()
	batch := notificationevent.Batch{SourceID: "codex:run", StoreID: "old-work-store", First: 1, Next: 1,
		Events: []notificationevent.Event{notificationSourceEvent(1, "failed", "same-event", "old-work-store", clock.Add(time.Second))}}
	options := Options{Sources: []Source{{Name: "codex", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
		return []notificationevent.Batch{batch}, nil
	}}}}
	m := openNotificationTest(t, options)
	m.now = func() time.Time { return clock }
	enableNotificationTest(t, m)
	m.collect(context.Background())
	batch.StoreID = "new-work-store"
	batch.Events[0].Target.StoreID = batch.StoreID
	m.collect(context.Background())
	s := m.Snapshot()
	if !s.Intake.Gap || s.Intake.Error == "" || len(s.Events) != 2 || s.Events[0].Target.StoreID == s.Events[1].Target.StoreID {
		t.Fatalf("work-store replacement hid original identity: %+v", s)
	}
	options.Home = m.options.Home
	path := m.path
	closeNotificationTest(t, m)
	corrupt := []byte(`{"version":1,"damaged":true}`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(context.Background(), Options{Home: options.Home, DisableWorker: true}); !errors.Is(err, ErrStore) {
		if reopened != nil {
			reopened.Close()
		}
		t.Fatalf("corrupt notification store initialized silently: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(corrupt) {
		t.Fatalf("corrupt notification store was replaced: %q, %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(context.Background(), Options{Home: options.Home, DisableWorker: true}); !errors.Is(err, ErrStore) {
		if reopened != nil {
			reopened.Close()
		}
		t.Fatalf("missing established notification store initialized silently: %v", err)
	}
	if _, err := os.Stat(filepath.Join(options.Home, "notifications", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("missing established store was recreated: %v", err)
	}
}
