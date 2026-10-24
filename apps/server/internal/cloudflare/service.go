package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

type Options struct {
	Listen, Origin, Issuer, Audience, OwnerSubject string
	Handler                                        http.Handler
	AllowRoute, LocalOnlyRoute                     func(*http.Request) bool
}

type Status struct {
	State  string `json:"state"`
	URL    string `json:"url,omitempty"`
	Listen string `json:"listen,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Service struct {
	cancel      context.CancelFunc
	server      *http.Server
	keys        *jwksTransport
	verifier    *tokenVerifier
	options     Options
	mu          sync.Mutex
	status      Status
	closed      bool
	requests    sync.WaitGroup
	connections map[net.Conn]http.ConnState
	done        chan struct{}
	closeOnce   sync.Once
	closeErr    error
}

type dependencies struct {
	client *http.Client
	now    func() time.Time
}

type connectionKey struct{}

// Start opens only the local HTTP ingress. A listening status is not evidence
// that cloudflared is running, that an Access policy exists, or that the public
// hostname is reachable. Tunnel installation and credentials remain external.
func Start(ctx context.Context, options Options) (*Service, error) {
	return start(ctx, options, dependencies{})
}

func start(parent context.Context, options Options, deps dependencies) (*Service, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, errors.New("Cloudflare ingress requires a lifetime context")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	listener, err := (&net.ListenConfig{}).Listen(parent, "tcp", options.Listen)
	if err != nil {
		return nil, fmt.Errorf("open Cloudflare loopback ingress: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	if deps.now == nil {
		deps.now = time.Now
	}
	client, keys := newJWKSClient(ctx, options.Issuer, deps.client)
	s := &Service{cancel: cancel, keys: keys, options: options,
		connections: make(map[net.Conn]http.ConnState), done: make(chan struct{}),
		status: Status{State: "listening", URL: options.Origin, Listen: listener.Addr().String()}}
	s.verifier = newTokenVerifier(ctx, options, client, deps.now)
	s.server = &http.Server{Handler: http.HandlerFunc(s.serveHTTP), ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return ctx },
		ConnContext: func(ctx context.Context, connection net.Conn) context.Context {
			return context.WithValue(ctx, connectionKey{}, connection)
		},
		ConnState: func(connection net.Conn, state http.ConnState) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if state == http.StateClosed {
				delete(s.connections, connection)
			} else {
				s.connections[connection] = state
			}
		}}
	go func() {
		err := s.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			s.status = Status{State: "error", Error: "The Cloudflare loopback ingress stopped unexpectedly."}
			s.mu.Unlock()
		}
		cancel()
	}()
	go func() { <-ctx.Done(); _ = s.Close() }()
	return s, nil
}

func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Close cancels and joins only this entrypoint's requests, including hijacked
// WebSockets. Shared managers, independent Codex runs and VMs are not stopped.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.cancel()
		s.mu.Unlock()
		err := s.server.Close()
		s.mu.Lock()
		connections := make([]net.Conn, 0, len(s.connections))
		for connection := range s.connections {
			connections = append(connections, connection)
		}
		s.mu.Unlock()
		for _, connection := range connections {
			_ = connection.Close()
		}
		s.keys.close()
		s.requests.Wait()
		s.mu.Lock()
		if s.status.State != "error" {
			s.status = Status{State: "stopped"}
		}
		if err != nil {
			s.closeErr = errors.New("the Cloudflare ingress could not close cleanly")
		}
		s.mu.Unlock()
		close(s.done)
	})
	<-s.done
	return s.closeErr
}

func (s *Service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w, finishResponse := PreventCaching(w)
	defer finishResponse()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "cloudflare_stopping", "Cloudflare access is stopping.")
		return
	}
	s.requests.Add(1)
	s.mu.Unlock()
	defer s.requests.Done()
	defer func() {
		if connection, ok := r.Context().Value(connectionKey{}).(net.Conn); ok {
			s.mu.Lock()
			if s.connections[connection] == http.StateHijacked {
				delete(s.connections, connection)
			}
			s.mu.Unlock()
		}
	}()
	// Treat every Upgrade header as requiring an Origin. WebSocket libraries
	// accept comma-separated and repeated Upgrade tokens, not just one exact
	// "websocket" header value.
	requireOrigin := r.Method != http.MethodGet && r.Method != http.MethodHead || len(r.Header.Values("Upgrade")) != 0
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !peer.Addr().IsLoopback() || r.TLS != nil || r.Host != s.options.Origin[len("https://"):] || !validOrigin(r, s.options.Origin, requireOrigin) {
		writeError(w, http.StatusForbidden, "cloudflare_forbidden", "This request is not authorized for Cloudflare access.")
		return
	}
	values := r.Header.Values("Cf-Access-Jwt-Assertion")
	if len(values) != 1 {
		writeError(w, http.StatusUnauthorized, "cloudflare_unauthorized", "Cloudflare Access authentication is required.")
		return
	}
	verifyContext, stopVerify := context.WithTimeout(r.Context(), identityTimeout)
	expires, err := s.verifier.verify(verifyContext, values[0])
	stopVerify()
	if err != nil {
		writeError(w, http.StatusUnauthorized, "cloudflare_unauthorized", "Cloudflare Access authentication is required.")
		return
	}
	r = withoutAccessCredentials(r)
	if s.options.LocalOnlyRoute != nil && s.options.LocalOnlyRoute(r) {
		writeError(w, http.StatusForbidden, "remote_local_only", "Perform this operation on the computer running AXV.")
		return
	}
	if !s.options.AllowRoute(r) {
		writeError(w, http.StatusNotFound, "not_found", "Unknown API endpoint.")
		return
	}
	// This is a finite signed lease, not provider revocation introspection. Its
	// deadline is at most fifteen minutes after original issuance; HTTP and
	// upgraded streams cannot renew it by reconnecting with the same token.
	requestContext, cancel := context.WithDeadline(r.Context(), expires)
	defer cancel()
	r = r.WithContext(requestContext)
	controller := http.NewResponseController(w)
	ioDone := make(chan struct{})
	stopIO := context.AfterFunc(requestContext, func() {
		defer close(ioDone)
		if connection, ok := requestContext.Value(connectionKey{}).(net.Conn); ok {
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
	authenticated, err := localaccess.WithAuthenticatedTunnel(r, s.options.Origin)
	if err != nil || !localaccess.AllowRequest(authenticated, requireOrigin) {
		writeError(w, http.StatusForbidden, "cloudflare_forbidden", "This request is not authorized for Cloudflare access.")
		return
	}
	s.options.Handler.ServeHTTP(w, authenticated)
}

// withoutAccessCredentials confines Cloudflare credentials to this authentication
// layer. Shared features may proxy ordinary application headers to compute or
// guest services, which must never receive the workstation's Access credentials.
// Clone first so authentication does not mutate the original inbound request.
func withoutAccessCredentials(r *http.Request) *http.Request {
	clean := r.Clone(r.Context())
	for name, values := range clean.Header {
		if strings.HasPrefix(strings.ToLower(name), "cf-access-") {
			delete(clean.Header, name)
			continue
		}
		if !strings.EqualFold(name, "Cookie") {
			continue
		}
		filtered := make([]string, 0, len(values))
		for _, value := range values {
			var kept []string
			for _, pair := range strings.Split(value, ";") {
				pair = strings.TrimSpace(pair)
				cookieName, _, _ := strings.Cut(pair, "=")
				switch strings.ToLower(strings.TrimSpace(cookieName)) {
				case "cf_authorization", "cf_binding", "cf_session", "cf_appsession", "cf_device":
					continue
				}
				if pair != "" {
					kept = append(kept, pair)
				}
			}
			if len(kept) != 0 {
				filtered = append(filtered, strings.Join(kept, "; "))
			}
		}
		if len(filtered) == 0 {
			delete(clean.Header, name)
		} else {
			clean.Header[name] = filtered
		}
	}
	return clean
}

func validOrigin(r *http.Request, origin string, required bool) bool {
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return !required
	}
	return len(values) == 1 && values[0] == origin
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message, "code": code})
}
