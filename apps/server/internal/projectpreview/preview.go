// Package projectpreview exposes registered guest development services on
// independent local browser origins, without AXV application or control routes.
package projectpreview

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

const maxActivePreviews = 64
const maxRegistrationReceipts = 4096
const retainedPreviewHistory = 64

type Target struct {
	Handler  http.Handler
	Validate func(context.Context) error
	Close    func()
}
type Resolver func(context.Context, compute.ProjectBinding, int) (Target, error)

type View struct {
	ID          string                 `json:"id"`
	ProjectID   string                 `json:"projectId"`
	Port        int                    `json:"port"`
	Binding     compute.ProjectBinding `json:"binding"`
	State       string                 `json:"state"`
	Error       string                 `json:"error,omitempty"`
	URL         string                 `json:"url,omitempty"`
	RemoteError string                 `json:"remoteError,omitempty"`
}

// Registration captures one request in this manager incarnation. A browser
// retains these exact fields until its original receipt has been recovered.
type Registration struct {
	InstanceID string                 `json:"instanceId"`
	RequestID  string                 `json:"requestId"`
	Binding    compute.ProjectBinding `json:"binding"`
	Port       int                    `json:"port"`
}

type registration struct {
	input Registration
	done  chan struct{}
	entry *entry
	err   *registrationError
}
type registrationError struct {
	status        int
	code, message string
}

type Manager struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	resolve    Resolver
	instanceID string
	entries    []*entry
	requests   map[string]*registration
	active     int
	closed     bool
	wg         sync.WaitGroup
}

type entry struct {
	owner     *Manager
	view      View
	token     string
	target    Target
	local     *surface
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func New(ctx context.Context, resolver Resolver) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: ctx, cancel: cancel, resolve: resolver, instanceID: rand.Text(), requests: map[string]*registration{}}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.check()
			}
		}
	}()
	return m
}

// Admission and retirement share a lock, so shutdown cannot begin waiting
// while another handler or validator is being added to this entry.
func (e *entry) acquire() bool {
	e.owner.mu.Lock()
	defer e.owner.mu.Unlock()
	if e.owner.closed || e.view.State != "ready" || e.ctx.Err() != nil {
		return false
	}
	e.wg.Add(1)
	return true
}

func (e *entry) shutdown() {
	e.closeOnce.Do(func() {
		e.cancel()
		e.local.close()
		e.wg.Wait()
		e.target.Close()
		e.target = Target{}
	})
}

func (e *entry) retire(state, reason string) {
	e.owner.mu.Lock()
	if e.view.State == "ready" || (state == "stopped" && e.view.State != "stopped") {
		if e.view.State == "ready" {
			e.owner.active--
		}
		e.view.State, e.view.Error = state, reason
		e.cancel()
	}
	e.owner.mu.Unlock()
	e.shutdown()
}

func (m *Manager) check() {
	m.mu.Lock()
	entries := append([]*entry(nil), m.entries...)
	m.mu.Unlock()
	var checks sync.WaitGroup
	for _, e := range entries {
		if !e.acquire() {
			continue
		}
		checks.Add(1)
		go func() {
			defer checks.Done()
			ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
			err := e.target.Validate(ctx)
			cancel()
			e.wg.Done()
			if err != nil && !errors.Is(err, context.Canceled) {
				e.retire("unavailable", "The original preview environment is unavailable. Register a new preview explicitly.")
			}
		}()
	}
	checks.Wait()
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	entries := append([]*entry(nil), m.entries...)
	m.mu.Unlock()
	for _, e := range entries {
		e.retire("stopped", "")
	}
	// Creation and validation belong to the manager, not the submitting browser.
	m.wg.Wait()
}
