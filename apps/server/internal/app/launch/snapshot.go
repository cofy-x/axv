// Package launch owns captured desktop inputs, snapshots and readiness identity.
// It does not initialize profiles, compose services or supervise processes.
package launch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// Inputs contains captured desktop inputs. Live compute resolution and
// process-local dependencies do not participate in desktop launch identity.
type Inputs struct {
	LaunchID        string            `json:"launchId,omitempty"`
	ComputeOverride string            `json:"computeOverride,omitempty"`
	Home            string            `json:"home"`
	Environment     map[string]string `json:"environment,omitempty"`
	Desktop         config.Config     `json:"desktop"`
}

// Snapshot preserves the existing format-4 daemon and _desktop
// snapshot fields. Compute and Host are the connection captured at launch;
// activation may subsequently refresh them without changing desktop identity.
type Snapshot struct {
	Inputs
	Compute profile.Compute `json:"compute"`
	Host    *compute.Config `json:"host,omitempty"`
}

// Clone gives the snapshot independent ownership of all captured reference values.
func (s Snapshot) Clone() Snapshot {
	s.Environment = maps.Clone(s.Environment)
	s.Host = cloneHost(s.Host)
	return s
}

// Fingerprint identifies captured desktop inputs independently of runtime state.
// The empty compute field belongs to the input projection, not live settings.
func (s Inputs) Fingerprint() string {
	identity := struct {
		ComputeOverride string            `json:"computeOverride,omitempty"`
		Home            string            `json:"home"`
		Environment     map[string]string `json:"environment,omitempty"`
		Desktop         config.Config     `json:"desktop"`
		Compute         profile.Compute   `json:"compute"`
	}{ComputeOverride: s.ComputeOverride, Home: s.Home, Environment: s.Environment, Desktop: s.Desktop}
	b, _ := json.Marshal(identity)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func cloneHost(source *compute.Config) *compute.Config {
	if source == nil {
		return nil
	}
	c := *source
	if source.SocketGroup != nil {
		group := *source.SocketGroup
		c.SocketGroup = &group
	}
	if source.Lima != nil {
		lima := *source.Lima
		c.Lima = &lima
	}
	if source.Firecracker != nil {
		firecracker := *source.Firecracker
		c.Firecracker = &firecracker
	}
	c.Images = slices.Clone(source.Images)
	for i := range c.Images {
		if source.Images[i].Guest != nil {
			guest := *source.Images[i].Guest
			guest.Capabilities = slices.Clone(guest.Capabilities)
			c.Images[i].Guest = &guest
		}
	}
	return &c
}
