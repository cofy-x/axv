package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	"github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
	"github.com/cofy-x/axv/apps/server/internal/notifications"
)

func workstationNotifications(ctx context.Context, home string, agents *codex.Manager, compute *gateway.Gateway) (*notifications.Manager, error) {
	return notifications.Open(ctx, notifications.Options{
		Home: home,
		Sources: []notifications.Source{
			{Name: "codex", Read: agents.NotificationSources},
			{Name: "compute", Read: func(ctx context.Context, cursors map[string]uint64) ([]notificationevent.Batch, error) {
				if !compute.HasNotificationSource() {
					for key := range cursors {
						if strings.HasPrefix(key, "compute:") {
							return nil, notifications.ErrMissing
						}
					}
					return nil, nil
				}
				// Discover the currently verified host before applying its own checkpoint.
				// A checkpoint from another host can never skip this host's retained events.
				batch, err := compute.NotificationEvents(ctx, 0)
				if err != nil {
					return nil, err
				}
				if after := cursors[batch.SourceID]; after != 0 {
					original := batch
					batch, err = compute.NotificationEvents(ctx, after)
					if err == nil && (batch.SourceID != original.SourceID || batch.StoreID != original.StoreID) {
						return nil, notifications.ErrConflict
					}
				}
				if err != nil {
					return nil, err
				}
				return []notificationevent.Batch{batch}, nil
			}},
		},
		Resolve: func(ctx context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
			switch target.Kind {
			case "codex":
				return agents.ResolveNotification(ctx, target)
			case "task":
				return compute.ResolveNotification(ctx, target)
			default:
				return notificationevent.Resolution{Target: target}, nil
			}
		},
	})
}

func notificationsUnavailable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("{\"code\":\"notification_store_unavailable\",\"error\":\"Notification storage could not be opened. Existing records were preserved.\"}\n"))
}
