package client

import (
	"context"
	"net/url"
	"strconv"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func taskPath(projectID string, ids ...string) (string, error) {
	if err := validProjectStrings(append([]string{projectID}, ids...)...); err != nil {
		return "", err
	}
	path := "projects/" + url.PathEscape(projectID) + "/tasks"
	for _, id := range ids {
		path += "/" + url.PathEscape(id)
	}
	return path, nil
}

func validTaskBinding(binding compute.ProjectBinding) error {
	return validProjectStrings(binding.HostID, binding.ProjectID, binding.VolumeID, binding.MachineID, binding.BootID, binding.InstanceID)
}

func (c *Client) SubmitProjectTask(ctx context.Context, projectID string, request compute.SubmitProjectTaskRequest) (compute.ProjectTaskResult, error) {
	var result compute.ProjectTaskResult
	path, err := taskPath(projectID)
	if err != nil {
		return result, err
	}
	if err = validProjectStrings(append([]string{request.RequestID, request.Spec.Directory}, request.Spec.Argv...)...); err != nil {
		return result, err
	}
	if err = validTaskBinding(request.Binding); err != nil {
		return result, err
	}
	err = c.DoJSON(ctx, "POST", path, request, &result)
	return result, err
}

func (c *Client) ProjectTasks(ctx context.Context, projectID string) ([]compute.ProjectTaskResult, error) {
	var result struct {
		Tasks []compute.ProjectTaskResult `json:"tasks"`
	}
	path, err := taskPath(projectID)
	if err != nil {
		return nil, err
	}
	err = c.DoJSON(ctx, "GET", path, nil, &result)
	return result.Tasks, err
}

func (c *Client) ProjectTask(ctx context.Context, projectID, taskID string) (compute.ProjectTaskResult, error) {
	var result compute.ProjectTaskResult
	path, err := taskPath(projectID, taskID)
	if err != nil {
		return result, err
	}
	err = c.DoJSON(ctx, "GET", path, nil, &result)
	return result, err
}

func (c *Client) ProjectTaskByRequest(ctx context.Context, projectID, requestID string) (compute.ProjectTaskResult, error) {
	var result compute.ProjectTaskResult
	if err := validProjectStrings(projectID, requestID); err != nil {
		return result, err
	}
	err := c.DoJSON(ctx, "GET", "projects/"+url.PathEscape(projectID)+"/task-requests/"+url.PathEscape(requestID), nil, &result)
	return result, err
}

func (c *Client) RetryProjectTask(ctx context.Context, projectID, taskID string, request compute.RetryProjectTaskRequest) (compute.ProjectTaskResult, error) {
	var result compute.ProjectTaskResult
	path, err := taskPath(projectID, taskID)
	if err != nil {
		return result, err
	}
	if err = validProjectStrings(request.RequestID); err != nil {
		return result, err
	}
	if err = validTaskBinding(request.Binding); err != nil {
		return result, err
	}
	err = c.DoJSON(ctx, "POST", path+"/retry", request, &result)
	return result, err
}

func (c *Client) CancelProjectTaskAttempt(ctx context.Context, projectID, taskID, attemptID string, request compute.CancelProjectTaskRequest) (compute.ProjectTaskResult, error) {
	var result compute.ProjectTaskResult
	path, err := taskPath(projectID, taskID, "attempts", attemptID)
	if err != nil {
		return result, err
	}
	if err = validTaskBinding(request.Binding); err != nil {
		return result, err
	}
	err = c.DoJSON(ctx, "POST", path+"/cancel", request, &result)
	return result, err
}

func (c *Client) ProjectTaskLogs(ctx context.Context, projectID, taskID, attemptID string, offset int64, limit int) (compute.ProjectTaskLog, error) {
	var result compute.ProjectTaskLog
	path, err := taskPath(projectID, taskID, "attempts", attemptID)
	if err != nil {
		return result, err
	}
	if offset < 0 || limit < 1 || limit > 65536 {
		return result, compute.Problem("invalid_request", "Task logs require a nonnegative byte offset and a limit of 1–65536 bytes.")
	}
	query := url.Values{"offset": {strconv.FormatInt(offset, 10)}, "limit": {strconv.Itoa(limit)}}
	err = c.DoJSON(ctx, "GET", path+"/logs?"+query.Encode(), nil, &result)
	return result, err
}
