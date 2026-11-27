package client

import (
	"context"
	"math"
	"net/url"
	"strconv"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func (c *Client) ListMachines(ctx context.Context) ([]compute.Machine, error) {
	var result struct {
		Machines []compute.Machine `json:"machines"`
	}
	if err := c.DoJSON(ctx, "GET", "machines", nil, &result); err != nil {
		return nil, err
	}
	return result.Machines, nil
}

func (c *Client) VMCodexNotifications(ctx context.Context, machineID string) ([]notificationevent.Batch, error) {
	var result struct {
		Batches []notificationevent.Batch `json:"batches"`
	}
	if err := c.DoJSON(ctx, "GET", "machines/"+url.PathEscape(machineID)+"/guest/codex/notifications", nil, &result); err != nil {
		return nil, err
	}
	return result.Batches, nil
}

func (c *Client) ResolveVMCodexNotification(ctx context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
	result := notificationevent.Resolution{Target: target}
	if target.Kind != "codex" || target.MachineID == "" || target.HostID != c.Binding.HostID {
		return result, compute.Problem("binding_changed", "The notification belongs to another VM host.")
	}
	err := c.DoJSON(ctx, "POST", "machines/"+url.PathEscape(target.MachineID)+"/guest/codex/notifications/resolve", target, &result)
	result.Target = target
	return result, err
}

func (c *Client) NotificationEvents(ctx context.Context, after uint64) (notificationevent.Batch, error) {
	var result notificationevent.Batch
	if after > math.MaxInt64 {
		return result, compute.Problem("invalid_request", "The task event cursor is outside its supported range.")
	}
	err := c.DoJSON(ctx, "GET", "task-events?after="+strconv.FormatUint(after, 10), nil, &result)
	return result, err
}

func (c *Client) ResolveNotification(ctx context.Context, target notificationevent.Target) (notificationevent.Resolution, error) {
	result := notificationevent.Resolution{Target: target}
	if target.Kind != "task" || target.StoreID != c.Binding.HostID || target.HostID != c.Binding.HostID {
		return result, compute.Problem("binding_changed", "The notification belongs to another task store or host.")
	}
	path, err := taskPath(target.ProjectID, target.TaskID, "attempts", target.AttemptID)
	if err != nil {
		return result, err
	}
	if target.ProjectID == "" || target.TaskID == "" || target.AttemptID == "" {
		return result, compute.Problem("invalid_request", "The original project, task and attempt identities are required.")
	}
	err = c.DoJSON(ctx, "GET", path+"/status", nil, &result)
	result.Target = target
	return result, err
}
