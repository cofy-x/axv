// Package management owns Cloudflare configuration changes, setup checks, health and traffic.
// HTTP authority checks belong to the adapter; profile writes retain revision and lifecycle guards.
package management

import (
	"context"
	"sync"
	"time"

	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
)

// ConfigurationGate serializes a complete configuration change against desktop
// listener transitions. The callback acquires the profile lock before checking
// publication ownership and saving a revision.
type ConfigurationGate interface {
	ChangeConfiguration(context.Context, func(DesktopState) error) error
}

type Options struct {
	Home, InstanceID      string
	Desktop               ConfigurationGate
	Busy                  func() bool
	ValidateDesktopOrigin func(string) error
	Client                func(string) (*cfclient.Client, error)
}

type Service struct {
	home, instanceID      string
	ctx                   context.Context
	cancel                context.CancelFunc
	desktop               ConfigurationGate
	busy                  func() bool
	validateDesktopOrigin func(string) error
	client                func(string) (*cfclient.Client, error)
	mu                    sync.Mutex
	healthOp              sync.Mutex
	health                Health
	healthRevision        string
}

func New(parent context.Context, options Options) *Service {
	ctx, cancel := context.WithCancel(parent)
	if options.Client == nil {
		options.Client = func(token string) (*cfclient.Client, error) { return cfclient.New(cfclient.Options{Token: token}) }
	}
	return &Service{home: options.Home, instanceID: options.InstanceID, ctx: ctx, cancel: cancel, desktop: options.Desktop, busy: options.Busy, validateDesktopOrigin: options.ValidateDesktopOrigin, client: options.Client}
}

// Close cancels provider work without stopping the desktop or publication managers.
func (m *Service) Close() { m.cancel() }
func (m *Service) requestContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	stop := context.AfterFunc(m.ctx, cancel)
	if m.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func (m *Service) requestError(request context.Context) error {
	if err := request.Err(); err != nil {
		return err
	}
	if m.ctx.Err() != nil {
		return problem("cloudflare_stopping", "AXV is stopping. Reopen it before checking or changing Cloudflare settings.")
	}
	return nil
}
