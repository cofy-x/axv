package client

import (
	"context"
	"net/url"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func (c *Client) ProjectBackups(ctx context.Context) ([]compute.Volume, error) {
	var result struct {
		Backups []compute.Volume `json:"backups"`
	}
	err := c.DoJSON(ctx, "GET", "project-backups", nil, &result)
	return result.Backups, err
}

func (c *Client) BackupProject(ctx context.Context, id string, req compute.BackupProjectRequest) (compute.VolumeResult, error) {
	var result compute.VolumeResult
	if err := validProjectStrings(id, req.RequestID, req.Name, req.MachineID); err != nil {
		return result, err
	}
	err := c.DoJSON(ctx, "POST", "projects/"+url.PathEscape(id)+"/backups", req, &result)
	return result, err
}

func (c *Client) RestoreProjectVolume(ctx context.Context, id string, req compute.RestoreProjectVolumeRequest) (compute.VolumeResult, error) {
	var result compute.VolumeResult
	if err := validProjectStrings(id, req.RequestID, req.Name); err != nil {
		return result, err
	}
	err := c.DoJSON(ctx, "POST", "project-backups/"+url.PathEscape(id)+"/restore", req, &result)
	return result, err
}

func (c *Client) BindRestoredProject(ctx context.Context, id string, req compute.BindRestoredProjectRequest) (compute.Project, error) {
	var result struct {
		Project compute.Project `json:"project"`
	}
	if err := validProjectStrings(id, req.RequestID, req.Name, req.MachineID, req.VolumeID, req.SourceProjectID); err != nil {
		return result.Project, err
	}
	err := c.DoJSON(ctx, "POST", "project-backups/"+url.PathEscape(id)+"/bind", req, &result)
	return result.Project, err
}
