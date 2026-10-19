// Package remoteaccess authenticates explicit private remote HTTP entrypoints.
// It does not configure the host network or own desktop and compute processes.
package remoteaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

// Identity is resolved from the actual connection by the private network client.
// UserID is a canonical positive decimal ID; NodeID is a stable node identifier.
type Identity struct {
	UserID string
	NodeID string
	Tagged bool
}

type WhoIsFunc func(context.Context, string) (Identity, error)

type MiddlewareOptions struct {
	Origin          string
	OwnerUserID     string
	WhoIs           WhoIsFunc
	AllowRoute      func(*http.Request) bool
	LocalOnlyRoute  func(*http.Request) bool
	IdentityTimeout time.Duration
	IdentityRefresh time.Duration
}

type Middleware struct {
	origin          string
	host            string
	ownerUserID     string
	whoIs           WhoIsFunc
	allowRoute      func(*http.Request) bool
	localOnlyRoute  func(*http.Request) bool
	identityTimeout time.Duration
	identityRefresh time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	closed          bool
	wg              sync.WaitGroup
}

// NewMiddleware requires an explicit route policy and a WhoIs resolver from the
// same private network server as the HTTPS listener. Connectivity and forwarded
// identity headers alone never authorize access.
func NewMiddleware(options MiddlewareOptions) (*Middleware, error) {
	u, err := url.Parse(options.Origin)
	if err != nil || u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.RawFragment != "" || u.String() != options.Origin || strings.HasSuffix(u.Host, ":") {
		return nil, fmt.Errorf("remote access requires an exact HTTPS origin")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("remote access requires a valid HTTPS port")
		}
	}
	if !canonicalUserID(options.OwnerUserID) || options.WhoIs == nil || options.AllowRoute == nil {
		return nil, fmt.Errorf("remote access requires an explicit owner user ID, identity resolver and route policy")
	}
	if options.IdentityTimeout < 0 {
		return nil, fmt.Errorf("remote identity timeout must be positive")
	}
	if options.IdentityTimeout == 0 {
		options.IdentityTimeout = 5 * time.Second
	}
	if options.IdentityRefresh < 0 {
		return nil, fmt.Errorf("remote identity refresh must be positive")
	}
	if options.IdentityRefresh == 0 {
		options.IdentityRefresh = 5 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Middleware{origin: options.Origin, host: u.Host, ownerUserID: options.OwnerUserID, whoIs: options.WhoIs,
		allowRoute: options.AllowRoute, localOnlyRoute: options.LocalOnlyRoute, identityTimeout: options.IdentityTimeout, identityRefresh: options.IdentityRefresh, ctx: ctx, cancel: cancel}, nil
}

// Close prevents new requests, cancels this entrypoint's HTTP and WebSocket
// contexts and waits for their handlers to finish. Shared local services and
// independent coding-agent runs retain their own lifetimes.
func (m *Middleware) Close() {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Middleware) acquire() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	m.wg.Add(1)
	return true
}

func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.acquire() {
			writeError(w, http.StatusServiceUnavailable, "remote_stopping", "Remote access is stopping.")
			return
		}
		requestContext, cancel := context.WithCancel(r.Context())
		stopEntry := context.AfterFunc(m.ctx, cancel)
		r = r.WithContext(requestContext)
		controller := http.NewResponseController(w)
		ioDone := make(chan struct{})
		stopIO := context.AfterFunc(requestContext, func() {
			defer close(ioDone)
			// Context cancellation does not unblock a partially received JSON
			// body. Deadlines also interrupt I/O on upgraded connections where
			// supported; feature handlers retain their own session cleanup.
			_ = controller.SetReadDeadline(time.Now())
			_ = controller.SetWriteDeadline(time.Now())
			if r.Body != nil {
				_ = r.Body.Close()
			}
		})
		defer func() {
			stopEntry()
			if !stopIO() {
				<-ioDone
			}
			cancel()
			m.wg.Done()
		}()
		requireOrigin := r.Method != http.MethodGet && r.Method != http.MethodHead || strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		if !m.requestOrigin(r, requireOrigin) {
			writeError(w, http.StatusForbidden, "remote_forbidden", "This request is not authorized for remote access.")
			return
		}
		if _, err := netip.ParseAddrPort(r.RemoteAddr); err != nil {
			writeError(w, http.StatusForbidden, "remote_forbidden", "This request is not authorized for remote access.")
			return
		}
		identityContext, stopIdentity := context.WithTimeout(requestContext, m.identityTimeout)
		identity, err := m.whoIs(identityContext, r.RemoteAddr)
		identityContextError := identityContext.Err()
		stopIdentity()
		if requestContext.Err() != nil {
			return
		}
		if identityContextError != nil {
			writeError(w, http.StatusServiceUnavailable, "remote_identity_unavailable", "The connection identity could not be verified. Try again.")
			return
		}
		if err != nil || !m.authorized(identity) {
			writeError(w, http.StatusForbidden, "remote_forbidden", "This request is not authorized for remote access.")
			return
		}
		if m.localOnlyRoute != nil && m.localOnlyRoute(r) {
			writeError(w, http.StatusForbidden, "remote_local_only", "Perform this operation on the computer running AXV.")
			return
		}
		if !m.allowRoute(r) {
			writeError(w, http.StatusNotFound, "not_found", "Unknown API endpoint.")
			return
		}
		authenticated, err := localaccess.WithAuthenticatedRemote(r, m.origin)
		if err != nil || !localaccess.AllowRequest(authenticated, requireOrigin) {
			writeError(w, http.StatusForbidden, "remote_forbidden", "This request is not authorized for remote access.")
			return
		}
		// An upgraded connection can survive a network-policy change. Keep its
		// original peer authorized for its entire lifetime, rather than allowing
		// a removed, retagged or replaced node to retain a terminal indefinitely.
		watchContext, stopWatch := context.WithCancel(requestContext)
		identityDone := make(chan struct{})
		go func() {
			defer close(identityDone)
			m.watchIdentity(watchContext, cancel, r.RemoteAddr, identity)
		}()
		defer func() { stopWatch(); <-identityDone }()
		next.ServeHTTP(w, authenticated)
	})
}

func (m *Middleware) authorized(identity Identity) bool {
	return !identity.Tagged && canonicalUserID(identity.UserID) && identity.UserID == m.ownerUserID && stableNodeID(identity.NodeID)
}

func (m *Middleware) watchIdentity(ctx context.Context, cancel context.CancelFunc, peer string, original Identity) {
	ticker := time.NewTicker(m.identityRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lookup, done := context.WithTimeout(ctx, m.identityTimeout)
			current, err := m.whoIs(lookup, peer)
			lookupErr := lookup.Err()
			done()
			if ctx.Err() != nil {
				return
			}
			if err != nil || lookupErr != nil || !m.authorized(current) || current.NodeID != original.NodeID {
				cancel()
				return
			}
		}
	}
}

func (m *Middleware) requestOrigin(r *http.Request, requireOrigin bool) bool {
	if r.TLS == nil || r.Host != m.host {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return !requireOrigin
	}
	return len(origins) == 1 && origins[0] == m.origin
}

func canonicalUserID(value string) bool {
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

func stableNodeID(value string) bool {
	return value != "" && value != "0" && len(value) <= 256 && !strings.ContainsAny(value, " \t\r\n\x00")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message, "code": code})
}
