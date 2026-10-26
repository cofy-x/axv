package management

import (
	"context"
	"strings"

	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func (m *Service) Wizard(request context.Context, input WizardInput) (map[string]any, error) {
	result := map[string]any{"ok": true}
	if input.Step == "access" {
		if input.Access == nil {
			return nil, problem("invalid_cloudflare_config", "Provide desktop Access settings.")
		}
		a := input.Access
		if err := policy.Validate(policy.Configuration{Enabled: true, Listen: a.Listen, Origin: a.Origin, Issuer: a.Issuer, Audience: a.Audience, OwnerSubject: a.OwnerSubject}); err != nil {
			return nil, problem("invalid_cloudflare_config", err.Error())
		}
		result["message"] = "Desktop settings are valid. Verify the external Access policy and owner sign-in after enabling."
		return result, nil
	}
	if input.Step != "token" && input.Step != "account" && input.Step != "zone" && input.Step != "tunnel" && input.Step != "domain" {
		return nil, problem("invalid_cloudflare_step", "Choose a valid Cloudflare setup step.")
	}
	token := input.Token
	if token == "" {
		c, _, err := profile.ReadCloudflareConfiguration(m.home)
		if err != nil {
			return nil, problem("cloudflare_config_failed", "Could not read the saved API token.")
		}
		token = c.Management.APIToken
	}
	client, err := m.client(token)
	if err != nil {
		return nil, problem("invalid_cloudflare_token", "Provide a valid Cloudflare API token.")
	}
	defer client.CloseIdleConnections()
	ctx, cancel := m.requestContext(request)
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
		var zones []cfclient.Zone
		var tunnels []cfclient.Tunnel
		tunnels, err = client.ListTunnels(ctx, input.AccountID)
		if err == nil {
			zones, err = client.ListZones(ctx, input.AccountID)
		}
		if err == nil {
			result["zones"] = zones
			result["tunnels"] = tunnelChoices(tunnels)
		}
	case "zone", "domain":
		var zone cfclient.Zone
		zone, err = client.GetZone(ctx, input.ZoneID)
		if err == nil && input.AccountID != "" && zone.Account.ID != input.AccountID {
			return nil, problem("cloudflare_zone_mismatch", "Choose a zone in the selected Cloudflare account.")
		}
		if err == nil && input.Step == "domain" && (!policy.ValidOrigin("https://"+input.Domain) || input.Domain != zone.Name && !strings.HasSuffix(input.Domain, "."+zone.Name)) {
			return nil, problem("cloudflare_domain_mismatch", "Choose the selected zone or one of its subdomains.")
		}
		if err == nil && input.Step == "domain" {
			if isolationErr := policy.ValidateAllocationDomain(input.Domain); isolationErr != nil {
				return nil, problem("cloudflare_domain_isolation", isolationErr.Error())
			}
			if input.Access != nil {
				if isolationErr := policy.ValidateIsolation(input.Access.Origin, input.Domain); isolationErr != nil {
					return nil, problem("cloudflare_domain_isolation", isolationErr.Error())
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
		var tunnel cfclient.Tunnel
		tunnel, err = client.GetTunnel(ctx, input.AccountID, input.TunnelID)
		if err == nil && !tunnel.RemoteConfig {
			return nil, problem("cloudflare_tunnel_local", "Choose a remotely managed tunnel so AXV can manage service routes. Existing connector credentials remain external.")
		}
		if err == nil {
			result["tunnels"] = tunnelChoices([]cfclient.Tunnel{tunnel})
			if tunnel.Status != "healthy" {
				result["warnings"] = []string{"The tunnel is not healthy. Start or inspect its external cloudflared connector."}
			}
		}
	}
	if err := m.requestError(request); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, problem("cloudflare_validation_failed", err.Error())
	}
	result["message"] = "Cloudflare setup step validated."
	return result, nil
}

func tunnelChoices(tunnels []cfclient.Tunnel) []map[string]any {
	choices := make([]map[string]any, 0, len(tunnels))
	for _, t := range tunnels {
		choices = append(choices, map[string]any{"id": t.ID, "name": t.Name, "status": t.Status, "remoteConfig": t.RemoteConfig})
	}
	return choices
}
