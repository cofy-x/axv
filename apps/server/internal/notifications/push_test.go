package notifications

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
	"github.com/cofy-x/axv/apps/server/internal/notifications/webpush"
)

type pushTransport func(context.Context, webpush.Subscription, string, string, []byte, time.Duration, string) webpush.Outcome

func (f pushTransport) Send(c context.Context, s webpush.Subscription, a, b string, p []byte, t time.Duration, subject string) webpush.Outcome {
	return f(c, s, a, b, p, t, subject)
}
func pushFixtureSubscription(t *testing.T, id string) webpush.Subscription {
	t.Helper()
	_, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	return webpush.Subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/" + id, Keys: webpush.Keys{P256DH: public, Auth: base64.RawURLEncoding.EncodeToString([]byte("private-auth-123"))}}
}
func preparePushTest(t *testing.T, m *Manager) PushSnapshot {
	t.Helper()
	s := m.PushSnapshot()
	p, err := m.PreparePush(PushPrepare{PushIdentity: PushIdentity{s.StoreID, s.Revision}, Contact: "mailto:owner@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func subscribePushTest(t *testing.T, m *Manager, id int, origin string) PushSubscriptionIdentity {
	t.Helper()
	s := m.PushSnapshot()
	sub, err := m.SubscribePush(PushSubscribe{PushIdentity: PushIdentity{s.StoreID, s.Revision}, KeyID: s.KeyID, InstallationID: fmt.Sprintf("%032x", id), Label: "Synthetic browser", EventTypes: []string{"agent_failed", "agent_waiting_input"}, Subscription: pushFixtureSubscription(t, fmt.Sprint(id))}, origin)
	if err != nil {
		t.Fatal(err)
	}
	last := sub.Subscriptions[len(sub.Subscriptions)-1]
	return PushSubscriptionIdentity{PushIdentity: PushIdentity{sub.StoreID, sub.Revision}, KeyID: sub.KeyID, InstallationID: last.InstallationID, SubscriptionID: last.ID}
}
func currentPushIdentity(m *Manager, i PushSubscriptionIdentity) PushSubscriptionIdentity {
	i.Revision = m.PushSnapshot().Revision
	return i
}

func TestPushDefaultDoesNotPrepareCredentialsAndSnapshotIsSafe(t *testing.T) {
	m := openNotificationTest(t, Options{})
	if p := m.PushSnapshot(); p.KeyID != "" || p.PublicKey != "" || p.Revision != 0 || len(p.Subscriptions) != 0 || m.state.WebPush != nil {
		t.Fatalf("default enrollment: %+v", p)
	}
	s := preparePushTest(t, m)
	id := subscribePushTest(t, m, 1, "https://axv.example")
	raw, err := json.Marshal(m.PushSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	secret := m.state.WebPush
	for _, value := range []string{secret.PrivateKey, secret.Subscriptions[0].Subscription.Endpoint, secret.Subscriptions[0].Subscription.Keys.Auth, secret.Subscriptions[0].Subscription.Keys.P256DH} {
		if strings.Contains(string(raw), value) {
			t.Fatal("safe snapshot leaked enrollment material")
		}
	}
	if _, err = m.PreparePush(PushPrepare{PushIdentity: PushIdentity{s.StoreID, s.Revision}, Contact: s.Contact}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale prepare = %v", err)
	}
	for name, change := range map[string]func(*PushSubscriptionIdentity){"store": func(i *PushSubscriptionIdentity) { i.StoreID = strings.Repeat("e", 32) }, "key": func(i *PushSubscriptionIdentity) { i.KeyID = strings.Repeat("e", 32) }, "installation": func(i *PushSubscriptionIdentity) { i.InstallationID = strings.Repeat("e", 32) }, "subscription": func(i *PushSubscriptionIdentity) { i.SubscriptionID = strings.Repeat("e", 32) }, "revision": func(i *PushSubscriptionIdentity) { i.Revision-- }} {
		t.Run(name, func(t *testing.T) {
			i := id
			change(&i)
			if _, err := m.TestPush(i, "https://axv.example"); err == nil {
				t.Fatal("unrelated subscription accepted")
			}
		})
	}
	if _, err = m.TestPush(id, "https://other.example"); !errors.Is(err, ErrConflict) {
		t.Fatalf("origin mismatch = %v", err)
	}
}

func TestPushSharedIntakeAndExactBrowserTestWorkWithoutPocket(t *testing.T) {
	now := time.Now().UTC()
	calls := 0
	var payloads []map[string]any
	batch := notificationevent.Batch{SourceID: "codex:run", StoreID: "work-store", First: 1, Next: 1, Events: []notificationevent.Event{notificationSourceEvent(1, "failed", "private-event", "work-store", now.Add(time.Second))}}
	opts := Options{Sources: []Source{{Name: "codex", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
		return []notificationevent.Batch{batch}, nil
	}}}, Resolve: func(_ context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
		return notificationevent.Resolution{Available: true, Current: true, State: "failed", Target: target}, nil
	}, PushSender: pushTransport(func(_ context.Context, _ webpush.Subscription, _, _ string, p []byte, ttl time.Duration, _ string) webpush.Outcome {
		calls++
		var v map[string]any
		if json.Unmarshal(p, &v) != nil {
			t.Fatal("invalid payload")
		}
		payloads = append(payloads, v)
		if ttl <= 0 || ttl > pushTTL {
			t.Fatal("unbounded TTL")
		}
		for _, private := range []string{"private-session", "private-run", "private-thread", "private-event", "work-store"} {
			if strings.Contains(string(p), private) {
				t.Fatal("private work identity in payload")
			}
		}
		return webpush.Outcome{Status: webpush.Accepted}
	})}
	m := openNotificationTest(t, opts)
	m.now = func() time.Time { return now }
	preparePushTest(t, m)
	first := subscribePushTest(t, m, 1, "https://axv.example")
	second := subscribePushTest(t, m, 2, "https://axv.example")
	m.collect(context.Background())
	m.collect(context.Background())
	history := m.Snapshot()
	if history.Config.Enabled || len(history.Events) != 1 || len(history.Events[0].PushDeliveries) != 2 || history.Events[0].Target.EventID != "private-event" || history.Events[0].Status != "disabled" {
		t.Fatalf("push intake depends on Pocket: %+v", history)
	}
	originalID := history.Events[0].ID
	m.dispatch(context.Background())
	m.dispatchPush(context.Background())
	m.dispatchPush(context.Background())
	if calls != 2 {
		t.Fatalf("fanout sends = %d", calls)
	}
	for _, p := range payloads {
		if p["notificationId"] != originalID || p["storeId"] != history.StoreID {
			t.Fatal("delivery lost shared event identity")
		}
	}
	if payloads[0]["subscriptionId"] == payloads[1]["subscriptionId"] {
		t.Fatal("fanout reused a browser receipt")
	}
	if _, err := m.TestPush(currentPushIdentity(m, first), "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	m.dispatchPush(context.Background())
	if calls != 3 || payloads[2]["subscriptionId"] != first.SubscriptionID || payloads[2]["subscriptionId"] == second.SubscriptionID {
		t.Fatal("synthetic test retargeted another browser")
	}
}

func TestPushExpiryRetirementAndCooldownArePerSubscription(t *testing.T) {
	for _, status := range []webpush.Status{webpush.Expired, webpush.RateLimited} {
		t.Run(string(status), func(t *testing.T) {
			clock := time.Now().UTC()
			calls := 0
			m := openNotificationTest(t, Options{PushSender: pushTransport(func(context.Context, webpush.Subscription, string, string, []byte, time.Duration, string) webpush.Outcome {
				calls++
				if calls == 1 {
					return webpush.Outcome{Status: status, RetryAfter: time.Hour}
				}
				return webpush.Outcome{Status: webpush.Accepted}
			})})
			m.now = func() time.Time { return clock }
			preparePushTest(t, m)
			a := subscribePushTest(t, m, 1, "https://axv.example")
			b := subscribePushTest(t, m, 2, "https://axv.example")
			if _, err := m.TestPush(currentPushIdentity(m, a), "https://axv.example"); err != nil {
				t.Fatal(err)
			}
			if _, err := m.TestPush(b, "https://axv.example"); err != nil {
				t.Fatal(err)
			}
			m.dispatchPush(context.Background())
			m.dispatchPush(context.Background())
			if calls != 2 {
				t.Fatalf("one subscription blocked another: %d", calls)
			}
			sub := m.PushSnapshot().Subscriptions[0]
			if status == webpush.Expired {
				if sub.Enabled || sub.Status != "expired" || m.state.WebPush.Subscriptions[0].Subscription.Endpoint != "" {
					t.Fatal("expired capability retained")
				}
			} else {
				if sub.NextSendAt == nil || !sub.Enabled {
					t.Fatal("cooldown missing")
				}
				clock = clock.Add(pushTTL + time.Second)
				m.dispatchPush(context.Background())
				if calls != 2 {
					t.Fatal("TTL-expired delivery retried")
				}
				for _, e := range m.Snapshot().Events {
					for _, d := range e.PushDeliveries {
						if d.SubscriptionID == a.SubscriptionID && d.Status != "expired" {
							t.Fatal("TTL not reflected")
						}
					}
				}
			}
			if !validState(m.state) {
				t.Fatal("provider result made store invalid")
			}
		})
	}
}

func TestPushRotationAndDisableNeverAdoptExistingEndpoints(t *testing.T) {
	m := openNotificationTest(t, Options{})
	preparePushTest(t, m)
	id := subscribePushTest(t, m, 1, "https://axv.example")
	if _, err := m.TestPush(id, "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	p := m.PushSnapshot()
	oldKey := p.KeyID
	if _, err := m.RotatePush(PushPrepare{PushIdentity: PushIdentity{p.StoreID, p.Revision}, Contact: "mailto:other@example.com"}); err != nil {
		t.Fatal(err)
	}
	p = m.PushSnapshot()
	if p.KeyID == oldKey || len(p.Subscriptions) != 0 || p.Contact != "mailto:other@example.com" {
		t.Fatal("rotation adopted old subscriptions")
	}
	if _, err := m.TestPush(currentPushIdentity(m, id), "https://axv.example"); !errors.Is(err, ErrConflict) {
		t.Fatal("rotated identity accepted")
	}
	d := m.Snapshot().Events[0].PushDeliveries[0]
	if d.Status != "disabled" || d.NextAttemptAt != nil {
		t.Fatal("old queued alert survived rotation")
	}
	fresh := subscribePushTest(t, m, 1, "https://axv.example")
	if _, err := m.DisablePush(fresh, "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	fresh = currentPushIdentity(m, fresh)
	if _, err := m.TestPush(fresh, "https://axv.example"); !errors.Is(err, ErrInput) {
		t.Fatal("disabled subscription still sent")
	}
	if _, err := m.UnsubscribePush(fresh, "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	if len(m.PushSnapshot().Subscriptions) != 0 {
		t.Fatal("server record retained after unsubscribe")
	}
}

func TestPushSendingRestartRetainsTargetAndBoundsUncertainRetries(t *testing.T) {
	clock := time.Now().UTC()
	calls := 0
	opts := Options{PushSender: pushTransport(func(context.Context, webpush.Subscription, string, string, []byte, time.Duration, string) webpush.Outcome {
		calls++
		return webpush.Outcome{Status: webpush.Uncertain}
	})}
	m := openNotificationTest(t, opts)
	m.now = func() time.Time { return clock }
	preparePushTest(t, m)
	id := subscribePushTest(t, m, 1, "https://axv.example")
	if _, err := m.TestPush(id, "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	next := clone(m.state)
	next.Events[0].PushDeliveries[0].Status = "sending"
	next.Events[0].PushDeliveries[0].Attempts = 1
	if err := m.save(next); err != nil {
		t.Fatal(err)
	}
	eventID := next.Events[0].ID
	opts.Home = m.options.Home
	closeNotificationTest(t, m)
	m = openNotificationTest(t, opts)
	d := m.Snapshot().Events[0].PushDeliveries[0]
	if d.Status != "uncertain" || d.NextAttemptAt == nil || d.SubscriptionID != id.SubscriptionID || m.Snapshot().Events[0].ID != eventID {
		t.Fatal("restart retargeted sending receipt")
	}
	clock = *d.NextAttemptAt
	m.now = func() time.Time { return clock }
	m.dispatchPush(context.Background())
	clock = clock.Add(3 * time.Minute)
	m.dispatchPush(context.Background())
	clock = clock.Add(3 * time.Minute)
	m.dispatchPush(context.Background())
	d = m.Snapshot().Events[0].PushDeliveries[0]
	if calls != 2 || d.Attempts != 3 || d.NextAttemptAt != nil || d.Status != "uncertain" {
		t.Fatalf("unbounded uncertain retry: calls=%d %+v", calls, d)
	}
}

func TestPushDurableClaimAndCompletionFailuresNeverDuplicateAcceptedSend(t *testing.T) {
	for _, phase := range []string{"claim", "completion"} {
		t.Run(phase, func(t *testing.T) {
			calls := 0
			opts := Options{PushSender: pushTransport(func(context.Context, webpush.Subscription, string, string, []byte, time.Duration, string) webpush.Outcome {
				calls++
				return webpush.Outcome{Status: webpush.Accepted}
			})}
			m := openNotificationTest(t, opts)
			preparePushTest(t, m)
			id := subscribePushTest(t, m, 1, "https://axv.example")
			if _, err := m.TestPush(id, "https://axv.example"); err != nil {
				t.Fatal(err)
			}
			injected := false
			m.write = func(path string, v any, mode os.FileMode) error {
				if err := jsonfile.Write(path, v, mode); err != nil {
					return err
				}
				s := v.(diskState)
				d := s.Events[0].PushDeliveries[0]
				if !injected && (phase == "claim" && d.Status == "sending" || phase == "completion" && d.Status == "accepted") {
					injected = true
					return errors.New("fixture publication sync failure")
				}
				return nil
			}
			m.dispatchPush(context.Background())
			want := 1
			if phase == "claim" {
				want = 0
			}
			if calls != want || !injected || m.completion == nil || m.Snapshot().Error == "" {
				t.Fatalf("failure lost recovery or sent too early: calls=%d", calls)
			}
			m.collect(context.Background())
			m.dispatchPush(context.Background())
			m.dispatchPush(context.Background())
			if calls != 1 || m.completion != nil || m.Snapshot().Events[0].PushDeliveries[0].Status != "accepted" {
				t.Fatal("completion replayed accepted delivery")
			}
			opts.Home = m.options.Home
			closeNotificationTest(t, m)
			m = openNotificationTest(t, opts)
			m.dispatchPush(context.Background())
			if calls != 1 {
				t.Fatal("restart replayed accepted delivery")
			}
		})
	}
}

func TestPushDisableCancelsOnlyExactInFlightSubscription(t *testing.T) {
	started := make(chan struct{})
	done := make(chan struct{})
	m := openNotificationTest(t, Options{PushSender: pushTransport(func(ctx context.Context, _ webpush.Subscription, _, _ string, _ []byte, _ time.Duration, _ string) webpush.Outcome {
		close(started)
		<-ctx.Done()
		return webpush.Outcome{Status: webpush.Uncertain}
	})})
	preparePushTest(t, m)
	id := subscribePushTest(t, m, 1, "https://axv.example")
	if _, err := m.TestPush(id, "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	go func() { m.dispatchPush(context.Background()); close(done) }()
	<-started
	if _, err := m.DisablePush(id, "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disable did not cancel exact send")
	}
	d := m.Snapshot().Events[0].PushDeliveries[0]
	if d.Status != "uncertain" || d.NextAttemptAt != nil {
		t.Fatal("disable promised retraction or left retry")
	}
}

func TestPushPendingReceiptsAreProtectedFromRetentionEviction(t *testing.T) {
	m := openNotificationTest(t, Options{})
	preparePushTest(t, m)
	id := subscribePushTest(t, m, 1, "https://axv.example")
	for i := 0; i < maxEvents; i++ {
		if _, err := m.TestPush(id, "https://axv.example"); err != nil {
			t.Fatal(err)
		}
	}
	first := m.state.Events[0].ID
	if _, err := m.TestPush(id, "https://axv.example"); !errors.Is(err, ErrStore) {
		t.Fatal("pending push receipts evicted to admit another test")
	}
	if m.state.Events[0].ID != first || len(m.state.Events) != maxEvents {
		t.Fatal("pending outbox changed")
	}
}

func TestPushWaitingCoalescesAcrossBrowsersAndResubscribeDoesNotAdoptHistory(t *testing.T) {
	clock := time.Now().UTC()
	batch := notificationevent.Batch{SourceID: "codex:run", StoreID: "work-store", First: 1, Next: 1, Events: []notificationevent.Event{notificationSourceEvent(1, "waiting_input", "waiting-1", "work-store", clock.Add(time.Second))}}
	m := openNotificationTest(t, Options{Sources: []Source{{Name: "codex", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
		return []notificationevent.Batch{batch}, nil
	}}}})
	m.now = func() time.Time { return clock }
	preparePushTest(t, m)
	a := subscribePushTest(t, m, 1, "https://axv.example")
	subscribePushTest(t, m, 2, "https://axv.example")
	m.collect(context.Background())
	old := m.Snapshot().Events[0]
	batch.Next = 2
	batch.Events = append(batch.Events, notificationSourceEvent(2, "waiting_input", "waiting-2", "work-store", clock.Add(2*time.Second)))
	m.collect(context.Background())
	var previous Notification
	for _, e := range m.Snapshot().Events {
		if e.ID == old.ID {
			previous = e
		}
	}
	if len(previous.PushDeliveries) != 2 {
		t.Fatal("missing fanout receipts")
	}
	for _, d := range previous.PushDeliveries {
		if d.Status != "superseded" || d.NextAttemptAt != nil {
			t.Fatal("Pocket-disabled coalescing left old waiting delivery")
		}
	}
	if _, err := m.UnsubscribePush(currentPushIdentity(m, a), "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(3 * time.Second)
	fresh := subscribePushTest(t, m, 1, "https://axv.example")
	m.collect(context.Background())
	if fresh.SubscriptionID == a.SubscriptionID {
		t.Fatal("reenrollment reused subscription identity")
	}
	for _, e := range m.Snapshot().Events {
		for _, d := range e.PushDeliveries {
			if d.SubscriptionID == fresh.SubscriptionID {
				t.Fatal("new browser adopted retained prior observations")
			}
		}
	}
}

func TestPushExpiredFullQueueCanAdmitNewObservation(t *testing.T) {
	clock := time.Now().UTC()
	m := openNotificationTest(t, Options{})
	m.now = func() time.Time { return clock }
	preparePushTest(t, m)
	id := subscribePushTest(t, m, 1, "https://axv.example")
	next := clone(m.state)
	for i := 0; i < maxEvents; i++ {
		next.Events = append(next.Events, Notification{ID: fmt.Sprintf("%032x", i+1), CreatedAt: clock, Kind: "test", State: "test", Status: "disabled", PushDeliveries: []PushDelivery{{SubscriptionID: id.SubscriptionID, KeyID: id.KeyID, Status: "pending", ExpiresAt: clock.Add(pushTTL)}}})
	}
	if err := m.save(next); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(pushTTL + time.Second)
	if _, err := m.TestPush(id, "https://axv.example"); err != nil {
		t.Fatalf("expired receipt capacity could not recover: %v", err)
	}
	if len(m.state.Events) != maxEvents || m.state.Events[0].ID == fmt.Sprintf("%032x", 1) || m.state.Events[len(m.state.Events)-1].PushDeliveries[0].Status != "pending" {
		t.Fatal("expiry evicted wrong record or did not admit test")
	}
}

func TestPushFanoutCapacityReservesRetrySpaceAndReportsGap(t *testing.T) {
	clock := time.Now().UTC()
	m := openNotificationTest(t, Options{})
	m.now = func() time.Time { return clock }
	preparePushTest(t, m)
	for i := 1; i <= maxPushSubscriptions; i++ {
		subscribePushTest(t, m, i, "https://axv.example")
	}
	before := m.PushSnapshot()
	if _, err := m.SubscribePush(PushSubscribe{PushIdentity: PushIdentity{before.StoreID, before.Revision}, KeyID: before.KeyID, InstallationID: strings.Repeat("f", 32), Label: "one too many", EventTypes: []string{"agent_failed"}, Subscription: pushFixtureSubscription(t, "overflow")}, "https://axv.example"); !errors.Is(err, ErrInput) {
		t.Fatal("unbounded subscriptions")
	}
	next := clone(m.state)
	for i := 0; i < maxSources; i++ {
		next.Checkpoints[fmt.Sprintf("source-%04d-", i)+strings.Repeat("s", 244)] = checkpoint{StoreID: strings.Repeat("t", 128), Sequence: 1}
	}
	admitted := 0
	for i := 1; i <= maxEvents; i++ {
		e := Notification{ID: fmt.Sprintf("%032x", i), CreatedAt: clock, Kind: "agent_failed", State: "failed", Status: "disabled", Target: notificationSourceEvent(1, "failed", "event", "work-store", clock).Target, PushDeliveries: pushForEvent(next.WebPush, "agent_failed", clock)}
		if !m.admit(&next, e) {
			break
		}
		admitted++
	}
	if admitted == 0 || admitted >= maxEvents || !next.Intake.Gap || len(next.Events) != admitted {
		t.Fatalf("byte budget did not bound fanout: %d", admitted)
	}
	if !validState(next) {
		t.Fatal("budget rejection corrupted metadata")
	}
	if err := m.save(next); err != nil {
		t.Fatalf("capacity gap poisoned writer: %v", err)
	}
	// All in-flight receipts can gain their largest retry fields without making
	// the accepted state unwritable or losing the durable intake checkpoint.
	at := clock.Add(time.Minute)
	for i := range next.Events {
		for j := range next.Events[i].PushDeliveries {
			d := &next.Events[i].PushDeliveries[j]
			d.Status = "uncertain"
			d.Error = "response_uncertain"
			d.NextAttemptAt = &at
			d.Attempts = 1
		}
	}
	if err := m.save(next); err != nil {
		t.Fatalf("bounded provider failures overflowed accepted outbox: %v", err)
	}
	clock = clock.Add(pushTTL + time.Second)
	m.collect(context.Background())
	if m.Snapshot().Error != "" {
		t.Fatal("expiry failed to recover bounded intake")
	}
}

func TestPocketClaimRecoveryPreservesConcurrentPushCancellation(t *testing.T) {
	for _, change := range []string{"disable", "rotate"} {
		t.Run(change, func(t *testing.T) {
			clock := time.Now().UTC()
			entered := make(chan struct{})
			release := make(chan struct{})
			done := make(chan struct{})
			pushCalls := 0
			batch := notificationevent.Batch{SourceID: "codex:run", StoreID: "work-store", First: 1, Next: 1, Events: []notificationevent.Event{notificationSourceEvent(1, "failed", "original", "work-store", clock.Add(time.Second))}}
			m := openNotificationTest(t, Options{Sources: []Source{{Name: "codex", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
				return []notificationevent.Batch{batch}, nil
			}}}, Resolve: func(_ context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
				close(entered)
				<-release
				return notificationevent.Resolution{Available: true, Current: true, State: "failed", Target: target}, nil
			}, PushSender: pushTransport(func(context.Context, webpush.Subscription, string, string, []byte, time.Duration, string) webpush.Outcome {
				pushCalls++
				return webpush.Outcome{Status: webpush.Accepted}
			})})
			m.now = func() time.Time { return clock }
			enableNotificationTest(t, m)
			preparePushTest(t, m)
			id := subscribePushTest(t, m, 1, "https://axv.example")
			m.collect(context.Background())
			injected := false
			m.write = func(path string, v any, mode os.FileMode) error {
				if err := jsonfile.Write(path, v, mode); err != nil {
					return err
				}
				next := v.(diskState)
				if !injected && len(next.Events) == 1 && next.Events[0].Status == "sending" {
					injected = true
					return errors.New("fixture claim directory sync failure")
				}
				return nil
			}
			go func() { m.dispatch(context.Background()); close(done) }()
			<-entered
			if change == "disable" {
				if _, err := m.DisablePush(id, "https://axv.example"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := m.RotatePush(PushPrepare{PushIdentity: id.PushIdentity, Contact: "mailto:owner@example.com"}); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			<-done
			if !injected || m.completion == nil {
				t.Fatal("claim publication failure was not injected")
			}
			m.collect(context.Background())
			m.dispatchPush(context.Background())
			d := m.Snapshot().Events[0].PushDeliveries[0]
			if d.Status != "disabled" || d.NextAttemptAt != nil || pushCalls != 0 {
				t.Fatalf("Pocket recovery resurrected another adapter's receipt: %+v calls=%d", d, pushCalls)
			}
		})
	}
}

func TestPushSubsecondTTLExpiresBeforeProviderAttempt(t *testing.T) {
	clock := time.Now().UTC()
	calls := 0
	m := openNotificationTest(t, Options{PushSender: pushTransport(func(context.Context, webpush.Subscription, string, string, []byte, time.Duration, string) webpush.Outcome {
		calls++
		return webpush.Outcome{Status: webpush.Accepted}
	})})
	m.now = func() time.Time { return clock }
	preparePushTest(t, m)
	id := subscribePushTest(t, m, 1, "https://axv.example")
	if _, err := m.TestPush(id, "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(pushTTL - time.Second/2)
	m.dispatchPush(context.Background())
	d := m.Snapshot().Events[0].PushDeliveries[0]
	if calls != 0 || d.Status != "expired" || d.Error != "ttl_expired" || d.Attempts != 0 {
		t.Fatalf("subsecond TTL became a provider rejection: %+v calls=%d", d, calls)
	}
}

func TestPushSnapshotFingerprintMatchesActualBrowserSubscriptionWithoutPersistingSecrets(t *testing.T) {
	m := openNotificationTest(t, Options{PushSender: pushTransport(func(context.Context, webpush.Subscription, string, string, []byte, time.Duration, string) webpush.Outcome {
		return webpush.Outcome{Status: webpush.Expired}
	})})
	prepared := preparePushTest(t, m)
	subscription := webpush.Subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/synthetic", Keys: webpush.Keys{P256DH: "BGsX0fLhLEJH-Lzm5WOkQPJ3A32BLeszoPShOUXYmMKWT-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU", Auth: "MDEyMzQ1Njc4OWFiY2RlZg"}}
	snapshot, err := m.SubscribePush(PushSubscribe{PushIdentity: PushIdentity{prepared.StoreID, prepared.Revision}, KeyID: prepared.KeyID, InstallationID: strings.Repeat("a", 32), Label: "Synthetic browser", EventTypes: []string{"agent_failed"}, Subscription: subscription}, "https://axv.example")
	if err != nil {
		t.Fatal(err)
	}
	const expected = "ab11b7897018eb2cb5145542821594115a745f51c54ab70dd8ec1c323b964a63"
	if len(snapshot.Subscriptions) != 1 || snapshot.Subscriptions[0].Fingerprint != expected {
		t.Fatal("fingerprint differs from the browser's canonical ASCII/NUL vector")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{subscription.Endpoint, subscription.Keys.P256DH, subscription.Keys.Auth, m.state.WebPush.PrivateKey} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("subscription comparison exposed raw enrollment material")
		}
	}
	stored, err := os.ReadFile(m.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), `"fingerprint"`) || m.state.WebPush.Subscriptions[0].Fingerprint != "" {
		t.Fatal("derived response fingerprint entered persistent state")
	}
	forged := clone(m.state)
	forged.WebPush.Subscriptions[0].Fingerprint = expected
	if validState(forged) {
		t.Fatal("persisted comparison digest was accepted as store identity")
	}
	// A refreshed browser endpoint or either encryption key must change the
	// comparison digest even when its browser installation ID remains unchanged.
	for _, field := range []string{"endpoint", "p256dh", "auth"} {
		t.Run(field, func(t *testing.T) {
			original := m.state.WebPush.Subscriptions[0].Subscription
			changed := original
			switch field {
			case "endpoint":
				changed.Endpoint += "-refreshed"
			case "p256dh":
				_, changed.Keys.P256DH, err = webpush.GenerateVAPIDKeys()
				if err != nil {
					t.Fatal(err)
				}
			case "auth":
				changed.Keys.Auth = base64.RawURLEncoding.EncodeToString([]byte("fedcba9876543210"))
			}
			m.state.WebPush.Subscriptions[0].Subscription = changed
			if m.PushSnapshot().Subscriptions[0].Fingerprint == expected {
				t.Fatal("refreshed subscription retained the old comparison digest")
			}
			m.state.WebPush.Subscriptions[0].Subscription = original
		})
	}
	row := snapshot.Subscriptions[0]
	identity := PushSubscriptionIdentity{PushIdentity: PushIdentity{snapshot.StoreID, snapshot.Revision}, KeyID: snapshot.KeyID, InstallationID: row.InstallationID, SubscriptionID: row.ID}
	if _, err = m.TestPush(identity, "https://axv.example"); err != nil {
		t.Fatal(err)
	}
	m.dispatchPush(context.Background())
	retired := m.PushSnapshot()
	if retired.Subscriptions[0].Status != "expired" || retired.Subscriptions[0].Fingerprint != "" {
		t.Fatal("expired subscription retained a live comparison digest")
	}
}

func TestPushUnsupportedServiceHasDistinctSafeOutcomeWithoutEnrollment(t *testing.T) {
	calls := 0
	m := openNotificationTest(t, Options{PushSender: pushTransport(func(context.Context, webpush.Subscription, string, string, []byte, time.Duration, string) webpush.Outcome {
		calls++
		return webpush.Outcome{Status: webpush.Accepted}
	})})
	prepared := preparePushTest(t, m)
	subscription := pushFixtureSubscription(t, "unsupported")
	subscription.Endpoint = "https://synthetic.notify.windows.com/w/?opaque-private-capability"
	_, err := m.SubscribePush(PushSubscribe{PushIdentity: PushIdentity{prepared.StoreID, prepared.Revision}, KeyID: prepared.KeyID, InstallationID: strings.Repeat("a", 32), Label: "Synthetic browser", EventTypes: []string{"agent_failed"}, Subscription: subscription}, "https://axv.example")
	if !errors.Is(err, ErrPushService) || strings.Contains(err.Error(), subscription.Endpoint) {
		t.Fatal("unsupported service was not distinguished safely")
	}
	m.dispatchPush(context.Background())
	after := m.PushSnapshot()
	if calls != 0 || len(after.Subscriptions) != 0 || after.Revision != prepared.Revision || len(m.Snapshot().Events) != 0 {
		t.Fatal("unsupported enrollment changed state or contacted a provider")
	}
}
