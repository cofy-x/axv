package management

import (
	"time"

	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
)

type AccessConfig struct {
	Enabled      bool   `json:"enabled"`
	Listen       string `json:"listen"`
	Origin       string `json:"origin"`
	Issuer       string `json:"issuer"`
	Audience     string `json:"audience"`
	OwnerSubject string `json:"ownerSubject"`
}

type AccountConfig struct {
	Configured bool   `json:"configured"`
	HasToken   bool   `json:"hasToken"`
	AccountID  string `json:"accountId"`
	ZoneID     string `json:"zoneId"`
	TunnelID   string `json:"tunnelId"`
	Domain     string `json:"domain"`
	Listen     string `json:"listen"`
	Audience   string `json:"audience"`
}

type ConfigView struct {
	InstanceID string        `json:"instanceId"`
	Revision   string        `json:"revision"`
	Access     AccessConfig  `json:"access"`
	Account    AccountConfig `json:"account"`
}

type AccessSettings struct {
	Listen       string `json:"listen"`
	Origin       string `json:"origin"`
	Issuer       string `json:"issuer"`
	Audience     string `json:"audience"`
	OwnerSubject string `json:"ownerSubject"`
}

type AccountSettings struct {
	AccountID string `json:"accountId"`
	ZoneID    string `json:"zoneId"`
	TunnelID  string `json:"tunnelId"`
	Domain    string `json:"domain"`
	Listen    string `json:"listen"`
	Audience  string `json:"audience"`
}

type ConfigUpdate struct {
	InstanceID       string           `json:"instanceId"`
	ExpectedRevision string           `json:"expectedRevision"`
	Access           *AccessSettings  `json:"access"`
	Account          *AccountSettings `json:"account"`
	TokenAction      string           `json:"tokenAction"`
	Token            string           `json:"token,omitempty"`
}

type Health struct {
	State        string    `json:"state"`
	CheckedAt    time.Time `json:"checkedAt"`
	Message      string    `json:"message"`
	TunnelName   string    `json:"tunnelName,omitempty"`
	TunnelStatus string    `json:"tunnelStatus,omitempty"`
}

// TrafficResult retains exact provider counters. The window remains available
// when analytics is unsupported, without emitting fabricated zero counters.
type TrafficResult struct {
	Supported bool      `json:"supported"`
	Reason    string    `json:"reason,omitempty"`
	Since     time.Time `json:"since"`
	Until     time.Time `json:"until"`
	*cfclient.Traffic
}

type WizardInput struct {
	Step      string        `json:"step"`
	Token     string        `json:"token,omitempty"`
	AccountID string        `json:"accountId,omitempty"`
	ZoneID    string        `json:"zoneId,omitempty"`
	TunnelID  string        `json:"tunnelId,omitempty"`
	Domain    string        `json:"domain,omitempty"`
	Access    *AccessConfig `json:"access,omitempty"`
}

// Problem is a safe operation failure. HTTP adapters map Code to a status.
type Problem struct{ Code, Message string }

func (p *Problem) Error() string         { return p.Message }
func problem(code, message string) error { return &Problem{Code: code, Message: message} }

type TokenUpdate struct {
	InstanceID       string `json:"instanceId"`
	ExpectedRevision string `json:"expectedRevision"`
	Token            string `json:"token"`
}

// DesktopState is captured while listener transitions are serialized.
type DesktopState struct{ Active, Stopping bool }
