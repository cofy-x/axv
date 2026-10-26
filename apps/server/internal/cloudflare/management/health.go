package management

import (
	"context"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func (m *Service) Health(request context.Context, force bool) (Health, error) {
	m.healthOp.Lock()
	defer m.healthOp.Unlock()
	if err := m.requestError(request); err != nil {
		return Health{}, err
	}
	c, revision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		return Health{}, problem("cloudflare_config_failed", "Cloudflare settings are unavailable. Inspect the profile locally.")
	}
	m.mu.Lock()
	cached, fresh := m.health, m.healthRevision == revision && !force && time.Since(m.health.CheckedAt) < time.Minute
	m.mu.Unlock()
	if fresh {
		return cached, nil
	}
	value := Health{State: "unconfigured", CheckedAt: time.Now().UTC(), Message: "Connect a Cloudflare account to check tunnel health."}
	if c.Management.Configured() {
		value.State = "error"
		client, clientErr := m.client(c.Management.APIToken)
		if clientErr == nil {
			defer client.CloseIdleConnections()
			ctx, cancel := m.requestContext(request)
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
	if err := m.requestError(request); err != nil {
		return Health{}, err
	}
	_, latestRevision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		return Health{}, problem("cloudflare_config_failed", "Cloudflare settings became unavailable during the health check. Inspect the profile locally.")
	}
	if latestRevision != revision {
		return Health{}, problem("cloudflare_config_conflict", "Cloudflare settings changed during the health check. Refresh health for the current settings.")
	}
	m.mu.Lock()
	m.health, m.healthRevision = value, revision
	m.mu.Unlock()
	return value, nil
}
