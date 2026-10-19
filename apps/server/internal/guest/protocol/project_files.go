package protocol

import "time"

const MaxProjectFileSize = 1 << 20

// ProjectFileScope addresses a directory on the mounted project volume in one
// exact guest-service incarnation. RootIdentity survives VM replacement; the
// host must separately verify the volume and its binding generation.
type ProjectFileScope struct {
	Root         string `json:"root"`
	RootIdentity string `json:"rootIdentity,omitempty"`
	BootID       string `json:"bootId"`
	InstanceID   string `json:"instanceId"`
}

type ProjectFileInspectRequest = ProjectFileScope

type ProjectFileRoot struct {
	Root         string `json:"root"`
	RootIdentity string `json:"rootIdentity"`
}

type ProjectFileListRequest struct {
	ProjectFileScope
	Path   string `json:"path"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type ProjectFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

type ProjectFileListing struct {
	Entries    []ProjectFileEntry `json:"entries"`
	NextCursor string             `json:"nextCursor,omitempty"`
	Truncated  bool               `json:"truncated"`
}

type ProjectFileReadRequest struct {
	ProjectFileScope
	Path string `json:"path"`
}

type ProjectFile struct {
	Path       string    `json:"path"`
	Content    string    `json:"content"`
	SHA256     string    `json:"sha256"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

type ProjectFileWriteRequest struct {
	ProjectFileScope
	Path           string  `json:"path"`
	Content        string  `json:"content"`
	ExpectedSHA256 *string `json:"expectedSha256"`
}

type ProjectFileMkdirRequest struct {
	ProjectFileScope
	Path string `json:"path"`
}
