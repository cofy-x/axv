package management

import (
	"context"
	"errors"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func configProjection(instanceID, revision string, c policy.Configuration) ConfigView {
	return ConfigView{InstanceID: instanceID, Revision: revision,
		Access:  AccessConfig{c.Enabled, c.Listen, c.Origin, c.Issuer, c.Audience, c.OwnerSubject},
		Account: AccountConfig{c.Management.Configured(), c.Management.APIToken != "", c.Management.AccountID, c.Management.ZoneID, c.Management.TunnelID, c.Management.Domain, c.Management.Listen, c.Management.Audience}}
}

func (m *Service) Configuration(request context.Context) (ConfigView, error) {
	c, revision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		return ConfigView{}, problem("cloudflare_config_failed", "Could not read Cloudflare configuration. Inspect the local profile; no settings were replaced.")
	}
	return configProjection(m.instanceID, revision, c), nil
}

func (m *Service) SaveConfiguration(request context.Context, input ConfigUpdate) (ConfigView, error) {
	if input.Access == nil || input.Account == nil {
		return ConfigView{}, problem("invalid_cloudflare_config", "Provide the complete reviewed desktop and account settings objects.")
	}
	if input.InstanceID == "" || input.InstanceID != m.instanceID {
		return ConfigView{}, problem("cloudflare_instance_changed", "This desktop instance changed. Reload Cloudflare settings before saving.")
	}
	if input.TokenAction != "keep" && input.TokenAction != "replace" && input.TokenAction != "clear" || input.TokenAction != "replace" && input.Token != "" || input.TokenAction == "replace" && input.Token == "" {
		return ConfigView{}, problem("invalid_cloudflare_token_action", "Choose whether to keep, replace or clear the saved API token.")
	}

	var result ConfigView
	err := m.desktop.ChangeConfiguration(request, func(state DesktopState) error {
		if err := m.requestError(request); err != nil {
			return err
		}
		if state.Stopping {
			return problem("cloudflare_stopping", "AXV is stopping. Reopen it before saving settings.")
		}
		if state.Active {
			return problem("cloudflare_disable_required", "Disable desktop access before changing Cloudflare settings. If its previous listener could not close, restart AXV first.")
		}
		lock, err := profile.Lock(m.home)
		if err != nil {
			return problem("cloudflare_config_busy", "Another profile change is running. Retry after it finishes.")
		}
		defer lock.Close()
		if m.busy != nil && m.busy() {
			return problem("cloudflare_publications_active", "Unpublish services and finish pending cleanup before changing Cloudflare settings.")
		}
		previous, _, err := profile.ReadCloudflareConfiguration(m.home)
		if err != nil {
			return problem("cloudflare_config_failed", "Could not read the existing Cloudflare settings; they were preserved.")
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
			listen = policy.DefaultDesktopListen
		}
		if publishListen == "" {
			publishListen = policy.DefaultPublishListen
		}
		next := policy.Configuration{Listen: listen, Origin: input.Access.Origin, Issuer: input.Access.Issuer, Audience: input.Access.Audience, OwnerSubject: input.Access.OwnerSubject,
			Management: policy.Account{APIToken: token, AccountID: input.Account.AccountID, ZoneID: input.Account.ZoneID, TunnelID: input.Account.TunnelID, Domain: input.Account.Domain, Listen: publishListen, Audience: input.Account.Audience}}
		if err := policy.Validate(next); err != nil {
			return problem("invalid_cloudflare_config", err.Error())
		}
		if m.validateDesktopOrigin != nil {
			if err := m.validateDesktopOrigin(next.Origin); err != nil {
				return problem("cloudflare_desktop_site_reserved", err.Error())
			}
		}
		saved, revision, err := profile.SaveCloudflareConfiguration(m.home, input.ExpectedRevision, next)
		if errors.Is(err, profile.ErrCloudflareRevision) {
			return problem("cloudflare_config_conflict", err.Error())
		}
		if err != nil {
			return problem("cloudflare_config_failed", "Could not save Cloudflare settings. Reload and inspect the current configuration before retrying.")
		}
		m.mu.Lock()
		m.health = Health{}
		m.healthRevision = ""
		m.mu.Unlock()

		result = configProjection(m.instanceID, revision, saved)
		return nil
	})
	return result, err
}

func (m *Service) RotateToken(request context.Context, input TokenUpdate) (ConfigView, error) {
	if input.InstanceID == "" || input.InstanceID != m.instanceID {
		return ConfigView{}, problem("cloudflare_instance_changed", "Reload the current desktop before rotating the token.")
	}
	previous, revision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		return ConfigView{}, problem("cloudflare_config_failed", "Could not read Cloudflare settings; the current token was preserved.")
	}
	if revision != input.ExpectedRevision {
		return ConfigView{}, problem("cloudflare_config_conflict", "Cloudflare settings changed. Reload before rotating the token.")
	}
	client, err := m.client(input.Token)
	if err != nil {
		return ConfigView{}, problem("invalid_cloudflare_token", "Provide a valid new Cloudflare API token.")
	}
	defer client.CloseIdleConnections()
	ctx, cancel := m.requestContext(request)
	defer cancel()
	_, err = client.VerifyToken(ctx)
	if err == nil && previous.Management.ZoneID != "" {
		err = client.CheckDNSAccess(ctx, previous.Management.ZoneID)
	}
	if err == nil && previous.Management.AccountID != "" && previous.Management.TunnelID != "" {
		_, err = client.GetTunnel(ctx, previous.Management.AccountID, previous.Management.TunnelID)
	}
	if err := m.requestError(request); err != nil {
		return ConfigView{}, err
	}
	if err != nil {
		return ConfigView{}, problem("cloudflare_validation_failed", err.Error())
	}

	var result ConfigView
	err = m.desktop.ChangeConfiguration(request, func(state DesktopState) error {
		if err := m.requestError(request); err != nil {
			return err
		}
		if state.Stopping {
			return problem("cloudflare_stopping", "AXV is stopping. Reopen it before rotating the token.")
		}
		lock, err := profile.Lock(m.home)
		if err != nil {
			return problem("cloudflare_config_busy", "Another profile change is running. Retry after it finishes.")
		}
		defer lock.Close()
		saved, next, err := profile.SaveCloudflareAPIToken(m.home, input.ExpectedRevision, input.Token)
		if errors.Is(err, profile.ErrCloudflareRevision) {
			return problem("cloudflare_config_conflict", err.Error())
		}
		if err != nil {
			return problem("cloudflare_config_failed", "Could not save the new token. Reload configuration before retrying.")
		}
		m.mu.Lock()
		m.healthRevision = ""
		m.mu.Unlock()

		result = configProjection(m.instanceID, next, saved)
		return nil
	})
	return result, err
}
