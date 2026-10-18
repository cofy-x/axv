// Package prepare owns durable, profile-scoped VM preparation independently of
// desktop transport and process supervision.
package prepare

import (
	"context"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// Activator verifies and publishes the already-committed configuration. A nil
// activator prepares configuration only, as used by an offline setup command.
type Activator func(context.Context, Activation) (Binding, error)

type Activation struct {
	Compute        profile.Compute
	Host           *compute.Config
	ExpectedHostID string
}

type Binding struct {
	HostID            string `json:"hostId"`
	ProtocolVersion   int    `json:"protocolVersion"`
	ConfigFingerprint string `json:"configFingerprint"`
}

type Options struct {
	Logger                    *diagnostics.Logger
	CacheHome                 string
	Home                      string
	DefaultLimaHome           string
	ImageBundle               string
	ConfigurationLockedReason string
	Activator                 Activator
}

type Request struct {
	ImageSource string `json:"imageSource,omitempty"`
	StorageRoot string `json:"storageRoot,omitempty"`
	LimaHome    string `json:"limaHome,omitempty"`
	CPUs        int    `json:"cpus,omitempty"`
	MemoryMiB   int64  `json:"memoryMiB,omitempty"`
	DiskGiB     int64  `json:"diskGiB,omitempty"`
	Machines    int    `json:"machines,omitempty"`
	Socket      string `json:"socket,omitempty"`
	HostConfig  string `json:"hostConfig,omitempty"`
	ImageBundle string `json:"imageBundle,omitempty"`
}

type Requirement struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"` // ready, needed, blocked
	Detail string `json:"detail"`
}

type Check struct {
	OfficialImageAvailable  bool          `json:"officialImageAvailable"`
	Platform                string        `json:"platform"`
	Arch                    string        `json:"arch"`
	Configured              bool          `json:"configured"`
	CacheHome               string        `json:"cacheHome"`
	ConfigurationSource     string        `json:"configurationSource"`
	LockedReason            string        `json:"lockedReason,omitempty"`
	AutomaticSetupSupported bool          `json:"automaticSetupSupported"`
	RequiredLimaVersion     string        `json:"requiredLimaVersion"`
	Defaults                Request       `json:"defaults"`
	Checks                  []Requirement `json:"checks"`
	ActiveJob               *Job          `json:"activeJob,omitempty"`
	LatestJob               *Job          `json:"latestJob,omitempty"`
}

type Download struct {
	OriginURL  string `json:"originUrl,omitempty"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"` // zero means the exact transfer size is unknown
	Cached     bool   `json:"cached"`
	CacheState string `json:"cacheState,omitempty"`
	CachePath  string `json:"cachePath,omitempty"`
}

type Plan struct {
	CacheHome      string        `json:"cacheHome"`
	ID             string        `json:"id"`
	CreatedAt      string        `json:"createdAt"`
	ExpiresAt      string        `json:"expiresAt"`
	Platform       string        `json:"platform"`
	Arch           string        `json:"arch"`
	Provider       string        `json:"provider"`
	Request        Request       `json:"request"`
	Actions        []string      `json:"actions"`
	Downloads      []Download    `json:"downloads"`
	Checks         []Requirement `json:"checks"`
	CanStart       bool          `json:"canStart"`
	ConfigPath     string        `json:"configPath"`
	HostConfigPath string        `json:"hostConfigPath"`
	Activation     string        `json:"activation"` // ready or configured
}

type StartRequest struct {
	RequestID string `json:"requestId"`
	PlanID    string `json:"planId"`
	Confirmed bool   `json:"confirmed"`
}

type ActionRequest struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

type Step struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Status     string `json:"status"` // pending, running, succeeded, failed
	Detail     string `json:"detail,omitempty"`
	BytesDone  int64  `json:"bytesDone,omitempty"`
	BytesTotal int64  `json:"bytesTotal,omitempty"`
}

type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Job struct {
	ID        string   `json:"id"`
	RequestID string   `json:"requestId"`
	PlanID    string   `json:"planId"`
	Revision  int64    `json:"revision"`
	Status    string   `json:"status"` // queued, running, interrupted, failed, succeeded, cancelled
	Attempt   int      `json:"attempt"`
	Phase     string   `json:"phase"`
	Steps     []Step   `json:"steps"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
	Committed bool     `json:"committed"`
	CanCancel bool     `json:"canCancel"`
	CanRetry  bool     `json:"canRetry"`
	Result    string   `json:"result,omitempty"` // configured or ready
	Error     *Failure `json:"error,omitempty"`
	Binding   *Binding `json:"binding,omitempty"`
	Plan      Plan     `json:"plan"`
}
