package gateway

import (
	"context"
	"slices"

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

// ConnectHost captures a verified route for operations scoped to one host identity.
func (g *Gateway) ConnectHost(ctx context.Context, hostID string) (*computeclient.Client, error) {
	g.mu.Lock()
	binding, closed := g.binding, g.closed
	g.mu.Unlock()
	if closed || binding == nil {
		return nil, compute.Problem("host_unavailable", "The original VM host is unavailable.")
	}
	if hostID != "" && binding.hostID != "" && binding.hostID != hostID {
		return nil, compute.Problem("binding_changed", "The operation belongs to another VM host.")
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
	client, err := g.ConnectHost(ctx, "")
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
	client, err := g.ConnectHost(ctx, target.HostID)
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

// VMCodexNotificationSources polls Codex notification batches from running,
// ready VMs that declare the "codex" guest capability. Stopped or transiently
// unreachable VMs are skipped without failing desktop notification intake.
func (g *Gateway) VMCodexNotificationSources(ctx context.Context, _ map[string]uint64) ([]notificationevent.Batch, error) {
	if !g.HasNotificationSource() {
		return nil, nil
	}
	client, err := g.ConnectHost(ctx, "")
	if err != nil {
		return nil, nil
	}
	defer client.Close()
	if !client.SupportsVMCodexNotifications {
		return nil, nil
	}
	machines, err := client.ListMachines(ctx)
	if err != nil {
		return nil, nil
	}
	hostID := client.Binding.HostID
	var batches []notificationevent.Batch
	for _, machine := range machines {
		if machine.Status != "running" || !machine.Ready || !slices.Contains(machine.GuestCapabilities, "codex") {
			continue
		}
		vmBatches, err := client.VMCodexNotifications(ctx, machine.ID)
		if err != nil {
			continue
		}
		name := machine.Spec.Name
		if name == "" {
			name = machine.ID
		}
		for _, batch := range vmBatches {
			batch.SourceID = "vm-codex:" + hostID + ":" + machine.ID + ":" + batch.SourceID
			for i := range batch.Events {
				batch.Events[i].Target.HostID = hostID
				batch.Events[i].Target.MachineID = machine.ID
				batch.Events[i].Target.MachineName = name
			}
			batches = append(batches, batch)
		}
	}
	return batches, nil
}

// ResolveVMCodexNotification verifies an exact VM Codex notification target.
func (g *Gateway) ResolveVMCodexNotification(ctx context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
	if target.Kind != "codex" || target.MachineID == "" || target.HostID == "" {
		return notificationevent.Resolution{Target: target}, nil
	}
	client, err := g.ConnectHost(ctx, target.HostID)
	if err != nil {
		return notificationevent.Resolution{Target: target}, nil
	}
	defer client.Close()
	result, err := client.ResolveVMCodexNotification(ctx, target)
	if err != nil {
		return notificationevent.Resolution{Target: target}, nil
	}
	return result, nil
}
