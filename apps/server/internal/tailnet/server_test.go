package tailnet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/remoteaccess"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/views"
)

const testDomain = "axv.example.ts.net"

var testTiming = timing{poll: 5 * time.Millisecond, refresh: 5 * time.Millisecond, identity: time.Second, certificate: time.Second, shutdown: time.Second}

type fakeBackend struct {
	mu          sync.Mutex
	status      *ipnstate.Status
	statusErr   error
	closeErr    error
	up          func(context.Context) (*ipnstate.Status, error)
	cert        func(context.Context, string, time.Duration) (*tls.Certificate, error)
	listener    net.Listener
	upCalls     atomic.Int32
	closeCalls  atomic.Int32
	certCalls   atomic.Int32
	listenCalls atomic.Int32
}

func (n *fakeBackend) Up(ctx context.Context) (*ipnstate.Status, error) {
	n.upCalls.Add(1)
	if n.up != nil {
		return n.up(ctx)
	}
	return n.Status(ctx)
}

func (n *fakeBackend) Status(context.Context) (*ipnstate.Status, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.status == nil {
		return nil, n.statusErr
	}
	status := *n.status
	if status.Self != nil {
		self := *status.Self
		status.Self = &self
	}
	if status.CurrentTailnet != nil {
		tailnet := *status.CurrentTailnet
		status.CurrentTailnet = &tailnet
	}
	status.CertDomains = append([]string(nil), status.CertDomains...)
	return &status, n.statusErr
}

func (n *fakeBackend) Certificate(ctx context.Context, domain string, validity time.Duration) (*tls.Certificate, error) {
	n.certCalls.Add(1)
	return n.cert(ctx, domain, validity)
}

func (n *fakeBackend) Listen(string, string) (net.Listener, error) {
	n.listenCalls.Add(1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	n.mu.Lock()
	n.listener = listener
	n.mu.Unlock()
	return listener, err
}

func (n *fakeBackend) WhoIs(context.Context, string) (remoteaccess.Identity, error) {
	return remoteaccess.Identity{UserID: "123", NodeID: "peer-client"}, nil
}

func (n *fakeBackend) Close() error { n.closeCalls.Add(1); return n.closeErr }

func clearCredentials(t *testing.T) {
	t.Helper()
	for _, key := range []string{"TS_AUTHKEY", "TS_AUTH_KEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID", "TS_ID_TOKEN", "TS_AUDIENCE", "TSNET_FORCE_LOGIN"} {
		t.Setenv(key, "")
	}
}

func validStatus() *ipnstate.Status {
	return &ipnstate.Status{BackendState: "Running", Self: &ipnstate.PeerStatus{ID: "node-server", UserID: 123, DNSName: testDomain + "."}, CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSEnabled: true}, CertDomains: []string{testDomain}}
}

func certificateFixture(t *testing.T, serial int64, validity ...time.Duration) *tls.Certificate {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	validFor := 48 * time.Hour
	if len(validity) != 0 {
		validFor = validity[0]
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: []string{testDomain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(validFor), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func testOptions(t *testing.T) Options {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("embedded tailnet state requires macOS or Linux")
	}
	clearCredentials(t)
	return Options{StateDir: filepath.Join(t.TempDir(), "tailscale"), Hostname: "axv", Handler: func(Endpoint) (http.Handler, func(), error) {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "AXV") }), func() {}, nil
	}}
}

func startFake(t *testing.T, options Options, node *fakeBackend) *Server {
	t.Helper()
	server, err := start(context.Background(), options, func(Options, func(string, ...any)) backend { return node }, testTiming)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close tailnet: %v", err)
		}
	})
	return server
}

func awaitState(t *testing.T, server *Server, expected State) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status := server.Status()
		if status.State == expected {
			return status
		}
		if status.State == StateError && expected != StateError {
			t.Fatalf("unexpected remote error: %+v", status)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("remote state never became %s: %+v", expected, server.Status())
	return Status{}
}

func TestListenerFailureRetainsDiagnosticAndReportsOnlyCleanupFailure(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		name := "cleaned-up"
		if cleanupFails {
			name = "cleanup-failed"
		}
		t.Run(name, func(t *testing.T) {
			options := testOptions(t)
			certificate := certificateFixture(t, 1)
			node := &fakeBackend{status: validStatus(), cert: func(context.Context, string, time.Duration) (*tls.Certificate, error) {
				return certificate, nil
			}}
			if cleanupFails {
				node.closeErr = errors.New("fixture backend cleanup failure")
			}
			server, err := start(context.Background(), options, func(Options, func(string, ...any)) backend { return node }, testTiming)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close() })
			awaitState(t, server, StateReady)
			node.mu.Lock()
			listener := node.listener
			node.mu.Unlock()
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			awaitState(t, server, StateError)
			<-server.done
			if err := server.Close(); (err != nil) != cleanupFails {
				t.Fatalf("Close confused a listener fault with cleanup failure: %v", err)
			}
			if status := server.Status(); status.State != StateError || status.Error == "" || status.URL != "" || node.closeCalls.Load() != 1 {
				t.Fatalf("listener failure lost its diagnostic or cleanup: %+v, closes %d", status, node.closeCalls.Load())
			}
		})
	}
}

func clientFor(t *testing.T, node *fakeBackend, certificates ...*tls.Certificate) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	for _, certificate := range certificates {
		roots.AddCert(certificate.Leaf)
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		node.mu.Lock()
		address := node.listener.Addr().String()
		node.mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}
}

func TestStartReturnsWhileEnrollmentWaitsAndCloseCancelsIt(t *testing.T) {
	options := testOptions(t)
	node := &fakeBackend{up: func(ctx context.Context) (*ipnstate.Status, error) { <-ctx.Done(); return nil, ctx.Err() }}
	server, err := start(context.Background(), options, func(_ Options, logf func(string, ...any)) backend {
		logf("To authenticate: %s account@example.com", "https://login.tailscale.com/a/example")
		return node
	}, testTiming)
	if err != nil {
		t.Fatal(err)
	}
	status := awaitState(t, server, StateNeedsLogin)
	if status.AuthURL != "https://login.tailscale.com/a/example" || status.URL != "" {
		t.Fatalf("incorrect pending login: %+v", status)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if node.closeCalls.Load() != 1 || server.Status().State != StateStopped || node.listenCalls.Load() != 0 {
		t.Fatal("pending enrollment was not cleanly stopped")
	}
	data, err := os.ReadFile(filepath.Join(options.StateDir, bindingName))
	if err != nil || strings.Contains(string(data), "login.tailscale.com") {
		t.Fatal("enrollment capability was persisted in the identity binding")
	}
}

func TestLoginNotificationsCannotReplaceConcurrentReadyStatus(t *testing.T) {
	for range 1000 {
		server := &Server{ctx: context.Background(), status: Status{State: StateStarting}}
		ready := Status{State: StateReady, URL: "https://" + testDomain, OwnerUserID: "123"}
		begin := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() { defer workers.Done(); <-begin; server.login("https://login.tailscale.com/a/example") }()
		go func() { defer workers.Done(); <-begin; server.publish(ready) }()
		close(begin)
		workers.Wait()
		if current := server.Status(); current != ready {
			t.Fatalf("a stale login notification replaced readiness: %+v", current)
		}
	}
}

func TestHTTPSSettingsWaitKeepsOnePersistentNodeUntilReady(t *testing.T) {
	options := testOptions(t)
	certificate := certificateFixture(t, 1)
	status := validStatus()
	status.CurrentTailnet.MagicDNSEnabled = false
	status.CertDomains = nil
	node := &fakeBackend{status: status, cert: func(context.Context, string, time.Duration) (*tls.Certificate, error) { return certificate, nil }}
	var cleanup atomic.Int32
	options.Handler = func(endpoint Endpoint) (http.Handler, func(), error) {
		if endpoint.Origin != "https://"+testDomain || endpoint.OwnerUserID != "123" || endpoint.WhoIs == nil {
			t.Errorf("unverified endpoint: %+v", endpoint)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "AXV") }), func() { cleanup.Add(1) }, nil
	}
	server := startFake(t, options, node)
	awaitState(t, server, StateNeedsHTTPS)
	if node.closeCalls.Load() != 0 || node.listenCalls.Load() != 0 {
		t.Fatal("settings wait closed the node or exposed HTTP prematurely")
	}
	node.mu.Lock()
	node.status = validStatus()
	node.mu.Unlock()
	awaitState(t, server, StateReady)
	response, err := clientFor(t, node, certificate).Get("https://" + testDomain + "/")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != "AXV" || len(response.TLS.VerifiedChains) == 0 {
		t.Fatalf("private HTTPS failed: %s %v", data, err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if node.upCalls.Load() != 1 || node.closeCalls.Load() != 1 || cleanup.Load() != 1 {
		t.Fatalf("lifecycle repeated: up=%d close=%d cleanup=%d", node.upCalls.Load(), node.closeCalls.Load(), cleanup.Load())
	}
}

func TestBackgroundCertificateRefreshPublishesRenewedCertificate(t *testing.T) {
	options := testOptions(t)
	first, renewed := certificateFixture(t, 1, time.Hour), certificateFixture(t, 2)
	var calls atomic.Int32
	node := &fakeBackend{status: validStatus(), cert: func(_ context.Context, domain string, validity time.Duration) (*tls.Certificate, error) {
		if domain != testDomain || validity == 0 {
			t.Errorf("certificate lookup allowed an uncontrolled provider renewal")
		}
		if calls.Add(1) == 1 {
			if validity != time.Nanosecond {
				t.Errorf("initial cache lookup required premature renewal: %s", validity)
			}
			return first, nil
		}
		if validity != 24*time.Hour {
			t.Errorf("background renewal did not request sufficient validity: %s", validity)
		}
		return renewed, nil
	}}
	server := startFake(t, options, node)
	awaitState(t, server, StateReady)
	client := clientFor(t, node, first, renewed)
	client.Transport.(*http.Transport).DisableKeepAlives = true
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("https://" + testDomain + "/")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.TLS.PeerCertificates[0].SerialNumber.Int64() == 2 && calls.Load() >= 2 {
			return
		}
	}
	t.Fatal("TLS did not use the certificate published by background renewal")
}

func TestValidNearExpiryCertificateSurvivesUnavailableSynchronousRenewal(t *testing.T) {
	options := testOptions(t)
	certificate := certificateFixture(t, 1, time.Hour)
	node := &fakeBackend{status: validStatus(), cert: func(_ context.Context, _ string, validity time.Duration) (*tls.Certificate, error) {
		if validity != time.Nanosecond {
			return nil, errors.New("synchronous renewal is unavailable")
		}
		return certificate, nil
	}}
	server := startFake(t, options, node)
	awaitState(t, server, StateReady)
	deadline := time.Now().Add(time.Second)
	for server.Status().Error == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.Status().Error == "" {
		t.Fatal("failed background renewal was not surfaced")
	}
	response, err := clientFor(t, node, certificate).Get("https://" + testDomain + "/")
	if err != nil {
		t.Fatalf("valid cached certificate was blocked by renewal: %v", err)
	}
	response.Body.Close()
	if response.TLS.PeerCertificates[0].NotAfter.After(time.Now().Add(24 * time.Hour)) {
		t.Fatal("fixture did not exercise the final 24 hours of validity")
	}
}

func TestExpiredCertificateNeverExposesAnHTTPSListener(t *testing.T) {
	options := testOptions(t)
	certificate := certificateFixture(t, 1, -time.Minute)
	node := &fakeBackend{status: validStatus(), cert: func(context.Context, string, time.Duration) (*tls.Certificate, error) { return certificate, nil }}
	server := startFake(t, options, node)
	awaitState(t, server, StateNeedsHTTPS)
	if node.listenCalls.Load() != 0 || server.Status().URL != "" {
		t.Fatal("expired certificate made remote HTTPS available")
	}
}

func TestDomainChangeDuringInitialCertificateWaitStopsObsoleteRequests(t *testing.T) {
	options := testOptions(t)
	node := &fakeBackend{status: validStatus()}
	node.cert = func(context.Context, string, time.Duration) (*tls.Certificate, error) {
		node.mu.Lock()
		node.status.Self.DNSName = "renamed.example.ts.net."
		node.status.CertDomains = []string{"renamed.example.ts.net"}
		node.mu.Unlock()
		return nil, errors.New("certificate for the old domain is no longer available")
	}
	server := startFake(t, options, node)
	status := awaitState(t, server, StateError)
	<-server.done
	if node.certCalls.Load() != 1 || node.listenCalls.Load() != 0 || node.closeCalls.Load() != 1 || status.URL != "" || status.AuthURL != "" {
		t.Fatalf("domain change kept obsolete certificate requests or exposed HTTPS: cert=%d listen=%d close=%d status=%+v", node.certCalls.Load(), node.listenCalls.Load(), node.closeCalls.Load(), status)
	}
}

func TestWrongSNIIsRejectedWithoutIssuingAnotherCertificate(t *testing.T) {
	options := testOptions(t)
	certificate := certificateFixture(t, 1)
	node := &fakeBackend{status: validStatus(), cert: func(context.Context, string, time.Duration) (*tls.Certificate, error) { return certificate, nil }}
	server := startFake(t, options, node)
	awaitState(t, server, StateReady)
	client := clientFor(t, node, certificate)
	client.Transport.(*http.Transport).TLSClientConfig.ServerName = "other.example.ts.net"
	response, err := client.Get("https://" + testDomain + "/")
	if response != nil {
		response.Body.Close()
	}
	if err == nil || node.certCalls.Load() != 1 {
		t.Fatal("foreign SNI reached the certificate provider or received HTTPS access")
	}
}

func TestCloseCancelsAndJoinsBackgroundRenewalWhileTLSUsesValidCache(t *testing.T) {
	options := testOptions(t)
	certificate := certificateFixture(t, 1, time.Hour)
	entered, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	node := &fakeBackend{status: validStatus(), cert: func(ctx context.Context, _ string, _ time.Duration) (*tls.Certificate, error) {
		if calls.Add(1) == 1 {
			return certificate, nil
		}
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}}
	server := startFake(t, options, node)
	awaitState(t, server, StateReady)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("background renewal did not reach the certificate provider")
	}
	response, err := clientFor(t, node, certificate).Get("https://" + testDomain + "/")
	if err != nil {
		t.Fatalf("blocked renewal stalled a valid cached TLS handshake: %v", err)
	}
	response.Body.Close()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Close left background renewal running")
	}
	if node.closeCalls.Load() != 1 {
		t.Fatal("the node was not closed exactly once after joining renewal")
	}
}

func TestCertificateCacheRejectsExpiredOrWrongHostname(t *testing.T) {
	certificate := certificateFixture(t, 1, time.Hour)
	cache := &certificateCache{value: certificate}
	if _, err := cache.current(testDomain, certificate.Leaf.NotAfter.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.current(testDomain, certificate.Leaf.NotAfter); err == nil {
		t.Fatal("expired cached certificate was served")
	}
	if _, err := cache.current("other.example.ts.net", time.Now()); err == nil {
		t.Fatal("cached certificate was served for another hostname")
	}
}

func TestCloseDuringInitialCertificateDoesNotExposeALateCachedResult(t *testing.T) {
	options := testOptions(t)
	certificate := certificateFixture(t, 1)
	entered := make(chan struct{})
	var handlers atomic.Int32
	options.Handler = func(Endpoint) (http.Handler, func(), error) {
		handlers.Add(1)
		return http.NotFoundHandler(), func() {}, nil
	}
	node := &fakeBackend{status: validStatus(), cert: func(ctx context.Context, _ string, _ time.Duration) (*tls.Certificate, error) {
		close(entered)
		<-ctx.Done()
		// A cached result can arrive concurrently with cancellation.
		return certificate, nil
	}}
	server := startFake(t, options, node)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial certificate lookup did not begin")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if node.listenCalls.Load() != 0 || handlers.Load() != 0 || server.Status().State != StateStopped {
		t.Fatal("a cancelled startup exposed a late HTTPS result")
	}
}

func TestIdentityMonitorFailureCancelsRemoteRequestsAndDrainsHandler(t *testing.T) {
	options := testOptions(t)
	certificate := certificateFixture(t, 1)
	started, cancelled := make(chan struct{}), make(chan struct{})
	var cleanup atomic.Int32
	options.Handler = func(Endpoint) (http.Handler, func(), error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			close(cancelled)
		}), func() { cleanup.Add(1) }, nil
	}
	node := &fakeBackend{status: validStatus(), cert: func(context.Context, string, time.Duration) (*tls.Certificate, error) { return certificate, nil }}
	server := startFake(t, options, node)
	awaitState(t, server, StateReady)
	response, err := clientFor(t, node, certificate).Get("https://" + testDomain + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-started
	node.mu.Lock()
	node.statusErr = errors.New("credential must not escape")
	node.mu.Unlock()
	status := awaitState(t, server, StateError)
	if strings.Contains(status.Error, "credential") || status.URL != "" || status.AuthURL != "" {
		t.Fatalf("unavailable identity leaked private status: %+v", status)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("monitor failure left a remote request alive")
	}
	<-server.done
	if cleanup.Load() != 1 || node.closeCalls.Load() != 1 {
		t.Fatal("monitor failure did not close owned remote resources")
	}
}

func TestTaggedSelfAndExplicitOwnerMismatchNeverExposeHTTP(t *testing.T) {
	for _, kind := range []string{"tagged", "configured owner"} {
		t.Run(kind, func(t *testing.T) {
			options := testOptions(t)
			status := validStatus()
			if kind == "tagged" {
				tags := views.SliceOf([]string{"tag:server"})
				status.Self.Tags = &tags
			} else {
				options.OwnerUserID = "456"
			}
			node := &fakeBackend{status: status}
			server := startFake(t, options, node)
			awaitState(t, server, StateError)
			<-server.done
			if node.listenCalls.Load() != 0 {
				t.Fatal("HTTP was exposed for an unauthorized enrollment")
			}
		})
	}
}
