package storage

import "encoding/json"

const MaxFileSize = 10 << 20
const MaxTextSize = 1 << 20

// Error describes a rejected storage operation without choosing an HTTP status.
type Error struct {
	Code, Message string
}

func (e *Error) Error() string { return e.Message }

func problem(code, message string) error { return &Error{Code: code, Message: message} }

type Entry struct {
	ID          string `json:"id"`
	ParentID    string `json:"parentId"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	MIME        string `json:"mime"`
	Size        int    `json:"size"`
	Revision    int    `json:"revision"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	TrashedAt   string `json:"trashedAt,omitempty"`
	Editable    bool   `json:"editable"`
	Previewable bool   `json:"previewable"`
	System      bool   `json:"system"`
	TrashBatch  string `json:"-"`
}

type Preferences struct {
	Wallpaper   string `json:"wallpaper"`
	IconSnap    bool   `json:"iconSnap"`
	Clock24Hour bool   `json:"clock24Hour"`
}

type Bootstrap struct {
	DatabaseID  string          `json:"databaseId"`
	Entries     []Entry         `json:"entries"`
	Preferences Preferences     `json:"preferences"`
	Layout      json.RawMessage `json:"layout"`
	LayoutError string          `json:"layoutError,omitempty"`
}

type CreateInput struct {
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Content  string `json:"content"`
}

type UpdateInput struct {
	Name     *string `json:"name"`
	ParentID *string `json:"parentId"`
	Content  *string `json:"content"`
	Revision *int    `json:"revision"`
}

type ImportFile struct {
	Name string
	Data []byte
}
