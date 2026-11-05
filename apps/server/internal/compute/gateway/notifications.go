package gateway

import (
	"context"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

// HasNotificationSource reports whether a host route is configured. It does not
// probe the host or establish that its event journal is currently reachable.
func (g *Gateway) HasNotificationSource() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.closed && g.binding != nil
}

func (g *Gateway) notificationClient(ctx context.Context, hostID string) (*computeclient.Client, error) {
	g.mu.Lock()
	binding, closed := g.binding, g.closed
	g.mu.Unlock()
	if closed || binding == nil {
		return nil, compute.Problem("host_unavailable", "The original VM host is unavailable.")
	}
	if hostID != "" && binding.hostID != "" && binding.hostID != hostID {
		return nil, compute.Problem("binding_changed", "The notification belongs to another VM host.")
	}
	if hostID == "" {
		hostID = binding.hostID
	}
	client, err := computeclient.Connect(ctx, binding.socket, computeclient.Binding{HostID: hostID, ConfigFingerprint: binding.fingerprint})
	if err != nil {
		return nil, compute.Problem("host_unavailable", "The original VM host could not be verified.")
	}
	return client, nil
}

// NotificationEvents captures one verified route for this read. A later gateway
// rebind cannot send the in-flight cursor to another host.
func (g *Gateway) NotificationEvents(ctx context.Context, after uint64) (notificationevent.Batch, error) {
	client, err := g.notificationClient(ctx, "")
	if err != nil {
		return notificationevent.Batch{}, err
	}
	defer client.Close()
	batch, err := client.NotificationEvents(ctx, after)
	if err != nil {
		return notificationevent.Batch{}, compute.Problem("host_unavailable", "The task notification source is unavailable; its checkpoint was preserved.")
	}
	return batch, nil
}

func (g *Gateway) ResolveNotification(ctx context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
	client, err := g.notificationClient(ctx, target.HostID)
	if err != nil {
		return notificationevent.Resolution{Target: target}, err
	}
	defer client.Close()
	result, err := client.ResolveNotification(ctx, target)
	if err != nil {
		return notificationevent.Resolution{Target: target}, compute.Problem("host_unavailable", "The original task status is unavailable.")
	}
	return result, nil
}
