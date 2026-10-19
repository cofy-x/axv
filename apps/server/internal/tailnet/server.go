// Package tailnet owns a profile's persistent embedded Tailscale HTTPS listener.
// It does not configure or control the host's Tailscale daemon or networking.
package tailnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/remoteaccess"
	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

type State string

const (
	StateDisabled   State = "disabled"
	StateStarting   State = "starting"
	StateNeedsLogin State = "needs-login"
	StateNeedsHTTPS State = "needs-https"
	StateReady      State = "ready"
	StateError      State = "error"
	StateStopped    State = "stopped"
)

// Status is for the local control surface. AuthURL is an enrollment capability;
// callers must not include it in ordinary persisted logs or remote responses.
type Status struct {
	State       State  `json:"state"`
	AuthURL     string `json:"authUrl,omitempty"`
	URL         string `json:"url,omitempty"`
	Error       string `json:"error,omitempty"`
	OwnerUserID string `json:"ownerUserId,omitempty"`
}

// Endpoint contains the verified server identity and this node's real peer lookup.
type Endpoint struct {
	Origin      string
	OwnerUserID string
	WhoIs       remoteaccess.WhoIsFunc
}

type Options struct {
	StateDir    string
	Hostname    string
	OwnerUserID string
	// Handler constructs a remote HTTP surface after enrollment and certificate
	// readiness. Its cleanup cancels only this surface's upgraded connections.
	Handler func(Endpoint) (http.Handler, func(), error)
	// Update must return promptly. It can call Status, but must not call Close.
	Update func(Status)
}

type Server struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	status   Status
	closeErr error
	update   func(Status)
	notifyMu sync.Mutex
	notified Status
}

type backend interface {
	Up(context.Context) (*ipnstate.Status, error)
	Status(context.Context) (*ipnstate.Status, error)
	Certificate(context.Context, string, time.Duration) (*tls.Certificate, error)
	Listen(string, string) (net.Listener, error)
	WhoIs(context.Context, string) (remoteaccess.Identity, error)
	Close() error
}

type timing struct {
	poll        time.Duration
	refresh     time.Duration
	identity    time.Duration
	certificate time.Duration
	shutdown    time.Duration
}

var defaultTiming = timing{poll: 5 * time.Second, refresh: 15 * time.Minute, identity: 5 * time.Second, certificate: 90 * time.Second, shutdown: 10 * time.Second}
var loginLink = regexp.MustCompile(`https://login\.tailscale\.com/a/[A-Za-z0-9]+`)

// Start validates owned state and returns without waiting for login or the
// network. Connection, settings and certificate failures remain visible in Status.
func Start(ctx context.Context, options Options) (*Server, error) {
	return start(ctx, options, newBackend, defaultTiming)
}

func start(ctx context.Context, options Options, factory func(Options, func(string, ...any)) backend, limits timing) (*Server, error) {
	if ctx == nil || options.Handler == nil {
		return nil, errors.New("tailnet requires a lifecycle context and remote handler")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, lock, err := prepareState(options)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Server{ctx: ctx, cancel: cancel, done: make(chan struct{}), status: Status{State: StateStarting}, update: options.Update}
	go func() {
		defer close(s.done)
		defer cancel()
		err := s.run(options, store, factory, limits)
		if lockErr := lock.Close(); lockErr != nil {
			err = errors.Join(err, errors.New("release the embedded Tailscale state lock"))
		}
		s.mu.Lock()
		s.closeErr = err
		s.mu.Unlock()
		if ctx.Err() != nil {
			s.publish(Status{State: StateStopped})
		}
	}()
	return s, nil
}

func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Close stops this listener and its remote attachments, preserving enrollment.
// It waits for backend cleanup and reports cleanup errors, never logging out.
func (s *Server) Close() error {
	s.cancel()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}

func (s *Server) publish(status Status) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
	s.notify()
}

func (s *Server) notify() {
	if s.update == nil {
		return
	}
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	current := s.Status()
	if current != s.notified {
		s.notified = current
		s.update(current)
	}
}

func (s *Server) login(format string, args ...any) {
	link := loginLink.FindString(fmt.Sprintf(format, args...))
	if link == "" {
		return
	}
	s.mu.Lock()
	if s.ctx.Err() != nil || s.status.State != StateStarting && s.status.State != StateNeedsLogin {
		s.mu.Unlock()
		return
	}
	s.status = Status{State: StateNeedsLogin, AuthURL: link}
	s.mu.Unlock()
	s.notify()
}

func (s *Server) fail(message string) {
	s.publish(Status{State: StateError, Error: message})
}

func (s *Server) run(options Options, store *stateStore, factory func(Options, func(string, ...any)) backend, limits timing) (cleanupErr error) {
	node := factory(options, s.login)
	defer func() {
		if err := node.Close(); err != nil {
			cleanupErr = errors.Join(cleanupErr, errors.New("close the embedded Tailscale node"))
		}
	}()
	status, err := node.Up(s.ctx)
	if err != nil {
		if s.ctx.Err() == nil {
			s.fail("The embedded Tailscale node could not connect. Restart remote access to retry.")
		}
		return nil
	}
	if err := store.bind(status, options.OwnerUserID); err != nil {
		s.fail(err.Error())
		return nil
	}
	domain, err := s.waitHTTPS(node, status, store.identity, limits)
	if err != nil {
		if s.ctx.Err() == nil {
			s.fail(err.Error())
		}
		return nil
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	var initialCertificate *tls.Certificate
	for {
		certificateCtx, done := context.WithTimeout(ctx, limits.certificate)
		initialCertificate, err = certificate(node, certificateCtx, domain, time.Nanosecond)
		done()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil
		}
		s.publish(Status{State: StateNeedsHTTPS, OwnerUserID: store.identity.UserID, Error: "Waiting for a valid tailnet HTTPS certificate. Check https://login.tailscale.com/admin/dns."})
		if err = pause(ctx, limits.poll); err != nil {
			return nil
		}
		current, statusErr := statusWithin(ctx, node, limits.identity)
		if statusErr != nil || verifyIdentity(current, store.identity) != nil || certificateDomain(current) != domain {
			s.fail("The embedded node connection, identity or HTTPS domain changed while obtaining its certificate.")
			return nil
		}
	}
	current, statusErr := statusWithin(ctx, node, limits.identity)
	if ctx.Err() != nil {
		return nil
	}
	if statusErr != nil || verifyIdentity(current, store.identity) != nil || certificateDomain(current) != domain {
		s.fail("The embedded node connection or identity changed before HTTPS became available.")
		return nil
	}
	origin := "https://" + domain
	certificates := &certificateCache{value: initialCertificate}
	handler, closeConnections, err := options.Handler(Endpoint{Origin: origin, OwnerUserID: store.identity.UserID, WhoIs: node.WhoIs})
	if err != nil || handler == nil || closeConnections == nil {
		if closeConnections != nil {
			closeConnections()
		}
		s.fail("The remote HTTP surface could not be initialized.")
		return nil
	}
	closeConnections = sync.OnceFunc(closeConnections)
	defer closeConnections()
	if ctx.Err() != nil {
		return nil
	}
	listener, err := node.Listen("tcp", ":443")
	if err != nil {
		s.fail("The embedded Tailscale HTTPS listener could not open.")
		return nil
	}
	if ctx.Err() != nil {
		return listener.Close()
	}
	listener = tls.NewListener(listener, &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello == nil || hello.ServerName != domain {
				return nil, errors.New("the TLS server name does not match this workstation")
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return certificates.current(domain, time.Now())
		},
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	s.publish(Status{State: StateReady, URL: origin, OwnerUserID: store.identity.UserID})
	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		refreshCertificates(ctx, node, domain, certificates, limits, s.renewalStatus)
	}()
	defer func() { cancel(); <-refreshDone }()
	monitor := time.NewTicker(limits.poll)
	defer monitor.Stop()
	var serveErr error
loop:
	for {
		select {
		case serveErr = <-served:
			served = nil
			if s.ctx.Err() == nil {
				s.fail("The remote HTTPS listener stopped unexpectedly.")
			}
			break loop
		case <-ctx.Done():
			break loop
		case <-monitor.C:
			current, statusErr := statusWithin(ctx, node, limits.identity)
			if ctx.Err() != nil {
				break loop
			}
			if statusErr != nil || verifyIdentity(current, store.identity) != nil || certificateDomain(current) != domain {
				s.fail("The embedded node connection or identity changed. Remote access has stopped.")
				break loop
			}
			if _, err := certificates.current(domain, time.Now()); err != nil {
				s.fail("The HTTPS certificate expired before it could be renewed. Remote access has stopped.")
				break loop
			}
		}
	}
	// Stop request contexts and upgraded streams before HTTP's graceful shutdown.
	cancel()
	closeConnections()
	shutdownCtx, stop := context.WithTimeout(context.Background(), limits.shutdown)
	err = server.Shutdown(shutdownCtx)
	stop()
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, errors.New("shut down the remote HTTP listener"), server.Close())
	}
	if served != nil {
		serveErr = <-served
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		cleanupErr = errors.Join(cleanupErr, errors.New("close the remote HTTP listener"))
	}
	return cleanupErr
}

func (s *Server) waitHTTPS(node backend, status *ipnstate.Status, identity binding, limits timing) (string, error) {
	for {
		if err := verifyIdentity(status, identity); err != nil {
			return "", err
		}
		if domain := certificateDomain(status); domain != "" {
			return domain, nil
		}
		s.publish(Status{State: StateNeedsHTTPS, OwnerUserID: identity.UserID, Error: "Enable MagicDNS and HTTPS certificates at https://login.tailscale.com/admin/dns."})
		if err := pause(s.ctx, limits.poll); err != nil {
			return "", err
		}
		var err error
		status, err = statusWithin(s.ctx, node, limits.identity)
		if err != nil {
			return "", errors.New("The embedded node could not refresh its HTTPS settings.")
		}
	}
}

func statusWithin(ctx context.Context, node backend, limit time.Duration) (*ipnstate.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	return node.Status(ctx)
}

func certificate(node backend, ctx context.Context, domain string, minValidity time.Duration) (*tls.Certificate, error) {
	value, err := node.Certificate(ctx, domain, minValidity)
	if err != nil || value == nil || value.Leaf == nil || value.Leaf.VerifyHostname(domain) != nil || time.Now().Before(value.Leaf.NotBefore) || !time.Now().Before(value.Leaf.NotAfter) {
		return nil, errors.New("a valid tailnet HTTPS certificate is unavailable")
	}
	return value, nil
}

func verifyIdentity(status *ipnstate.Status, identity binding) error {
	if status == nil || status.BackendState != "Running" || status.Self == nil || status.Self.IsTagged() || status.Self.ID == "" || status.Self.UserID <= 0 {
		return errors.New("The embedded node requires a connected, untagged owner identity.")
	}
	if strconv.FormatInt(int64(status.Self.UserID), 10) != identity.UserID || string(status.Self.ID) != identity.NodeID {
		return errors.New("The embedded node owner or identity differs from this profile's recorded enrollment. Restore that identity or explicitly reset remote state.")
	}
	return nil
}

func certificateDomain(status *ipnstate.Status) string {
	if status == nil || status.Self == nil || status.CurrentTailnet == nil || !status.CurrentTailnet.MagicDNSEnabled {
		return ""
	}
	domain := strings.TrimSuffix(status.Self.DNSName, ".")
	if !strings.HasSuffix(domain, ".ts.net") || !slices.Contains(status.CertDomains, domain) {
		return ""
	}
	return domain
}

func pause(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type embeddedBackend struct{ node *tsnet.Server }

func newBackend(options Options, logf func(string, ...any)) backend {
	return &embeddedBackend{node: &tsnet.Server{Dir: options.StateDir, Hostname: options.Hostname, ControlURL: "https://controlplane.tailscale.com", UserLogf: logf}}
}

func (n *embeddedBackend) Up(ctx context.Context) (*ipnstate.Status, error) { return n.node.Up(ctx) }
func (n *embeddedBackend) Close() error                                     { return n.node.Close() }
func (n *embeddedBackend) Listen(network, address string) (net.Listener, error) {
	return n.node.Listen(network, address)
}

func (n *embeddedBackend) client() (*local.Client, error) { return n.node.LocalClient() }

func (n *embeddedBackend) Status(ctx context.Context) (*ipnstate.Status, error) {
	client, err := n.client()
	if err != nil {
		return nil, err
	}
	return client.Status(ctx)
}

func (n *embeddedBackend) Certificate(ctx context.Context, domain string, validity time.Duration) (*tls.Certificate, error) {
	client, err := n.client()
	if err != nil {
		return nil, err
	}
	certPEM, keyPEM, err := client.CertPairWithValidity(ctx, domain, validity)
	if err != nil {
		return nil, err
	}
	value, err := tls.X509KeyPair(certPEM, keyPEM)
	if err == nil && value.Leaf == nil && len(value.Certificate) != 0 {
		value.Leaf, err = x509.ParseCertificate(value.Certificate[0])
	}
	return &value, err
}

func (n *embeddedBackend) WhoIs(ctx context.Context, remote string) (remoteaccess.Identity, error) {
	client, err := n.client()
	if err != nil {
		return remoteaccess.Identity{}, errors.New("the embedded peer identity is unavailable")
	}
	who, err := client.WhoIs(ctx, remote)
	if err != nil || who == nil || who.Node == nil || who.UserProfile == nil {
		return remoteaccess.Identity{}, errors.New("the embedded peer identity could not be verified")
	}
	return peerIdentity(who), nil
}

func peerIdentity(who *apitype.WhoIsResponse) remoteaccess.Identity {
	if who == nil || who.Node == nil || who.UserProfile == nil {
		return remoteaccess.Identity{}
	}
	node := who.Node
	// WhoIs resolves a netmap entry, including entries retained for expired or
	// restricted peers. These entries cannot authorize either a new request or
	// the next check of an existing stream. Expired includes Tailscale's control
	// clock adjustment; comparing KeyExpiry to our local clock would lose it.
	if node.Expired || !node.MachineAuthorized || node.IsJailed || node.UnsignedPeerAPIOnly ||
		node.User <= 0 || node.User != who.UserProfile.ID || !presentNodeID(string(node.StableID)) {
		return remoteaccess.Identity{}
	}
	return remoteaccess.Identity{UserID: strconv.FormatInt(int64(node.User), 10), NodeID: string(node.StableID), Tagged: node.IsTagged()}
}
