package server

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"sync"

	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/tailnet"
)

type remoteService interface {
	Status() tailnet.Status
	Close() error
}

type remoteStarter func(context.Context, profile.Remote) (remoteService, error)

type remoteSnapshot struct {
	Enabled   bool `json:"enabled"`
	Supported bool `json:"supported"`
	tailnet.Status
}

type remoteControlError struct {
	status  int
	code    string
	message string
}

func (e *remoteControlError) Error() string { return e.message }

// remoteController owns mutable profile settings and one optional transport.
// Serial transitions finish closing the previous node before starting another;
// the shared workstation services retain their own independent lifetimes.
type remoteController struct {
	ctx       context.Context
	home      string
	supported bool
	start     remoteStarter
	op        sync.Mutex
	mu        sync.Mutex
	enabled   bool
	closed    bool
	node      remoteService
	fallback  tailnet.Status
	closing   bool
	closeErr  error
}

func newRemoteController(ctx context.Context, home string, handler http.Handler) *remoteController {
	return newRemoteControllerWithStarter(ctx, home, runtime.GOOS == "linux" || runtime.GOOS == "darwin", func(ctx context.Context, settings profile.Remote) (remoteService, error) {
		return startRemote(ctx, home, settings, handler)
	})
}

func newRemoteControllerWithStarter(ctx context.Context, home string, supported bool, start remoteStarter) *remoteController {
	c := &remoteController{ctx: ctx, home: home, supported: supported, start: start, fallback: tailnet.Status{State: tailnet.StateDisabled}}
	settings, err := profile.ReadRemote(home)
	c.enabled = settings.Enabled
	if err != nil {
		c.fallback = tailnet.Status{State: tailnet.StateError, Error: "The AXV remote configuration could not be read. Correct the profile and retry."}
		diagnostics.FromContext(ctx).Error("remote.config_failed", err)
	} else if settings.Enabled {
		if supported {
			c.startNode(settings)
		} else {
			c.fallback = tailnet.Status{State: tailnet.StateError, Error: "AXV remote access currently requires macOS or Linux."}
		}
	}
	return c
}

func (c *remoteController) Status() remoteSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := c.fallback
	if c.node != nil && !c.closing && c.closeErr == nil && !c.closed {
		status = c.node.Status()
	}
	return remoteSnapshot{Enabled: c.enabled, Supported: c.supported, Status: status}
}

func (c *remoteController) startNode(settings profile.Remote) {
	c.mu.Lock()
	c.fallback = tailnet.Status{State: tailnet.StateStarting}
	c.mu.Unlock()
	node, err := c.start(c.ctx, settings)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.fallback = tailnet.Status{State: tailnet.StateError, Error: err.Error()}
		diagnostics.FromContext(c.ctx).Error("remote.unavailable", err)
		return
	}
	c.node = node
}

func (c *remoteController) SetEnabled(ctx context.Context, enabled bool) error {
	c.op.Lock()
	defer c.op.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	closed, closeErr := c.closed || c.ctx.Err() != nil, c.closeErr
	c.mu.Unlock()
	if closed {
		return &remoteControlError{http.StatusServiceUnavailable, "remote_stopping", "AXV is stopping. Reopen AXV before changing remote access."}
	}
	if enabled && !c.supported {
		return &remoteControlError{http.StatusNotImplemented, "remote_unsupported", "AXV remote access currently requires macOS or Linux."}
	}
	if closeErr != nil {
		return &remoteControlError{http.StatusConflict, "remote_close_failed", "The previous remote node could not close cleanly. Restart AXV before changing remote access."}
	}
	lock, err := profile.Lock(c.home)
	if err != nil {
		return &remoteControlError{http.StatusConflict, "remote_config_busy", "The AXV profile could not be locked. Retry after other configuration changes finish."}
	}
	settings, err := profile.SaveRemoteEnabled(c.home, enabled)
	lockErr := lock.Close()
	if err != nil {
		diagnostics.FromContext(c.ctx).Error("remote.config_failed", err)
		return &remoteControlError{http.StatusInternalServerError, "remote_config_failed", "Could not save AXV remote access settings. The connection was not changed."}
	}
	if lockErr != nil {
		diagnostics.FromContext(c.ctx).Error("remote.config_unlock_failed", lockErr)
	}
	// Once the setting is committed, complete its transition even if the browser
	// disconnects. The node belongs to AXV's lifecycle, never the HTTP request.
	c.mu.Lock()
	c.enabled = enabled
	node := c.node
	if enabled && node != nil {
		state := node.Status().State
		if state != tailnet.StateError && state != tailnet.StateStopped {
			c.mu.Unlock()
			return nil
		}
	}
	c.closing = node != nil
	c.fallback = tailnet.Status{State: tailnet.StateStopped}
	c.mu.Unlock()
	if node != nil {
		if err := node.Close(); err != nil {
			c.mu.Lock()
			c.closeErr = err
			c.fallback = tailnet.Status{State: tailnet.StateError, Error: "The previous remote node could not close cleanly. Restart AXV before enabling it again."}
			c.mu.Unlock()
			diagnostics.FromContext(c.ctx).Error("remote.close_failed", err)
			return &remoteControlError{http.StatusConflict, "remote_close_failed", "The previous remote node could not close cleanly. Restart AXV before changing remote access."}
		}
	}
	c.mu.Lock()
	c.node, c.closing = nil, false
	c.fallback = tailnet.Status{State: tailnet.StateDisabled}
	c.mu.Unlock()
	if enabled {
		c.startNode(settings)
	}
	return nil
}

// Close preserves the durable enabled choice and credentials for AXV's next
// service launch. It never stops shared managers or writes profile settings.
func (c *remoteController) Close() error {
	c.op.Lock()
	defer c.op.Unlock()
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.closed, c.closing = true, true
	c.fallback = tailnet.Status{State: tailnet.StateStopped}
	node, previous := c.node, c.closeErr
	c.mu.Unlock()
	var err error
	if node != nil && previous == nil {
		err = node.Close()
	}
	c.mu.Lock()
	c.closeErr = errors.Join(previous, err)
	c.mu.Unlock()
	return errors.Join(previous, err)
}
