package storage

import "encoding/json"

// Error describes a rejected storage operation without choosing an HTTP status.
type Error struct {
	Code, Message string
}

func (e *Error) Error() string { return e.Message }

func problem(code, message string) error { return &Error{Code: code, Message: message} }

type Preferences struct {
	Wallpaper   string `json:"wallpaper"`
	IconSnap    bool   `json:"iconSnap"`
	Clock24Hour bool   `json:"clock24Hour"`
}

type Bootstrap struct {
	DatabaseID  string          `json:"databaseId"`
	Preferences Preferences     `json:"preferences"`
	Layout      json.RawMessage `json:"layout"`
	LayoutError string          `json:"layoutError,omitempty"`
}
