package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func TestVMCodexNotificationSourcesAndResolution(t *testing.T) {
	observed := time.Date(2026, 11, 28, 1, 0, 0, 0, time.UTC)
	socket := filepath.Join(shortSocketDirectory(t), "vm-codex-notif.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			_ = json.NewEncoder(w).Encode(compute.Runtime{
				Ready:                true,
				HostID:               "host-vm-notif",
				ProtocolVersion:      compute.ProtocolVersion,
				ConfigFingerprint:    "host-vm-notif-config",
				VMCodexNotifications: true,
			})
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/machines":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"machines": []compute.Machine{
					{
						ID:                "vm-ready",
						HostID:            "host-vm-notif",
						Spec:              compute.Spec{Name: "Dev Box"},
						Status:            "running",
						Ready:             true,
						GuestCapabilities: []string{"exec", "terminals", "codex"},
					},
					{
						ID:                "vm-stopped",
						HostID:            "host-vm-notif",
						Spec:              compute.Spec{Name: "Stopped Box"},
						Status:            "stopped",
						Ready:             false,
						GuestCapabilities: []string{"exec", "codex"},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/machines/vm-ready/guest/codex/notifications":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"batches": []notificationevent.Batch{
					{
						SourceID: "codex:run-1",
						StoreID:  "guest-db-1",
						First:    1,
						Next:     1,
						Events: []notificationevent.Event{
							{
								ID:         "evt-1",
								Sequence:   1,
								ObservedAt: observed,
								State:      "waiting_approval",
								Target: notificationevent.Target{
									Kind:      "codex",
									StoreID:   "guest-db-1",
									SessionID: "sess-1",
									RunID:     "run-1",
									ThreadID:  "thread-1",
									EventID:   "evt-1",
								},
							},
						},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/machines/vm-ready/guest/codex/notifications/resolve":
			var target notificationevent.Target
			if err := json.NewDecoder(r.Body).Decode(&target); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(notificationevent.Resolution{
				Available: true,
				Current:   true,
				State:     "waiting_approval",
				Target:    target,
			})
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	g := NewGateway("", GatewayOptions{})
	t.Cleanup(g.Close)
	g.Bind(socket, "host-vm-notif", "host-vm-notif-config")

	batches, err := g.VMCodexNotificationSources(context.Background(), nil)
	if err != nil {
		t.Fatalf("VMCodexNotificationSources: %v", err)
	}
	if len(batches) != 1 || len(batches[0].Events) != 1 {
		t.Fatalf("expected 1 VM codex batch with 1 event, got %+v", batches)
	}
	got := batches[0].Events[0].Target
	if got.HostID != "host-vm-notif" || got.MachineID != "vm-ready" || got.MachineName != "Dev Box" {
		t.Fatalf("expected enriched VM target, got %+v", got)
	}
	if batches[0].SourceID != "vm-codex:host-vm-notif:vm-ready:codex:run-1" {
		t.Fatalf("unexpected SourceID %q", batches[0].SourceID)
	}

	res, err := g.ResolveVMCodexNotification(context.Background(), got)
	if err != nil {
		t.Fatalf("ResolveVMCodexNotification: %v", err)
	}
	if !res.Available || !res.Current || res.Target.MachineID != "vm-ready" {
		t.Fatalf("unexpected resolution: %+v", res)
	}
}
