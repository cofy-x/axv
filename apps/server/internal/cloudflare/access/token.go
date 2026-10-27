// Package access verifies Cloudflare Access application tokens and their original signed leases.
package access

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	identityTimeout = 5 * time.Second
	keyRefreshDelay = 5 * time.Second
	maxJWKSBytes    = 1 << 20
	maxTokenBytes   = 24 << 10
	// LeaseDuration is the maximum lifetime of an accepted owner token.
	// It is measured from signed iat, never from a connection or reconnect.
	LeaseDuration = 15 * time.Minute
)

var errUnauthorized = errors.New("Cloudflare Access authentication failed")

type tokenVerifier struct {
	verifier *oidc.IDTokenVerifier
	audience string
	owner    string
	now      func() time.Time
}

func newTokenVerifier(ctx context.Context, options Options, client *http.Client, now func() time.Time) *tokenVerifier {
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(ctx, client), options.Issuer+"/cdn-cgi/access/certs")
	return &tokenVerifier{
		verifier: oidc.NewVerifier(options.Issuer, keys, &oidc.Config{
			ClientID: options.Audience, SupportedSigningAlgs: []string{oidc.RS256}, Now: now,
		}),
		audience: options.Audience, owner: options.OwnerSubject, now: now,
	}
}

func (v *tokenVerifier) verify(ctx context.Context, raw string) (time.Time, error) {
	if raw == "" || len(raw) > maxTokenBytes || strings.ContainsAny(raw, " \t\r\n,") {
		return time.Time{}, errUnauthorized
	}
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		// Provider/library errors can contain attacker-controlled claims. They
		// must never reach diagnostics, response bodies, or transport status.
		return time.Time{}, errUnauthorized
	}
	var claims struct {
		Type       string          `json:"type"`
		Expires    *int64          `json:"exp"`
		Issued     *int64          `json:"iat"`
		NotBefore  *int64          `json:"nbf"`
		CommonName json.RawMessage `json:"common_name"`
	}
	if err := token.Claims(&claims); err != nil || claims.Type != "app" ||
		token.Subject == "" || token.Subject != v.owner || len(claims.CommonName) != 0 ||
		len(token.Audience) != 1 || token.Audience[0] != v.audience || claims.Expires == nil || claims.Issued == nil {
		return time.Time{}, errUnauthorized
	}
	now := v.now()
	// go-oidc permits clock skew for nbf and does not validate iat. Access is
	// a high-privilege workstation boundary, so enforce these claims without
	// skew and require the signed issuance/expiry values to be consistent.
	expires, issued := time.Unix(*claims.Expires, 0), time.Unix(*claims.Issued, 0)
	if *claims.Issued <= 0 || issued.After(now) || !expires.After(now) || !expires.After(issued) || expires.After(issued.Add(LeaseDuration)) {
		return time.Time{}, errUnauthorized
	}
	if claims.NotBefore != nil && (*claims.NotBefore <= 0 || time.Unix(*claims.NotBefore, 0).After(now) || *claims.NotBefore >= *claims.Expires) {
		return time.Time{}, errUnauthorized
	}
	// Reject longer-lived tokens instead of silently clipping them: Access may
	// keep returning the original application token until its signed expiry.
	// Returning the signed deadline also prevents reconnects renewing a lease.
	return expires, nil
}

// jwksTransport confines go-oidc's background refreshes to the configured HTTPS
// endpoint. It bounds complete responses and joins outstanding refreshes when
// this entrypoint closes, even though RemoteKeySet detaches request cancellation.
type jwksTransport struct {
	base     http.RoundTripper
	endpoint string
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	closed   bool
	wg       sync.WaitGroup
	cacheMu  sync.Mutex
	now      func() time.Time
	fetched  bool
	last     time.Time
	response *http.Response
	body     []byte
}

func newJWKSClient(ctx context.Context, issuer string, injected *http.Client) (*http.Client, *jwksTransport) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: identityTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.TLSHandshakeTimeout = identityTimeout
	transport.ResponseHeaderTimeout = identityTimeout
	transport.MaxResponseHeaderBytes = 32 << 10
	var base http.RoundTripper = transport
	if injected != nil && injected.Transport != nil {
		// Composition may supply a trusted transport for signing-key fixtures.
		// Production callers use the default TLS trust configuration.
		base = injected.Transport
	}
	requestContext, cancel := context.WithCancel(ctx)
	bounded := &jwksTransport{base: base, endpoint: issuer + "/cdn-cgi/access/certs", ctx: requestContext, cancel: cancel, now: time.Now}
	return &http.Client{Transport: bounded, Timeout: identityTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("Cloudflare signing-key redirects are not allowed")
	}}, bounded
}

func (t *jwksTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet || r.URL.String() != t.endpoint || r.URL.Scheme != "https" || r.URL.User != nil {
		return nil, errors.New("unexpected Cloudflare signing-key endpoint")
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("Cloudflare signing-key transport is stopped")
	}
	t.wg.Add(1)
	t.mu.Unlock()
	defer t.wg.Done()
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	defer func() { stop(); cancel() }()
	// RemoteKeySet coalesces concurrent refreshes, but sequential unknown key
	// IDs can otherwise trigger an unbounded series of outbound requests. Keep
	// both successful responses and failed attempts for a short refresh window.
	// A new signing key can be discovered after at most this five-second delay;
	// cached keys never authorize an unknown or invalid token signature.
	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()
	if ctx.Err() != nil || t.ctx.Err() != nil {
		return nil, errors.New("Cloudflare signing-key request was cancelled")
	}
	if t.fetched && t.now().Sub(t.last) < keyRefreshDelay {
		return t.cachedResponse(r)
	}
	response, body, err := t.fetch(r.Clone(ctx))
	t.fetched, t.last = true, t.now()
	if err != nil {
		t.response, t.body = nil, nil
		return nil, errors.New("Cloudflare signing keys are unavailable; retry after the refresh window")
	}
	t.response, t.body = response, body
	return t.cachedResponse(r)
}

func (t *jwksTransport) cachedResponse(r *http.Request) (*http.Response, error) {
	if t.response == nil {
		return nil, errors.New("Cloudflare signing keys are unavailable; retry after the refresh window")
	}
	response := new(http.Response)
	*response = *t.response
	response.Request = r
	response.Header = t.response.Header.Clone()
	response.Body = io.NopCloser(bytes.NewReader(t.body))
	return response, nil
}

func (t *jwksTransport) fetch(r *http.Request) (*http.Response, []byte, error) {
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, nil, errors.New("Cloudflare signing keys are unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, errors.New("Cloudflare signing-key response was not successful")
	}
	if response.ContentLength > maxJWKSBytes {
		return nil, nil, errors.New("Cloudflare signing-key response exceeds its size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxJWKSBytes+1))
	if err != nil || len(body) > maxJWKSBytes {
		return nil, nil, errors.New("Cloudflare signing-key response could not be read within its limit")
	}
	response.Body = nil
	response.ContentLength = int64(len(body))
	return response, body, nil
}

func (t *jwksTransport) close() {
	t.mu.Lock()
	t.closed = true
	t.cancel()
	t.mu.Unlock()
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	t.wg.Wait()
}
