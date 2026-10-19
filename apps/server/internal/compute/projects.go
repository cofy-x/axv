package compute

import (
	"encoding/json"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Project identifies persistent project data, independently of a machine or run.
type Project struct {
	ID           string    `json:"id"`
	HostID       string    `json:"hostId"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	VolumeID     string    `json:"volumeId"`
	Root         string    `json:"root"`
	RootIdentity string    `json:"rootIdentity"`
	Revision     int64     `json:"revision"`
	CreatedAt    time.Time `json:"createdAt"`
}

type RegisterProjectRequest struct {
	RequestID      string `json:"requestId"`
	VolumeID       string `json:"volumeId"`
	VolumeRevision int64  `json:"volumeRevision"`
	MachineID      string `json:"machineId"`
	Root           string `json:"root"`
	Name           string `json:"name,omitempty"`
}

// ProjectBinding fences a file operation to the environment explicitly opened
// by its caller. A fresh binding must never be substituted during retries.
type ProjectBinding struct {
	HostID          string `json:"hostId"`
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	VolumeID        string `json:"volumeId"`
	VolumeRevision  int64  `json:"volumeRevision"`
	MachineID       string `json:"machineId"`
	BootID          string `json:"bootId"`
	InstanceID      string `json:"instanceId"`
}

type ProjectBindingResult struct {
	Project Project        `json:"project"`
	Binding ProjectBinding `json:"binding"`
}

type ProjectFileRequest struct {
	Binding        ProjectBinding `json:"binding"`
	Path           string         `json:"path"`
	Cursor         string         `json:"cursor,omitempty"`
	Limit          int            `json:"limit,omitempty"`
	Content        string         `json:"content,omitempty"`
	ExpectedSHA256 *string        `json:"expectedSha256,omitempty"`
}

// Empty file contents are significant on writes. Keep them in the JSON when
// an explicit write expectation is present, while omitting write-only fields
// from read/list/directory requests.
func (r ProjectFileRequest) MarshalJSON() ([]byte, error) {
	type request ProjectFileRequest
	var content *string
	if r.ExpectedSHA256 != nil || r.Content != "" {
		content = &r.Content
	}
	return json.Marshal(struct {
		request
		Content *string `json:"content,omitempty"`
	}{request: request(r), Content: content})
}

func ValidateProjectRoot(root string) error {
	if root == "" {
		return nil
	}
	if !utf8.ValidString(root) || len(root) > 4096 || path.IsAbs(root) || path.Clean(root) != root || strings.Contains(root, "\\") || strings.IndexFunc(root, unicode.IsControl) >= 0 {
		return Problem("invalid_request", "Project roots must be canonical paths relative to the project volume.")
	}
	for _, component := range strings.Split(root, "/") {
		if component == "." || component == ".." || component == ".axv" || strings.HasPrefix(component, ".axv-") {
			return Problem("invalid_request", "Project roots cannot escape the volume or use AXV's reserved names.")
		}
	}
	return nil
}
