// Package protocol is the versioned transport contract shared by the host and
// Linux guest. It has no dependency on a hypervisor or desktop feature.
package protocol

const Version = 3
const Port uint32 = 1024

type Config struct {
	MachineID   string `json:"machineId"`
	Token       string `json:"token"`
	ProjectRoot string `json:"projectRoot"`
	DNS         string `json:"dns,omitempty"`
}

type Health struct {
	Capabilities []string `json:"capabilities"`
	Version      int      `json:"version"`
	MachineID    string   `json:"machineId"`
	BootID       string   `json:"bootId"`
	Ready        bool     `json:"ready"`
	Reason       string   `json:"reason,omitempty"`
	SSHAvailable bool     `json:"sshAvailable"`
}

type ExecRequest struct {
	Argv           []string `json:"argv"`
	Directory      string   `json:"directory,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

type ExecResult struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

type TerminalRequest struct {
	RequestID string `json:"requestId"`
	Directory string `json:"directory,omitempty"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

type Terminal struct {
	ID     string `json:"id"`
	BootID string `json:"bootId"`
	Status string `json:"status"`
}
