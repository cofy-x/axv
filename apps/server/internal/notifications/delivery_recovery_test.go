package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func TestPartiallyPublishedSendingClaimRecoversBeforeAnyHTTPRequest(t *testing.T) {
	for _, status := range []string{"pending", "uncertain"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			clock := time.Now().UTC()
			calls := 0
			manager := openNotificationTest(t, Options{Client: &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return notificationResponse(http.StatusCreated, nil), nil
			})}})
			manager.now = func() time.Time { return clock }
			enableNotificationTest(t, manager)
			snapshot := manager.Snapshot()
			if err := manager.Test(snapshot.StoreID, snapshot.Revision); err != nil {
				t.Fatal(err)
			}
			if status == "uncertain" {
				next := clone(manager.state)
				retry := clock.Add(-time.Minute)
				next.Events[0].Status, next.Events[0].Error = "uncertain", "response_uncertain"
				next.Events[0].Attempts, next.Events[0].NextAttemptAt = 1, &retry
				if err := manager.save(next); err != nil {
					t.Fatal(err)
				}
			}
			original := manager.Snapshot().Events[0]
			injected := false
			manager.write = func(path string, value any, mode os.FileMode) error {
				if err := jsonfile.Write(path, value, mode); err != nil {
					return err
				}
				if !injected {
					injected = true
					return errors.New("fixture directory sync failed after publication")
				}
				return nil
			}
			manager.dispatch(ctx)
			if !injected || calls != 0 || manager.completion == nil || manager.Snapshot().Error == "" {
				t.Fatalf("failed claim publication started delivery or orphaned its recovery: injected=%v calls=%d recovery=%v state=%+v", injected, calls, manager.completion != nil, manager.Snapshot())
			}
			manager.collect(ctx)
			recovered := manager.Snapshot()
			if manager.completion != nil || recovered.Error != "" || len(recovered.Events) != 1 || recovered.Events[0].ID != original.ID || recovered.Events[0].Status != original.Status || recovered.Events[0].Attempts != original.Attempts || recovered.Events[0].Error != original.Error {
				t.Fatalf("unsent claim recovery changed original retry state: original=%+v recovered=%+v", original, recovered)
			}
			if original.NextAttemptAt == nil {
				if recovered.Events[0].NextAttemptAt != nil {
					t.Fatal("unsent pending claim acquired a retry delay")
				}
			} else if recovered.Events[0].NextAttemptAt == nil || !recovered.Events[0].NextAttemptAt.Equal(*original.NextAttemptAt) {
				t.Fatal("unsent uncertain claim lost its original retry delay")
			}
			manager.dispatch(ctx)
			after := manager.Snapshot()
			if calls != 1 || after.Events[0].Status != "accepted" || after.Events[0].Attempts != original.Attempts+1 || after.Error != "" {
				t.Fatalf("recovered claim could not complete one delivery: calls=%d state=%+v", calls, after)
			}
		})
	}
}

func TestPartiallyPublishedAcceptedOutcomeDoesNotResend(t *testing.T) {
	ctx := context.Background()
	calls := 0
	options := Options{Client: &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return notificationResponse(http.StatusCreated, nil), nil
	})}}
	manager := openNotificationTest(t, options)
	enableNotificationTest(t, manager)
	before := manager.Snapshot()
	if err := manager.Test(before.StoreID, before.Revision); err != nil {
		t.Fatal(err)
	}
	originalID := manager.Snapshot().Events[0].ID
	injected := false
	manager.write = func(path string, value any, mode os.FileMode) error {
		if err := jsonfile.Write(path, value, mode); err != nil {
			return err
		}
		next := value.(diskState)
		if !injected && len(next.Events) == 1 && next.Events[0].Status == "accepted" {
			injected = true
			return errors.New("fixture directory sync failed after acceptance publication")
		}
		return nil
	}
	manager.dispatch(ctx)
	if !injected || calls != 1 || manager.Snapshot().Error == "" {
		t.Fatalf("partial acceptance publication was not surfaced: injected=%v calls=%d state=%+v", injected, calls, manager.Snapshot())
	}
	manager.collect(ctx)
	manager.dispatch(ctx)
	after := manager.Snapshot()
	if calls != 1 || manager.completion != nil || after.Error != "" || len(after.Events) != 1 || after.Events[0].ID != originalID || after.Events[0].Status != "accepted" || after.Events[0].Attempts != 1 {
		t.Fatalf("partially published acceptance was resent or abandoned: calls=%d state=%+v", calls, after)
	}
	options.Home = manager.options.Home
	closeNotificationTest(t, manager)
	reopened := openNotificationTest(t, options)
	reopened.dispatch(ctx)
	if snapshot := reopened.Snapshot(); calls != 1 || snapshot.Events[0].ID != originalID || snapshot.Events[0].Status != "accepted" || snapshot.Events[0].Attempts != 1 {
		t.Fatalf("restart forgot a recovered acceptance: calls=%d state=%+v", calls, snapshot)
	}
}

func TestDeliveryCompletionWriteFailureRecoversWithoutManagerRestart(t *testing.T) {
	for _, outcome := range []string{"accepted", "uncertain"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := context.Background()
			clock := time.Now().UTC()
			var manager *Manager
			var originalPath string
			calls, reads := 0, 0
			options := Options{
				Client: &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
					calls++
					// The durable sending claim has already committed. Fail only
					// publication of the subsequent provider outcome.
					manager.path = filepath.Dir(originalPath)
					if outcome == "accepted" {
						return notificationResponse(http.StatusCreated, nil), nil
					}
					return nil, errors.New("provider response was lost")
				})},
				Sources: []Source{{Name: "healthy", Read: func(context.Context, map[string]uint64) ([]notificationevent.Batch, error) {
					reads++
					snapshot := manager.Snapshot()
					if snapshot.Error != "" || len(snapshot.Events) != 1 || snapshot.Events[0].Status != outcome {
						t.Errorf("new intake began before completion was durably recovered: %+v", snapshot)
					}
					return nil, nil
				}}},
			}
			manager = openNotificationTest(t, options)
			manager.now = func() time.Time { return clock }
			originalPath = manager.path
			enableNotificationTest(t, manager)
			before := manager.Snapshot()
			if err := manager.Test(before.StoreID, before.Revision); err != nil {
				t.Fatal(err)
			}
			originalID := manager.Snapshot().Events[0].ID
			manager.dispatch(ctx)
			if snapshot := manager.Snapshot(); calls != 1 || snapshot.Error == "" {
				t.Fatalf("post-send persistence failure was hidden: calls=%d state=%+v", calls, snapshot)
			}
			if err := manager.Test(before.StoreID, before.Revision); !errors.Is(err, ErrStore) {
				t.Fatalf("test mutation bypassed an uncommitted provider outcome: %v", err)
			}
			config := before.Config
			if err := manager.Configure(Update{StoreID: before.StoreID, Revision: before.Revision, Enabled: config.Enabled,
				TokenAction: "keep", ApplicationID: config.ApplicationID, DeviceID: config.DeviceID,
				ReturnOrigin: config.ReturnOrigin, EventTypes: config.EventTypes}); !errors.Is(err, ErrStore) {
				t.Fatalf("configuration bypassed an uncommitted provider outcome: %v", err)
			}
			manager.collect(ctx)
			manager.dispatch(ctx)
			if reads != 0 || calls != 1 || manager.Snapshot().Error == "" {
				t.Fatalf("failed completion was replaced by ordinary intake: reads=%d calls=%d state=%+v", reads, calls, manager.Snapshot())
			}
			manager.path = originalPath
			manager.collect(ctx)
			manager.dispatch(ctx)
			after := manager.Snapshot()
			if reads != 1 || calls != 1 || after.Error != "" || len(after.Events) != 1 || after.Events[0].ID != originalID || after.Events[0].Status != outcome || after.Events[0].Attempts != 1 {
				t.Fatalf("completion recovery orphaned sending work or repeated delivery: reads=%d calls=%d state=%+v", reads, calls, after)
			}
			if outcome == "uncertain" && (after.Events[0].NextAttemptAt == nil || !after.Events[0].NextAttemptAt.After(clock)) {
				t.Fatalf("uncertain completion lost its retry backoff: %+v", after.Events[0])
			}
			data, err := os.ReadFile(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			var durable diskState
			if err := json.Unmarshal(data, &durable); err != nil || len(durable.Events) != 1 || durable.Events[0].ID != originalID || durable.Events[0].Status != outcome || durable.Events[0].Attempts != 1 {
				t.Fatalf("recovered completion was not durable: state=%+v err=%v", durable, err)
			}
		})
	}
}

func TestQuotaCooldownBlocksOtherPendingEventsAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	clock := time.Now().UTC()
	calls := 0
	options := Options{Client: &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return notificationResponse(http.StatusTooManyRequests, http.Header{"Retry-After": {"120"}}), nil
		}
		return notificationResponse(http.StatusCreated, nil), nil
	})}}
	manager := openNotificationTest(t, options)
	manager.now = func() time.Time { return clock }
	enableNotificationTest(t, manager)
	before := manager.Snapshot()
	for range 2 {
		if err := manager.Test(before.StoreID, before.Revision); err != nil {
			t.Fatal(err)
		}
	}
	manager.dispatch(ctx)
	if calls != 1 {
		t.Fatalf("initial quota attempt count=%d", calls)
	}
	data, err := json.Marshal(manager.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var cooldown struct {
		NextSendAt *time.Time `json:"nextSendAt"`
	}
	if err := json.Unmarshal(data, &cooldown); err != nil || cooldown.NextSendAt == nil || !cooldown.NextSendAt.Equal(clock.Add(120*time.Second)) {
		t.Fatalf("adapter cooldown was not exposed: nextSendAt=%v err=%v", cooldown.NextSendAt, err)
	}
	clock = clock.Add(119 * time.Second)
	manager.dispatch(ctx)
	if calls != 1 {
		t.Fatalf("another pending notification bypassed quota cooldown: calls=%d", calls)
	}
	options.Home = manager.options.Home
	closeNotificationTest(t, manager)
	reopened := openNotificationTest(t, options)
	reopened.now = func() time.Time { return clock }
	reopened.dispatch(ctx)
	if calls != 1 {
		t.Fatalf("restart discarded adapter quota cooldown: calls=%d", calls)
	}
	clock = clock.Add(time.Second)
	reopened.dispatch(ctx)
	reopened.dispatch(ctx)
	after := reopened.Snapshot()
	if calls != 3 || len(after.Events) != 2 {
		t.Fatalf("cooldown expiry did not release both original notifications: calls=%d state=%+v", calls, after)
	}
	attempts := 0
	for _, event := range after.Events {
		if event.Status != "accepted" || event.Attempts < 1 || event.Attempts > maxAttempts || event.NextAttemptAt != nil {
			t.Fatalf("cooldown changed a bounded delivery outcome: %+v", event)
		}
		attempts += event.Attempts
	}
	if attempts != 3 {
		t.Fatalf("quota retry accounting changed across restart: attempts=%d", attempts)
	}
}

func TestOpenRejectsOversizedValidPrefixWithoutDiscardingUnreadTail(t *testing.T) {
	manager := openNotificationTest(t, Options{})
	path, home := manager.path, manager.options.Home
	closeNotificationTest(t, manager)
	prefix, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const limit = 4 << 20
	oversized := append(append([]byte{}, prefix...), bytes.Repeat([]byte(" "), limit-len(prefix))...)
	oversized = append(oversized, []byte(`{"unread":"trailing data"}`)...)
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), Options{Home: home, DisableWorker: true})
	if reopened != nil {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if !errors.Is(err, ErrStore) {
		t.Errorf("oversized store with unread trailing data was accepted: %v", err)
	}
	preserved, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(preserved, oversized) {
		t.Fatalf("rejected oversized store was changed: readErr=%v size=%d want=%d", readErr, len(preserved), len(oversized))
	}
}
