package server

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/desktop"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/management"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type cloudflareService interface {
	Status() desktop.Status
	Close() error
}

type cloudflareStarter func(context.Context, policy.Configuration) (cloudflareService, error)

// cloudflareController owns one authenticated ingress, not the external tunnel
// process. Transitions preserve shared services and close the old entrypoint
// completely before starting a replacement.
type cloudflareController struct {
	ctx            context.Context
	home           string
	start          cloudflareStarter
	validateOrigin func(string) error
	boundOrigin    string
	op             sync.Mutex
	mu             sync.Mutex
	enabled        bool
	configured     bool
	closed         bool
	service        cloudflareService
	fallback       desktop.Status
	closing        bool
	closeErr       error
}

func newCloudflareController(ctx context.Context, home string, handler http.Handler, validateOrigin func(string) error) *cloudflareController {
	return newCloudflareControllerGuarded(ctx, home, func(ctx context.Context, settings policy.Configuration) (cloudflareService, error) {
		return desktop.Start(ctx, desktop.Options{
			Listen: settings.Listen, Origin: settings.Origin, Issuer: settings.Issuer,
			Audience: settings.Audience, OwnerSubject: settings.OwnerSubject, Handler: handler,
			AllowRoute: remoteRoute, LocalOnlyRoute: remoteLocalOnlyRoute,
		})
	}, validateOrigin)
}

func newCloudflareControllerWithStarter(ctx context.Context, home string, start cloudflareStarter) *cloudflareController {
	return newCloudflareControllerGuarded(ctx, home, start, nil)
}

func newCloudflareControllerGuarded(ctx context.Context, home string, start cloudflareStarter, validateOrigin func(string) error) *cloudflareController {
	c := &cloudflareController{ctx: ctx, home: home, start: start, validateOrigin: validateOrigin, fallback: desktop.Status{State: "disabled"}}
	settings, err := profile.ReadCloudflare(home)
	c.enabled = settings.Enabled
	if err != nil {
		c.fallback = desktop.Status{State: "error", Error: "Cloudflare configuration: " + err.Error()}
		diagnostics.FromContext(ctx).Error("cloudflare.config_failed", err)
		return c
	}
	c.configured = configuredCloudflare(settings)
	c.fallback.Listen = settings.Listen
	if settings.Enabled {
		c.startIngress(settings)
	}
	return c
}

func configuredCloudflare(settings policy.Configuration) bool {
	settings.Enabled = true
	return policy.Validate(settings) == nil
}

func (c *cloudflareController) Status() desktop.Snapshot {
	c.mu.Lock()
	status := c.fallback
	enabled, configured := c.enabled, c.configured
	if c.service != nil && !c.closing && c.closeErr == nil && !c.closed {
		status = c.service.Status()
	}
	refresh := c.service == nil && !c.closing && !c.closed
	projectDisabled := !enabled && c.closeErr == nil
	c.mu.Unlock()
	// An operator can complete a disabled ingress configuration while AXV is
	// running. Refresh its eligibility without starting networking or replacing
	// the identity of an already running ingress.
	if refresh {
		settings, err := profile.ReadCloudflare(c.home)
		configured = err == nil && configuredCloudflare(settings)
		if err != nil {
			status = desktop.Status{State: "error", Error: "Cloudflare configuration: " + err.Error()}
		} else if projectDisabled {
			status = desktop.Status{State: "disabled", Listen: settings.Listen}
		} else {
			status.Listen = settings.Listen
		}
	}
	return desktop.Snapshot{Enabled: enabled, Configured: configured, Status: status}
}

func (c *cloudflareController) startIngress(settings policy.Configuration) {
	c.mu.Lock()
	c.boundOrigin = settings.Origin
	c.fallback = desktop.Status{State: "starting", Listen: settings.Listen}
	c.mu.Unlock()
	var service cloudflareService
	var err error
	if c.validateOrigin != nil {
		err = c.validateOrigin(settings.Origin)
	}
	if err == nil {
		service, err = c.start(c.ctx, settings)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.fallback = desktop.Status{State: "error", Listen: settings.Listen, Error: "Cloudflare ingress: " + err.Error()}
		diagnostics.FromContext(c.ctx).Error("cloudflare.start_failed", err)
		return
	}
	c.service = service
}

// BoundOrigin tracks the identity captured by the actual entrypoint, including
// a listener whose close failed. Reading a newer profile is not a rebind.
func (c *cloudflareController) BoundOrigin() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.boundOrigin
}

// ChangeConfiguration holds the same operation boundary as enable, disable and
// shutdown through the complete profile read/check/save transaction.
func (c *cloudflareController) ChangeConfiguration(ctx context.Context, change func(management.DesktopState) error) error {
	c.op.Lock()
	defer c.op.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	state := management.DesktopState{
		Active:   c.enabled || c.service != nil || c.closing || c.closeErr != nil,
		Stopping: c.closed || c.ctx.Err() != nil,
	}
	c.mu.Unlock()
	return change(state)
}

func (c *cloudflareController) SetEnabled(ctx context.Context, enabled bool) error {
	c.op.Lock()
	defer c.op.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	closed, closeErr := c.closed || c.ctx.Err() != nil, c.closeErr
	c.mu.Unlock()
	if closed {
		return &management.Problem{Code: "cloudflare_stopping", Message: "AXV is stopping. Reopen AXV before changing Cloudflare access."}
	}
	if closeErr != nil {
		return &management.Problem{Code: "cloudflare_close_failed", Message: "The previous Cloudflare ingress could not close cleanly. Restart AXV before changing access."}
	}
	lock, err := profile.Lock(c.home)
	if err != nil {
		return &management.Problem{Code: "cloudflare_config_busy", Message: "The AXV profile could not be locked. Retry after other configuration changes finish."}
	}
	settings, err := profile.SaveCloudflareEnabled(c.home, enabled)
	lockErr := lock.Close()
	if err != nil {
		diagnostics.FromContext(c.ctx).Error("cloudflare.config_failed", err)
		return &management.Problem{Code: "cloudflare_config_failed", Message: "Could not save Cloudflare access settings. Check the local configuration; the connection was not changed."}
	}
	if lockErr != nil {
		diagnostics.FromContext(c.ctx).Error("cloudflare.config_unlock_failed", lockErr)
	}
	// Once the switch is committed, finish the transition even if this browser
	// disconnects. The ingress belongs to the desktop service lifecycle.
	c.mu.Lock()
	c.enabled, c.configured = enabled, configuredCloudflare(settings)
	service := c.service
	if enabled && service != nil {
		state := service.Status().State
		if state != "error" && state != "stopped" {
			c.mu.Unlock()
			return nil
		}
	}
	c.closing = service != nil
	c.fallback = desktop.Status{State: "stopped", Listen: settings.Listen}
	c.mu.Unlock()
	if service != nil {
		if err := service.Close(); err != nil {
			c.mu.Lock()
			c.closeErr = err
			c.fallback = desktop.Status{State: "error", Listen: settings.Listen, Error: "The previous Cloudflare ingress could not close cleanly. Restart AXV before enabling it again."}
			c.mu.Unlock()
			diagnostics.FromContext(c.ctx).Error("cloudflare.close_failed", err)
			return &management.Problem{Code: "cloudflare_close_failed", Message: "The previous Cloudflare ingress could not close cleanly. Restart AXV before changing access."}
		}
	}
	c.mu.Lock()
	c.service, c.closing = nil, false
	c.boundOrigin = ""
	c.fallback = desktop.Status{State: "disabled", Listen: settings.Listen}
	c.mu.Unlock()
	if enabled {
		c.startIngress(settings)
	}
	return nil
}

// Close withdraws only this ingress and its clients. It preserves the durable
// enabled setting and never stops shared feature managers or cloudflared.
func (c *cloudflareController) Close() error {
	c.op.Lock()
	defer c.op.Unlock()
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.closed, c.closing = true, true
	c.fallback.State, c.fallback.URL = "stopped", ""
	service, previous := c.service, c.closeErr
	c.mu.Unlock()
	var err error
	if service != nil && previous == nil {
		err = service.Close()
	}
	c.mu.Lock()
	c.closeErr = errors.Join(previous, err)
	if c.closeErr == nil {
		c.boundOrigin = ""
	}
	c.mu.Unlock()
	return errors.Join(previous, err)
}
