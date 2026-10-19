package client

import (
	"context"
	"net/url"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

// ProjectBindingResult is an explicit snapshot. Retain it for the whole file
// operation; obtaining a newer binding is a separate caller decision.
type ProjectBindingResult = compute.ProjectBindingResult

type ProjectFileResult struct {
	ProjectBindingResult
	protocol.ProjectFile
}

type ProjectListingResult struct {
	ProjectBindingResult
	protocol.ProjectFileListing
}

type ProjectEntryResult struct {
	ProjectBindingResult
	protocol.ProjectFileEntry
}

func (c *Client) Projects(ctx context.Context) ([]compute.Project, error) {
	var result struct {
		Projects []compute.Project `json:"projects"`
	}
	err := c.DoJSON(ctx, "GET", "projects", nil, &result)
	return result.Projects, err
}

func (c *Client) RegisterProject(ctx context.Context, request compute.RegisterProjectRequest) (compute.Project, error) {
	if err := validProjectStrings(request.RequestID, request.VolumeID, request.MachineID, request.Root, request.Name); err != nil {
		return compute.Project{}, err
	}
	var result struct {
		Project compute.Project `json:"project"`
	}
	err := c.DoJSON(ctx, "POST", "projects", request, &result)
	return result.Project, err
}

func (c *Client) ProjectBinding(ctx context.Context, id string) (ProjectBindingResult, error) {
	if err := validProjectStrings(id); err != nil {
		return ProjectBindingResult{}, err
	}
	var result ProjectBindingResult
	err := c.DoJSON(ctx, "GET", "projects/"+url.PathEscape(id)+"/binding", nil, &result)
	return result, err
}

func (c *Client) ProjectFiles(ctx context.Context, id string, request compute.ProjectFileRequest) (ProjectListingResult, error) {
	var result ProjectListingResult
	err := c.projectFile(ctx, id, "list", request, &result)
	return result, err
}

func (c *Client) ReadProjectFile(ctx context.Context, id string, request compute.ProjectFileRequest) (ProjectFileResult, error) {
	var result ProjectFileResult
	err := c.projectFile(ctx, id, "read", request, &result)
	return result, err
}

func (c *Client) WriteProjectFile(ctx context.Context, id string, request compute.ProjectFileRequest) (ProjectFileResult, error) {
	var result ProjectFileResult
	err := c.projectFile(ctx, id, "write", request, &result)
	return result, err
}

func (c *Client) MkdirProject(ctx context.Context, id string, request compute.ProjectFileRequest) (ProjectEntryResult, error) {
	var result ProjectEntryResult
	err := c.projectFile(ctx, id, "mkdir", request, &result)
	return result, err
}

func (c *Client) projectFile(ctx context.Context, id, action string, request compute.ProjectFileRequest, output any) error {
	binding := request.Binding
	values := []string{id, binding.HostID, binding.ProjectID, binding.VolumeID, binding.MachineID, binding.BootID, binding.InstanceID, request.Path, request.Cursor, request.Content}
	if request.ExpectedSHA256 != nil {
		values = append(values, *request.ExpectedSHA256)
	}
	if err := validProjectStrings(values...); err != nil {
		return err
	}
	return c.DoJSON(ctx, "POST", "projects/"+url.PathEscape(id)+"/files/"+action, request, output)
}

// encoding/json replaces invalid UTF-8 while marshaling strings. Reject it
// before encoding so a path or reviewed content cannot silently change.
func validProjectStrings(values ...string) error {
	for _, value := range values {
		if !utf8.ValidString(value) {
			return compute.Problem("invalid_request", "VM project request values must be valid UTF-8.")
		}
	}
	return nil
}
