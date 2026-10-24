package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflareapi"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type cloudflareAccessConfig struct {
	Enabled      bool   `json:"enabled"`
	Listen       string `json:"listen"`
	Origin       string `json:"origin"`
	Issuer       string `json:"issuer"`
	Audience     string `json:"audience"`
	OwnerSubject string `json:"ownerSubject"`
}

type cloudflareAccountConfig struct {
	Configured bool   `json:"configured"`
	HasToken   bool   `json:"hasToken"`
	AccountID  string `json:"accountId"`
	ZoneID     string `json:"zoneId"`
	TunnelID   string `json:"tunnelId"`
	Domain     string `json:"domain"`
	Listen     string `json:"listen"`
	Audience   string `json:"audience"`
}

type cloudflareConfigView struct {
	InstanceID string                  `json:"instanceId"`
	Revision   string                  `json:"revision"`
	Access     cloudflareAccessConfig  `json:"access"`
	Account    cloudflareAccountConfig `json:"account"`
}

type cloudflareConfigUpdate struct {
	InstanceID       string `json:"instanceId"`
	ExpectedRevision string `json:"expectedRevision"`
	Access           *struct {
		Listen       string `json:"listen"`
		Origin       string `json:"origin"`
		Issuer       string `json:"issuer"`
		Audience     string `json:"audience"`
		OwnerSubject string `json:"ownerSubject"`
	} `json:"access"`
	Account *struct {
		AccountID string `json:"accountId"`
		ZoneID    string `json:"zoneId"`
		TunnelID  string `json:"tunnelId"`
		Domain    string `json:"domain"`
		Listen    string `json:"listen"`
		Audience  string `json:"audience"`
	} `json:"account"`
	TokenAction string `json:"tokenAction"`
	Token       string `json:"token,omitempty"`
}

type cloudflareHealth struct {
	State        string    `json:"state"`
	CheckedAt    time.Time `json:"checkedAt"`
	Message      string    `json:"message"`
	TunnelName   string    `json:"tunnelName,omitempty"`
	TunnelStatus string    `json:"tunnelStatus,omitempty"`
}

type cloudflareManagement struct {
	home, instanceID      string
	ctx                   context.Context
	cancel                context.CancelFunc
	desktop               *cloudflareController
	busy                  func() bool
	validateDesktopOrigin func(string) error
	client                func(string) (*cloudflareapi.Client, error)
	mu                    sync.Mutex
	healthOp              sync.Mutex
	health                cloudflareHealth
	healthRevision        string
}

func newCloudflareManagement(home, instanceID string, desktop *cloudflareController, busy func() bool) *cloudflareManagement {
	ctx, cancel := context.WithCancel(desktop.ctx)
	return &cloudflareManagement{home: home, instanceID: instanceID, ctx: ctx, cancel: cancel, desktop: desktop, busy: busy, client: func(token string) (*cloudflareapi.Client, error) {
		return cloudflareapi.New(cloudflareapi.Options{Token: token})
	}}
}

// Close owns no workers: the HTTP server owns/join requests. Cancel its provider
// I/O before the other managers close, including non-signal server exits.
func (m *cloudflareManagement) Close() { m.cancel() }

// Provider reads belong to both the requesting client and this desktop. HTTP
// Shutdown alone does not cancel active requests when the desktop stops.
func (m *cloudflareManagement) requestContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	stop := context.AfterFunc(m.ctx, cancel)
	if m.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func (m *cloudflareManagement) requestEnded(w http.ResponseWriter, r *http.Request) bool {
	if r.Context().Err() != nil {
		return true
	}
	if m.ctx.Err() != nil {
		cloudflareProblem(w, 503, "cloudflare_stopping", "AXV is stopping. Reopen it before checking or changing Cloudflare settings.")
		return true
	}
	return false
}

func cloudflareConfigProjection(instanceID, revision string, c profile.Cloudflare) cloudflareConfigView {
	return cloudflareConfigView{InstanceID: instanceID, Revision: revision,
		Access:  cloudflareAccessConfig{c.Enabled, c.Listen, c.Origin, c.Issuer, c.Audience, c.OwnerSubject},
		Account: cloudflareAccountConfig{c.Management.Configured(), c.Management.APIToken != "", c.Management.AccountID, c.Management.ZoneID, c.Management.TunnelID, c.Management.Domain, c.Management.Listen, c.Management.Audience}}
}

func cloudflareJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(value)
}

func cloudflareProblem(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	remoteWriteError(w, status, code, message)
}

func cloudflareDecode(w http.ResponseWriter, r *http.Request, value any) bool {
	media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.URL.RawQuery != "" || r.URL.ForceQuery || mediaErr != nil || media != "application/json" {
		cloudflareProblem(w, 400, "invalid_cloudflare_request", "Provide one Cloudflare JSON object without query parameters.")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	// A pointer wrapper rejects JSON null without losing strict field checks.
	var raw json.RawMessage
	if d.Decode(&raw) != nil || len(raw) == 0 || raw[0] != '{' || !jsonunicode.Valid(raw) || d.Decode(new(any)) != io.EOF {
		cloudflareProblem(w, 400, "invalid_cloudflare_request", "Provide one valid Cloudflare JSON object.")
		return false
	}
	d = json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		cloudflareProblem(w, 400, "invalid_cloudflare_request", "Cloudflare settings contain invalid or unknown fields.")
		return false
	}
	return true
}

func (m *cloudflareManagement) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cloudflare/config", m.getConfig)
	mux.HandleFunc("PUT /api/cloudflare/config", m.putConfig)
	mux.HandleFunc("PUT /api/cloudflare/token", m.rotateToken)
	mux.HandleFunc("POST /api/cloudflare/wizard", m.wizard)
	mux.HandleFunc("GET /api/cloudflare/health", m.getHealth)
	mux.HandleFunc("GET /api/cloudflare/traffic", m.getTraffic)
	mux.HandleFunc("GET /api/cloudflare/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if !localaccess.AllowRequest(r, false) {
			cloudflareProblem(w, 403, "cloudflare_forbidden", "Authenticated desktop access is required.")
			return
		}
		cloudflareJSON(w, map[string]bool{"configure": localaccess.Request(r, false), "publish": true, "health": true, "traffic": true})
	})
}

func (m *cloudflareManagement) rotateToken(w http.ResponseWriter, r *http.Request) {
	if !localaccess.Request(r, true) {
		cloudflareProblem(w, 403, "cloudflare_local_only", "Rotate Cloudflare credentials on the computer running AXV.")
		return
	}
	var input struct {
		InstanceID       string `json:"instanceId"`
		ExpectedRevision string `json:"expectedRevision"`
		Token            string `json:"token"`
	}
	if !cloudflareDecode(w, r, &input) {
		return
	}
	if input.InstanceID == "" || input.InstanceID != m.instanceID {
		cloudflareProblem(w, 409, "cloudflare_instance_changed", "Reload the current desktop before rotating the token.")
		return
	}
	previous, revision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		cloudflareProblem(w, 500, "cloudflare_config_failed", "Could not read Cloudflare settings; the current token was preserved.")
		return
	}
	if revision != input.ExpectedRevision {
		cloudflareProblem(w, 409, "cloudflare_config_conflict", "Cloudflare settings changed. Reload before rotating the token.")
		return
	}
	client, err := m.client(input.Token)
	if err != nil {
		cloudflareProblem(w, 400, "invalid_cloudflare_token", "Provide a valid new Cloudflare API token.")
		return
	}
	defer client.CloseIdleConnections()
	ctx, cancel := m.requestContext(r.Context())
	defer cancel()
	_, err = client.VerifyToken(ctx)
	if err == nil && previous.Management.ZoneID != "" {
		err = client.CheckDNSAccess(ctx, previous.Management.ZoneID)
	}
	if err == nil && previous.Management.AccountID != "" && previous.Management.TunnelID != "" {
		_, err = client.GetTunnel(ctx, previous.Management.AccountID, previous.Management.TunnelID)
	}
	if m.requestEnded(w, r) {
		return
	}
	if err != nil {
		cloudflareProblem(w, 400, "cloudflare_validation_failed", err.Error())
		return
	}
	m.desktop.op.Lock()
	defer m.desktop.op.Unlock()
	if m.requestEnded(w, r) {
		return
	}
	m.desktop.mu.Lock()
	closed := m.desktop.closed || m.desktop.ctx.Err() != nil
	m.desktop.mu.Unlock()
	if closed {
		cloudflareProblem(w, 503, "cloudflare_stopping", "AXV is stopping. Reopen it before rotating the token.")
		return
	}
	lock, err := profile.Lock(m.home)
	if err != nil {
		cloudflareProblem(w, 409, "cloudflare_config_busy", "Another profile change is running. Retry after it finishes.")
		return
	}
	defer lock.Close()
	saved, next, err := profile.SaveCloudflareAPIToken(m.home, input.ExpectedRevision, input.Token)
	if errors.Is(err, profile.ErrCloudflareRevision) {
		cloudflareProblem(w, 409, "cloudflare_config_conflict", err.Error())
		return
	}
	if err != nil {
		cloudflareProblem(w, 500, "cloudflare_config_failed", "Could not save the new token. Reload configuration before retrying.")
		return
	}
	m.mu.Lock()
	m.healthRevision = ""
	m.mu.Unlock()
	cloudflareJSON(w, cloudflareConfigProjection(m.instanceID, next, saved))
}

func (m *cloudflareManagement) getConfig(w http.ResponseWriter, r *http.Request) {
	if !localaccess.Request(r, false) {
		cloudflareProblem(w, 403, "cloudflare_local_only", "Configure Cloudflare on the computer running AXV.")
		return
	}
	c, revision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		cloudflareProblem(w, 500, "cloudflare_config_failed", "Could not read Cloudflare configuration. Inspect the local profile; no settings were replaced.")
		return
	}
	cloudflareJSON(w, cloudflareConfigProjection(m.instanceID, revision, c))
}

func (m *cloudflareManagement) putConfig(w http.ResponseWriter, r *http.Request) {
	if !localaccess.Request(r, true) {
		cloudflareProblem(w, 403, "cloudflare_local_only", "Configure Cloudflare on the computer running AXV.")
		return
	}
	var input cloudflareConfigUpdate
	if !cloudflareDecode(w, r, &input) {
		return
	}
	if input.Access == nil || input.Account == nil {
		cloudflareProblem(w, 400, "invalid_cloudflare_config", "Provide the complete reviewed desktop and account settings objects.")
		return
	}
	if input.InstanceID == "" || input.InstanceID != m.instanceID {
		cloudflareProblem(w, 409, "cloudflare_instance_changed", "This desktop instance changed. Reload Cloudflare settings before saving.")
		return
	}
	if input.TokenAction != "keep" && input.TokenAction != "replace" && input.TokenAction != "clear" || input.TokenAction != "replace" && input.Token != "" || input.TokenAction == "replace" && input.Token == "" {
		cloudflareProblem(w, 400, "invalid_cloudflare_token_action", "Choose whether to keep, replace or clear the saved API token.")
		return
	}
	m.desktop.op.Lock()
	defer m.desktop.op.Unlock()
	if m.requestEnded(w, r) {
		return
	}
	m.desktop.mu.Lock()
	enabled, closed := m.desktop.enabled || m.desktop.service != nil || m.desktop.closing || m.desktop.closeErr != nil, m.desktop.closed || m.desktop.ctx.Err() != nil
	m.desktop.mu.Unlock()
	if closed {
		cloudflareProblem(w, 503, "cloudflare_stopping", "AXV is stopping. Reopen it before saving settings.")
		return
	}
	if enabled {
		cloudflareProblem(w, 409, "cloudflare_disable_required", "Disable desktop access before changing Cloudflare settings. If its previous listener could not close, restart AXV first.")
		return
	}
	lock, err := profile.Lock(m.home)
	if err != nil {
		cloudflareProblem(w, 409, "cloudflare_config_busy", "Another profile change is running. Retry after it finishes.")
		return
	}
	defer lock.Close()
	if m.busy != nil && m.busy() {
		cloudflareProblem(w, 409, "cloudflare_publications_active", "Unpublish services and finish pending cleanup before changing Cloudflare settings.")
		return
	}
	previous, _, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		cloudflareProblem(w, 500, "cloudflare_config_failed", "Could not read the existing Cloudflare settings; they were preserved.")
		return
	}
	token := previous.Management.APIToken
	if input.TokenAction == "replace" {
		token = input.Token
	}
	if input.TokenAction == "clear" {
		token = ""
	}
	listen, publishListen := input.Access.Listen, input.Account.Listen
	if listen == "" {
		listen = profile.DefaultCloudflareListen
	}
	if publishListen == "" {
		publishListen = profile.DefaultCloudflareManagementListen
	}
	next := profile.Cloudflare{Listen: listen, Origin: input.Access.Origin, Issuer: input.Access.Issuer, Audience: input.Access.Audience, OwnerSubject: input.Access.OwnerSubject,
		Management: profile.CloudflareManagement{APIToken: token, AccountID: input.Account.AccountID, ZoneID: input.Account.ZoneID, TunnelID: input.Account.TunnelID, Domain: input.Account.Domain, Listen: publishListen, Audience: input.Account.Audience}}
	if err := profile.ValidateCloudflare(next); err != nil {
		cloudflareProblem(w, 400, "invalid_cloudflare_config", err.Error())
		return
	}
	if m.validateDesktopOrigin != nil {
		if err := m.validateDesktopOrigin(next.Origin); err != nil {
			cloudflareProblem(w, 409, "cloudflare_desktop_site_reserved", err.Error())
			return
		}
	}
	saved, revision, err := profile.SaveCloudflareConfiguration(m.home, input.ExpectedRevision, next)
	if errors.Is(err, profile.ErrCloudflareRevision) {
		cloudflareProblem(w, 409, "cloudflare_config_conflict", err.Error())
		return
	}
	if err != nil {
		cloudflareProblem(w, 500, "cloudflare_config_failed", "Could not save Cloudflare settings. Reload and inspect the current configuration before retrying.")
		return
	}
	m.mu.Lock()
	m.health = cloudflareHealth{}
	m.healthRevision = ""
	m.mu.Unlock()
	cloudflareJSON(w, cloudflareConfigProjection(m.instanceID, revision, saved))
}

type cloudflareWizardInput struct {
	Step      string                  `json:"step"`
	Token     string                  `json:"token,omitempty"`
	AccountID string                  `json:"accountId,omitempty"`
	ZoneID    string                  `json:"zoneId,omitempty"`
	TunnelID  string                  `json:"tunnelId,omitempty"`
	Domain    string                  `json:"domain,omitempty"`
	Access    *cloudflareAccessConfig `json:"access,omitempty"`
}

func (m *cloudflareManagement) wizard(w http.ResponseWriter, r *http.Request) {
	if !localaccess.Request(r, true) {
		cloudflareProblem(w, 403, "cloudflare_local_only", "Configure Cloudflare on the computer running AXV.")
		return
	}
	var input cloudflareWizardInput
	if !cloudflareDecode(w, r, &input) {
		return
	}
	result := map[string]any{"ok": true}
	if input.Step == "access" {
		if input.Access == nil {
			cloudflareProblem(w, 400, "invalid_cloudflare_config", "Provide desktop Access settings.")
			return
		}
		a := input.Access
		if err := profile.ValidateCloudflare(profile.Cloudflare{Enabled: true, Listen: a.Listen, Origin: a.Origin, Issuer: a.Issuer, Audience: a.Audience, OwnerSubject: a.OwnerSubject}); err != nil {
			cloudflareProblem(w, 400, "invalid_cloudflare_config", err.Error())
			return
		}
		result["message"] = "Desktop settings are valid. Verify the external Access policy and owner sign-in after enabling."
		cloudflareJSON(w, result)
		return
	}
	if input.Step != "token" && input.Step != "account" && input.Step != "zone" && input.Step != "tunnel" && input.Step != "domain" {
		cloudflareProblem(w, 400, "invalid_cloudflare_step", "Choose a valid Cloudflare setup step.")
		return
	}
	token := input.Token
	if token == "" {
		c, _, err := profile.ReadCloudflareConfiguration(m.home)
		if err != nil {
			cloudflareProblem(w, 500, "cloudflare_config_failed", "Could not read the saved API token.")
			return
		}
		token = c.Management.APIToken
	}
	client, err := m.client(token)
	if err != nil {
		cloudflareProblem(w, 400, "invalid_cloudflare_token", "Provide a valid Cloudflare API token.")
		return
	}
	defer client.CloseIdleConnections()
	ctx, cancel := m.requestContext(r.Context())
	defer cancel()
	switch input.Step {
	case "token":
		_, err = client.VerifyToken(ctx)
		if err == nil {
			accounts, listErr := client.ListAccounts(ctx)
			if listErr != nil {
				result["warnings"] = []string{"Account listing is unavailable. Enter the scoped account ID manually."}
			} else {
				result["accounts"] = accounts
			}
		}
	case "account":
		var zones []cloudflareapi.Zone
		var tunnels []cloudflareapi.Tunnel
		tunnels, err = client.ListTunnels(ctx, input.AccountID)
		if err == nil {
			zones, err = client.ListZones(ctx, input.AccountID)
		}
		if err == nil {
			result["zones"] = zones
			result["tunnels"] = cloudflareTunnelChoices(tunnels)
		}
	case "zone", "domain":
		var zone cloudflareapi.Zone
		zone, err = client.GetZone(ctx, input.ZoneID)
		if err == nil && input.AccountID != "" && zone.Account.ID != input.AccountID {
			cloudflareProblem(w, 400, "cloudflare_zone_mismatch", "Choose a zone in the selected Cloudflare account.")
			return
		}
		if err == nil && input.Step == "domain" && (!profile.CloudflareOrigin("https://"+input.Domain) || input.Domain != zone.Name && !strings.HasSuffix(input.Domain, "."+zone.Name)) {
			cloudflareProblem(w, 400, "cloudflare_domain_mismatch", "Choose the selected zone or one of its subdomains.")
			return
		}
		if err == nil && input.Step == "domain" {
			if isolationErr := profile.ValidateAllocationDomain(input.Domain); isolationErr != nil {
				cloudflareProblem(w, 400, "cloudflare_domain_isolation", isolationErr.Error())
				return
			}
			if input.Access != nil {
				if isolationErr := profile.ValidateCloudflareIsolation(input.Access.Origin, input.Domain); isolationErr != nil {
					cloudflareProblem(w, 400, "cloudflare_domain_isolation", isolationErr.Error())
					return
				}
			}
		}
		if err == nil {
			err = client.CheckDNSAccess(ctx, input.ZoneID)
		}
		if err == nil {
			result["zoneName"] = zone.Name
			result["warnings"] = []string{"DNS read access was checked. DNS Edit permission is required when publishing."}
		}
	case "tunnel":
		var tunnel cloudflareapi.Tunnel
		tunnel, err = client.GetTunnel(ctx, input.AccountID, input.TunnelID)
		if err == nil && !tunnel.RemoteConfig {
			cloudflareProblem(w, 400, "cloudflare_tunnel_local", "Choose a remotely managed tunnel so AXV can manage service routes. Existing connector credentials remain external.")
			return
		}
		if err == nil {
			result["tunnels"] = cloudflareTunnelChoices([]cloudflareapi.Tunnel{tunnel})
			if tunnel.Status != "healthy" {
				result["warnings"] = []string{"The tunnel is not healthy. Start or inspect its external cloudflared connector."}
			}
		}
	}
	if m.requestEnded(w, r) {
		return
	}
	if err != nil {
		cloudflareProblem(w, 400, "cloudflare_validation_failed", err.Error())
		return
	}
	result["message"] = "Cloudflare setup step validated."
	cloudflareJSON(w, result)
}

func cloudflareTunnelChoices(tunnels []cloudflareapi.Tunnel) []map[string]any {
	choices := make([]map[string]any, 0, len(tunnels))
	for _, t := range tunnels {
		choices = append(choices, map[string]any{"id": t.ID, "name": t.Name, "status": t.Status, "remoteConfig": t.RemoteConfig})
	}
	return choices
}

func (m *cloudflareManagement) getHealth(w http.ResponseWriter, r *http.Request) {
	if !localaccess.AllowRequest(r, false) {
		cloudflareProblem(w, 403, "cloudflare_forbidden", "Authenticated desktop access is required.")
		return
	}
	if r.URL.RawQuery != "" && r.URL.RawQuery != "force=true" {
		cloudflareProblem(w, 400, "invalid_cloudflare_request", "Use only force=true to refresh tunnel health.")
		return
	}
	m.healthOp.Lock()
	defer m.healthOp.Unlock()
	if m.requestEnded(w, r) {
		return
	}
	c, revision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		cloudflareProblem(w, 500, "cloudflare_config_failed", "Cloudflare settings are unavailable. Inspect the profile locally.")
		return
	}
	m.mu.Lock()
	cached, fresh := m.health, m.healthRevision == revision && r.URL.Query().Get("force") != "true" && time.Since(m.health.CheckedAt) < time.Minute
	m.mu.Unlock()
	if fresh {
		cloudflareJSON(w, cached)
		return
	}
	value := cloudflareHealth{State: "unconfigured", CheckedAt: time.Now().UTC(), Message: "Connect a Cloudflare account to check tunnel health."}
	if c.Management.Configured() {
		value.State = "error"
		client, clientErr := m.client(c.Management.APIToken)
		if clientErr == nil {
			defer client.CloseIdleConnections()
			ctx, cancel := m.requestContext(r.Context())
			defer cancel()
			_, clientErr = client.VerifyToken(ctx)
			if clientErr == nil {
				clientErr = client.CheckDNSAccess(ctx, c.Management.ZoneID)
			}
			if clientErr == nil {
				tunnel, tunnelErr := client.GetTunnel(ctx, c.Management.AccountID, c.Management.TunnelID)
				clientErr = tunnelErr
				if clientErr == nil {
					value.TunnelName, value.TunnelStatus = tunnel.Name, tunnel.Status
					value.State = "degraded"
					value.Message = "The tunnel needs attention. Check its external connector; public application reachability has not been verified."
					if tunnel.Status == "healthy" && tunnel.RemoteConfig {
						value.State = "healthy"
						value.Message = "Token, DNS read access and tunnel status check out. Public application reachability has not been verified."
					}
				}
			}
		}
		if clientErr != nil {
			value.Message = clientErr.Error()
		}
	}
	if m.requestEnded(w, r) {
		return
	}
	_, latestRevision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		cloudflareProblem(w, 500, "cloudflare_config_failed", "Cloudflare settings became unavailable during the health check. Inspect the profile locally.")
		return
	}
	if latestRevision != revision {
		cloudflareProblem(w, 409, "cloudflare_config_conflict", "Cloudflare settings changed during the health check. Refresh health for the current settings.")
		return
	}
	m.mu.Lock()
	m.health, m.healthRevision = value, revision
	m.mu.Unlock()
	cloudflareJSON(w, value)
}

func (m *cloudflareManagement) getTraffic(w http.ResponseWriter, r *http.Request) {
	if !localaccess.AllowRequest(r, false) {
		cloudflareProblem(w, 403, "cloudflare_forbidden", "Authenticated desktop access is required.")
		return
	}
	hours := 24
	query := r.URL.Query()
	if len(query) > 1 || len(query["hours"]) > 1 || len(query) == 1 && query.Get("hours") == "" {
		cloudflareProblem(w, 400, "invalid_cloudflare_request", "Choose a traffic window of 1, 6, 12, 24, 168 or 720 hours.")
		return
	}
	if value := query.Get("hours"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n != 1 && n != 6 && n != 12 && n != 24 && n != 168 && n != 720 {
			cloudflareProblem(w, 400, "invalid_cloudflare_request", "Choose a traffic window of 1, 6, 12, 24, 168 or 720 hours.")
			return
		}
		hours = n
	}
	c, _, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		cloudflareProblem(w, 500, "cloudflare_config_failed", "Cloudflare settings are unavailable. Inspect the profile locally.")
		return
	}
	// Use complete hourly buckets. Extending the start of a rolling window
	// would exceed its requested duration and can cross provider plan limits.
	until := time.Now().UTC().Truncate(time.Hour)
	since := until.Add(-time.Duration(hours) * time.Hour)
	if !c.Management.Configured() {
		cloudflareJSON(w, map[string]any{"supported": false, "reason": "Connect a Cloudflare account to read zone traffic.", "since": since, "until": until})
		return
	}
	client, err := m.client(c.Management.APIToken)
	if err != nil {
		cloudflareProblem(w, 500, "cloudflare_config_failed", "The Cloudflare API token is invalid. Check it locally.")
		return
	}
	defer client.CloseIdleConnections()
	ctx, cancel := m.requestContext(r.Context())
	defer cancel()
	traffic, err := client.Traffic(ctx, cloudflareapi.TrafficRequest{ZoneID: c.Management.ZoneID, Since: since, Until: until})
	if m.requestEnded(w, r) {
		return
	}
	if err != nil {
		cloudflareJSON(w, map[string]any{"supported": false, "reason": err.Error(), "since": since, "until": until})
		return
	}
	data, _ := json.Marshal(traffic)
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		cloudflareProblem(w, 502, "cloudflare_traffic_failed", "Cloudflare traffic could not be read.")
		return
	}
	result["supported"] = true
	result["scope"] = "zone"
	cloudflareJSON(w, result)
}
