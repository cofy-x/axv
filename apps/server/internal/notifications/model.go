// Package notifications owns durable intake checkpoints and optional outbound
// delivery. Execution owners retain their event journals and all work control.
package notifications

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

const maxEvents = 512
const maxSources = 4096
const maxAttempts = 3

var ErrStore = errors.New("notification_store_unavailable")
var ErrConflict = errors.New("notification_conflict")
var ErrInput = errors.New("invalid_notification_request")
var ErrMissing = errors.New("notification_unavailable")

type Config struct {
	Enabled         bool     `json:"enabled"`
	TokenConfigured bool     `json:"tokenConfigured"`
	ApplicationID   string   `json:"applicationId"`
	DeviceID        string   `json:"deviceId"`
	ReturnOrigin    string   `json:"returnOrigin"`
	EventTypes      []string `json:"eventTypes"`
}

type Notification struct {
	ID            string                   `json:"id"`
	CreatedAt     time.Time                `json:"createdAt"`
	Kind          string                   `json:"kind"`
	State         string                   `json:"state"`
	Status        string                   `json:"status"`
	Attempts      int                      `json:"attempts"`
	NextAttemptAt *time.Time               `json:"nextAttemptAt,omitempty"`
	Target        notificationevent.Target `json:"target"`
	Error         string                   `json:"error,omitempty"`
}

type Intake struct {
	Gap   bool   `json:"gap"`
	Error string `json:"error,omitempty"`
}

type Snapshot struct {
	StoreID      string         `json:"storeId"`
	Revision     uint64         `json:"revision"`
	Config       Config         `json:"config"`
	Events       []Notification `json:"events"`
	Intake       Intake         `json:"intake"`
	Error        string         `json:"error,omitempty"`
	NextSendAt   *time.Time     `json:"nextSendAt,omitempty"`
	CanConfigure bool           `json:"canConfigure"`
}

type Update struct {
	StoreID       string   `json:"storeId"`
	Revision      uint64   `json:"revision"`
	Enabled       bool     `json:"enabled"`
	TokenAction   string   `json:"tokenAction"`
	Token         string   `json:"token,omitempty"`
	ApplicationID string   `json:"applicationId"`
	DeviceID      string   `json:"deviceId"`
	ReturnOrigin  string   `json:"returnOrigin"`
	EventTypes    []string `json:"eventTypes"`
}

type Source struct {
	Name string
	Read func(context.Context, map[string]uint64) ([]notificationevent.Batch, error)
}

type Options struct {
	Home          string
	Sources       []Source
	Resolve       func(context.Context, notificationevent.Target) (notificationevent.Resolution, error)
	Client        *http.Client
	DisableWorker bool
}

type checkpoint struct {
	StoreID  string `json:"storeId"`
	Sequence uint64 `json:"sequence"`
}

type diskState struct {
	Version     int                   `json:"version"`
	StoreID     string                `json:"storeId"`
	Revision    uint64                `json:"revision"`
	Config      Config                `json:"config"`
	Token       string                `json:"token"`
	NextSendAt  *time.Time            `json:"nextSendAt,omitempty"`
	EnabledAt   time.Time             `json:"enabledAt"`
	Checkpoints map[string]checkpoint `json:"checkpoints"`
	Events      []Notification        `json:"events"`
	Intake      Intake                `json:"intake"`
}

var eventTitles = map[string]string{
	"agent_waiting_approval": "Agent requested approval",
	"agent_waiting_input":    "Agent requested input",
	"agent_succeeded":        "Agent turn completed",
	"agent_failed":           "Agent turn failed",
	"agent_interrupted":      "Agent turn interrupted",
	"task_succeeded":         "Project task completed",
	"task_failed":            "Project task failed",
	"task_cancelled":         "Project task cancelled",
	"task_interrupted":       "Project task interrupted",
	"task_unknown":           "Project task outcome uncertain",
	"test":                   "AXV notification test",
}
