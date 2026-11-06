package client

import (
	"context"
	"net/url"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func (c *Client) ProjectGitHubState(ctx context.Context, id string) (result compute.ProjectGitHubState, err error) {
	if err = validProjectStrings(id); err != nil {
		return
	}
	err = c.DoJSON(ctx, "GET", "projects/"+url.PathEscape(id)+"/github", nil, &result)
	return
}

func (c *Client) ReadProjectGitHub(ctx context.Context, id, action string, request compute.ProjectGitHubRequest) (result compute.ProjectGitHubResult, err error) {
	if action != "git" && action != "review" && action != "refresh" {
		return result, compute.Problem("invalid_request", "Only fixed GitHub read actions are supported.")
	}
	err = c.projectGitHub(ctx, id, action, request, &result)
	return
}

func (c *Client) AssociateProjectGitHub(ctx context.Context, id string, request compute.ProjectGitHubRequest) (result compute.ProjectGitHubState, err error) {
	err = c.projectGitHub(ctx, id, "associate", request, &result)
	return
}

func (c *Client) ClearProjectGitHub(ctx context.Context, id string, request compute.ProjectGitHubRequest) (result compute.ProjectGitHubState, err error) {
	err = c.projectGitHub(ctx, id, "clear", request, &result)
	return
}

func (c *Client) projectGitHub(ctx context.Context, id, action string, request compute.ProjectGitHubRequest, output any) error {
	b := request.Binding
	if err := validProjectStrings(id, b.HostID, b.ProjectID, b.VolumeID, b.MachineID, b.BootID, b.InstanceID, request.Repository, request.RepositoryID, request.AccountID); err != nil {
		return err
	}
	return c.DoJSON(ctx, "POST", "projects/"+url.PathEscape(id)+"/github/"+action, request, output)
}
