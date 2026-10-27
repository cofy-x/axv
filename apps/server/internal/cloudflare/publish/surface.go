package publish

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/access"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/internal/origin"
)

type connectionKey struct{}
type surface struct {
	server      *http.Server
	connections map[net.Conn]http.ConnState
	mu          sync.Mutex
	listen      string
}

func (m *Manager) ensureSurface(address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return net.ErrClosed
	}
	if m.surface != nil {
		if m.surface.listen != address {
			for _, item := range m.state.Records {
				if item.Publication.State != "removed" && item.Resources.Listen != address {
					return problem(409, "publication_origin_changed", "The publishing origin changed while resources still exist.")
				}
			}
			if err := m.surface.close(); err != nil {
				return err
			}
			m.surface = nil
		} else {
			return nil
		}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	s := &surface{connections: map[net.Conn]http.ConnState{}, listen: address}
	s.server = &http.Server{Handler: http.HandlerFunc(m.serveHTTP), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return m.ctx }, ConnContext: func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, connectionKey{}, c)
	}, ConnState: func(c net.Conn, state http.ConnState) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if state == http.StateClosed {
			delete(s.connections, c)
		} else {
			s.connections[c] = state
		}
	}}
	m.surface = s
	go func() { _ = s.server.Serve(listener) }()
	return nil
}

func (s *surface) close() error {
	result := s.server.Close()
	s.mu.Lock()
	connections := make([]net.Conn, 0, len(s.connections))
	for c := range s.connections {
		connections = append(connections, c)
	}
	s.mu.Unlock()
	for _, c := range connections {
		if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (m *Manager) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w, finishResponse := origin.PreventCaching(w)
	defer finishResponse()
	securityHeaders(w.Header())
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !peer.Addr().IsLoopback() || r.TLS != nil {
		controlError(w, 403, "publication_forbidden", "Use the configured Cloudflare publishing origin.")
		return
	}
	m.mu.Lock()
	entry := m.live[r.Host]
	if entry == nil || entry.closed || entry.ctx.Err() != nil || m.closed {
		m.mu.Unlock()
		controlError(w, 404, "publication_not_found", "No active published application belongs to this hostname.")
		return
	}
	requireOrigin := r.Method != http.MethodGet && r.Method != http.MethodHead || len(r.Header.Values("Upgrade")) > 0
	origins := r.Header.Values("Origin")
	origin := "https://" + entry.hostname
	if len(origins) > 1 || len(origins) == 1 && origins[0] != origin || requireOrigin && len(origins) != 1 {
		m.mu.Unlock()
		controlError(w, 403, "publication_forbidden", "Use this application's exact HTTPS origin.")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); (site == "cross-site" || site == "same-site") && !(r.Method == http.MethodGet && r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document") {
		m.mu.Unlock()
		controlError(w, 403, "publication_forbidden", "Load application resources from their own origin.")
		return
	}
	entry.wg.Add(1)
	surface := m.surface
	connection, _ := r.Context().Value(connectionKey{}).(net.Conn)
	if connection != nil {
		entry.connections[connection]++
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if connection != nil {
			entry.connections[connection]--
			if entry.connections[connection] == 0 {
				delete(entry.connections, connection)
			}
		}
		m.mu.Unlock()
		if connection != nil && surface != nil {
			surface.mu.Lock()
			if surface.connections[connection] == http.StateHijacked {
				delete(surface.connections, connection)
			}
			surface.mu.Unlock()
		}
		entry.wg.Done()
	}()
	ctx, cancel := context.WithCancel(r.Context())
	stopEntry := context.AfterFunc(entry.ctx, cancel)
	defer func() { stopEntry(); cancel() }()
	if entry.visibility == "owner" {
		values := r.Header.Values("Cf-Access-Jwt-Assertion")
		if len(values) != 1 {
			controlError(w, 401, "publication_unauthorized", "Cloudflare Access authentication for the configured owner is required.")
			return
		}
		verifyContext, stop := context.WithTimeout(ctx, 5*time.Second)
		var expires time.Time
		if m.options.Authorize != nil {
			expires, err = m.options.Authorize(verifyContext, entry.settings, r)
		} else {
			expires, err = entry.verifier.Verify(verifyContext, values[0])
		}
		stop()
		if err != nil || !expires.After(time.Now()) {
			controlError(w, 401, "publication_unauthorized", "Cloudflare Access authentication for the configured owner is required.")
			return
		}
		var expireCancel context.CancelFunc
		ctx, expireCancel = context.WithDeadline(ctx, expires)
		defer expireCancel()
	}
	r = access.StripCredentials(r).WithContext(ctx)
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-axv-") {
			r.Header.Del(name)
		}
	}
	ioDone := make(chan struct{})
	controller := http.NewResponseController(w)
	stopIO := context.AfterFunc(ctx, func() {
		defer close(ioDone)
		if connection != nil {
			_ = connection.Close()
		}
		_ = controller.SetReadDeadline(time.Now())
		_ = controller.SetWriteDeadline(time.Now())
		if r.Body != nil {
			_ = r.Body.Close()
		}
	})
	defer func() {
		if !stopIO() {
			<-ioDone
		}
	}()
	if err := entry.target.Validate(ctx); err != nil {
		controlError(w, 409, "publication_binding_changed", "The original published VM project environment is no longer available.")
		return
	}
	if ctx.Err() != nil {
		return
	}
	if !m.browserIsolation(w, r, entry, time.Now()) {
		return
	}
	r = withoutIsolationCookies(r)
	entry.target.Handler.ServeHTTP(w, r)
}

func securityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "sandbox allow-scripts allow-same-origin allow-forms allow-downloads; frame-ancestors 'none'")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Origin-Agent-Cluster", "?1")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}
