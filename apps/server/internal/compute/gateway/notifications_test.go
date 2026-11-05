package gateway

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func TestNotificationGatewayCapturesFeedAndRejectsReplacementTarget(t *testing.T) {
	dir := shortSocketDirectory(t)
	firstSocket, nextSocket := filepath.Join(dir, "notify-first.sock"), filepath.Join(dir, "notify-next.sock")
	entered, release := make(chan struct{}), make(chan struct{})
	target := notificationevent.Target{Kind: "task", StoreID: "first", HostID: "first", ProjectID: "project", TaskID: "task", AttemptID: "original", EventID: "event"}
	bindingHost(t, firstSocket, "first", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AXV-Host-ID") != "first" {
			t.Error("notification request lost its captured host")
		}
		switch r.URL.Path {
		case "/v1/task-events":
			if r.URL.Query().Get("after") != "7" {
				t.Errorf("notification cursor changed: %s", r.URL.RawQuery)
			}
			close(entered)
			<-release
			_ = json.NewEncoder(w).Encode(notificationevent.Batch{SourceID: "compute:first", StoreID: "first", First: 1, Next: 8, Events: []notificationevent.Event{{ID: "event", Sequence: 8, State: "failed", Target: target}}})
		case "/v1/projects/project/tasks/task/attempts/original/status":
			_ = json.NewEncoder(w).Encode(notificationevent.Resolution{Available: true, State: "failed", Target: target})
		default:
			t.Errorf("unexpected notification route: %s", r.URL.Path)
		}
	})
	bindingHost(t, nextSocket, "next", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("old notification reached a replacement host")
		w.WriteHeader(http.StatusBadRequest)
	})
	gateway := NewGateway("", GatewayOptions{})
	defer gateway.Close()
	gateway.Bind(firstSocket, "first", "first-config")
	resolved, err := gateway.ResolveNotification(t.Context(), target)
	if err != nil || !resolved.Available || resolved.Current || resolved.State != "failed" || resolved.Target != target {
		t.Fatalf("gateway did not retain the historical attempt: %+v %v", resolved, err)
	}
	type readResult struct {
		batch notificationevent.Batch
		err   error
	}
	done := make(chan readResult, 1)
	go func() {
		batch, err := gateway.NotificationEvents(t.Context(), 7)
		done <- readResult{batch, err}
	}()
	<-entered
	gateway.Bind(nextSocket, "next", "next-config")
	close(release)
	read := <-done
	if read.err != nil || read.batch.StoreID != "first" || read.batch.Next != 8 || len(read.batch.Events) != 1 || read.batch.Events[0].Target != target {
		t.Fatalf("in-flight feed was retargeted: %+v %v", read.batch, read.err)
	}
	if _, err := gateway.ResolveNotification(t.Context(), target); compute.ErrorCode(err) != "binding_changed" {
		t.Fatalf("old attempt reached a replacement route: %v", err)
	}
	gateway.Close()
	if _, err := gateway.NotificationEvents(t.Context(), 7); compute.ErrorCode(err) != "host_unavailable" {
		t.Fatalf("closed gateway concealed source unavailability: %v", err)
	}
}
