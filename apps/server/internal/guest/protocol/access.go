package protocol

import "time"

// SSHInfo describes one VM boot. HostID is added by the private host API.
type SSHInfo struct {
	Version   int    `json:"version"`
	HostID    string `json:"hostId,omitempty"`
	MachineID string `json:"machineId"`
	BootID    string `json:"bootId"`
	Available bool   `json:"available"`
	User      string `json:"user"`
	HostKey   string `json:"hostKey,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type SSHClientRequest struct {
	BootID    string `json:"bootId"`
	ClientID  string `json:"clientId"`
	PublicKey string `json:"publicKey"`
}

type SSHClient struct {
	BootID    string `json:"bootId"`
	ClientID  string `json:"clientId"`
	PublicKey string `json:"publicKey"`
}

type Reachability struct {
	From   string `json:"from"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type NetworkAddress struct {
	Interface    string       `json:"interface"`
	Address      string       `json:"address"`
	PrefixLength int          `json:"prefixLength"`
	Family       string       `json:"family"`
	Reachability Reachability `json:"reachability"`
}

type NetworkInfo struct {
	Version    int              `json:"version"`
	HostID     string           `json:"hostId,omitempty"`
	MachineID  string           `json:"machineId"`
	BootID     string           `json:"bootId"`
	ObservedAt time.Time        `json:"observedAt"`
	Provider   string           `json:"provider,omitempty"`
	Addresses  []NetworkAddress `json:"addresses"`
}
