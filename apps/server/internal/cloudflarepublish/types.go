// Package cloudflarepublish owns durable, explicitly published VM project
// services on isolated HTTPS hostnames. It never serves the AXV desktop.
package cloudflarepublish

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflareapi"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/projectpreview"
)

type Settings struct {
	APIToken, AccountID, ZoneID, TunnelID, Domain, Listen string
	Audience, Issuer, OwnerSubject, DesktopOrigin         string
}

type CapturedRoute struct {
	Socket            string `json:"socket"`
	ConfigFingerprint string `json:"configFingerprint"`
}

type Options struct {
	Home     string
	Gateway  *gateway.Gateway
	Settings func() (Settings, error)
	Capture  func(context.Context, compute.ProjectBinding) (CapturedRoute, error)
	Resolve  func(context.Context, CapturedRoute, compute.ProjectBinding, int) (projectpreview.Target, error)
	// Client is only for a bounded Cloudflare API fixture. Production leaves it nil.
	Client    func(Settings) (*cloudflareapi.Client, error)
	Authorize func(context.Context, Settings, *http.Request) (time.Time, error)
}

type Registration struct {
	InstanceID string                 `json:"instanceId"`
	RequestID  string                 `json:"requestId"`
	Binding    compute.ProjectBinding `json:"binding"`
	Port       int                    `json:"port"`
	Name       string                 `json:"name"`
	Domain     string                 `json:"domain"`
	Visibility string                 `json:"visibility"`
}

type Publication struct {
	ID             string                 `json:"id"`
	RequestID      string                 `json:"requestId"`
	Name           string                 `json:"name"`
	Generation     string                 `json:"generation"`
	Domain         string                 `json:"domain"`
	Hostname       string                 `json:"hostname"`
	URL            string                 `json:"url,omitempty"`
	Visibility     string                 `json:"visibility"`
	Binding        compute.ProjectBinding `json:"binding"`
	Port           int                    `json:"port"`
	State          string                 `json:"state"`
	Phase          string                 `json:"phase"`
	Error          string                 `json:"error,omitempty"`
	CleanupPending bool                   `json:"cleanupPending"`
	CreatedAt      time.Time              `json:"createdAt"`
	UpdatedAt      time.Time              `json:"updatedAt"`
}

// Problem is safe for HTTP/CLI display and never includes provider response
// bodies, credentials or captured private routing paths.
type Problem struct {
	Status  int
	Code    string
	Message string
}

func (p *Problem) Error() string { return p.Message }
func problem(status int, code, format string, args ...any) error {
	return &Problem{status, code, fmt.Sprintf(format, args...)}
}

type resources struct {
	AccountID       string                          `json:"accountId"`
	ZoneID          string                          `json:"zoneId"`
	TunnelID        string                          `json:"tunnelId"`
	Domain          string                          `json:"domain"`
	Listen          string                          `json:"listen"`
	Issuer          string                          `json:"issuer,omitempty"`
	Audience        string                          `json:"audience,omitempty"`
	OwnerSubject    string                          `json:"ownerSubject,omitempty"`
	DNS             *cloudflareapi.CNAMEOwnership   `json:"dns,omitempty"`
	Ingress         *cloudflareapi.IngressOwnership `json:"ingress,omitempty"`
	IngressExpected string                          `json:"ingressExpected,omitempty"`
}

type record struct {
	Publication Publication   `json:"publication"`
	Input       Registration  `json:"input"`
	CookieSite  string        `json:"cookieSite"`
	Route       CapturedRoute `json:"route"`
	Resources   resources     `json:"resources"`
	Remove      bool          `json:"remove"`
}

type journal struct {
	Version    int               `json:"version"`
	InstanceID string            `json:"instanceId"`
	Records    map[string]record `json:"records"`
}
