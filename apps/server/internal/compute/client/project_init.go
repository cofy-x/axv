package client

import (
	"context"
	"net/url"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func (c *Client) InitializeProject(ctx context.Context, projectID string, req compute.InitializeProjectRequest) (compute.ProjectTaskResult, error) {
	var result compute.ProjectTaskResult
	if _, err := compute.ProjectInitializeSpec(req); err != nil {
		return result, err
	}
	if err := validTaskBinding(req.Binding); err != nil {
		return result, err
	}
	err := c.DoJSON(ctx, "POST", "projects/"+url.PathEscape(projectID)+"/initialize", req, &result)
	return result, err
}

func (c *Client) FinishProjectInitialization(ctx context.Context, projectID, requestID string, binding compute.ProjectBinding) (compute.Project, error) {
	var result struct {
		Project compute.Project `json:"project"`
	}
	if err := validProjectStrings(projectID, requestID); err != nil {
		return result.Project, err
	}
	if err := validTaskBinding(binding); err != nil {
		return result.Project, err
	}
	err := c.DoJSON(ctx, "POST", "projects/"+url.PathEscape(projectID)+"/initializations/"+url.PathEscape(requestID)+"/finish", struct {
		Binding compute.ProjectBinding `json:"binding"`
	}{binding}, &result)
	return result.Project, err
}
