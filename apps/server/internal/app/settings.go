package app

import (
	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// ComputeSettings is a resolved connection, refreshed independently of desktop
// inputs when preparation publishes new profile configuration.
type ComputeSettings struct {
	Compute      profile.Compute `json:"compute"`
	Host         *compute.Config `json:"host,omitempty"`
	ComputeError string          `json:"-"`
}

// ProcessState contains dependencies owned by this desktop process only.
type ProcessState struct {
	RunnerExecutable string              `json:"-"`
	Logger           *diagnostics.Logger `json:"-"`
}

// Settings composes resolved inputs and process state for service execution.
// Persist Snapshot instead of using this runtime object as a file contract.
type Settings struct {
	launch.Inputs
	ComputeSettings
	ProcessState
}

func (s Settings) Snapshot() launch.Snapshot {
	return (launch.Snapshot{Inputs: s.Inputs, Compute: s.Compute, Host: s.Host}).Clone()
}

// SettingsFromSnapshot restores captured inputs without resolving the current
// profile or environment. The new process initializes its runtime dependencies.
func SettingsFromSnapshot(snapshot launch.Snapshot) Settings {
	snapshot = snapshot.Clone()
	return Settings{Inputs: snapshot.Inputs, ComputeSettings: ComputeSettings{Compute: snapshot.Compute, Host: snapshot.Host}}
}
