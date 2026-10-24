package cloudflare

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

const (
	testOrigin = "https://axv.example.com"
	testIssuer = "https://owner.cloudflareaccess.com"
	testOwner  = "7335d417-61da-459d-899c-0a01c76a2f94"
	testAUD    = "4714c1358e65fe4b408ad6d432a5f878f08194bdb4752441fd56faefa9b2b6f2"
)

type keyFixture struct {
	server *httptest.Server
	client *http.Client
	first  *rsa.PrivateKey
	second *rsa.PrivateKey
	mu     sync.Mutex
	keys   []jose.JSONWebKey
	mode   string
	calls  atomic.Int64
	start  chan struct{}
}

func newKeyFixture(t *testing.T) *keyFixture {
	t.Helper()
	first, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &keyFixture{first: first, second: second, keys: []jose.JSONWebKey{{Key: &first.PublicKey, KeyID: "first", Algorithm: "RS256", Use: "sig"}}, start: make(chan struct{}, 20)}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		f.start <- struct{}{}
		if r.URL.Path != "/cdn-cgi/access/certs" || r.Host != "owner.cloudflareaccess.com" {
			t.Errorf("unexpected JWKS request: %s %s", r.Host, r.URL.Path)
		}
		f.mu.Lock()
		mode, keys := f.mode, append([]jose.JSONWebKey(nil), f.keys...)
		f.mu.Unlock()
		switch mode {
		case "redirect":
			http.Redirect(w, r, "https://attacker.example.com/keys", http.StatusFound)
		case "error":
			http.Error(w, "fixture failure", http.StatusInternalServerError)
		case "invalid":
			_, _ = io.WriteString(w, "not a key set")
		case "large":
			_, _ = io.WriteString(w, strings.Repeat(" ", maxJWKSBytes+1))
		case "slow":
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: keys})
		}
	}))
	transport := f.server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "owner.cloudflareaccess.com:443" {
			t.Errorf("unexpected JWKS destination: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, f.server.Listener.Addr().String())
	}
	f.client = &http.Client{Transport: transport}
	t.Cleanup(func() { transport.CloseIdleConnections(); f.server.Close() })
	return f
}

func (f *keyFixture) setMode(mode string) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

func (f *keyFixture) token(t *testing.T, now time.Time, updates map[string]any) string {
	t.Helper()
	claims := map[string]any{"iss": testIssuer, "sub": testOwner, "aud": []string{testAUD}, "type": "app",
		"exp": now.Add(14 * time.Minute).Unix(), "iat": now.Add(-time.Minute).Unix(), "nbf": now.Add(-time.Minute).Unix(), "email": "ignored@example.com"}
	for key, value := range updates {
		if value == nil {
			delete(claims, key)
		} else {
			claims[key] = value
		}
	}
	return signedToken(t, f.first, "first", claims)
}

func signedToken(t *testing.T, key *rsa.PrivateKey, kid string, claims any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	object, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := object.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func testOptions(t *testing.T) Options {
	t.Helper()
	// Configuration requires an explicit, nonzero port. Reserve an OS-selected
	// loopback port briefly without changing the production validation rules.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return Options{Listen: address, Origin: testOrigin, Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner,
		Handler:    http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		AllowRoute: func(*http.Request) bool { return true }}
}

func TestVerifyRequiresSignedOwnerApplicationToken(t *testing.T) {
	f := newKeyFixture(t)
	now := time.Now().Truncate(time.Second)
	client, transport := newJWKSClient(context.Background(), testIssuer, f.client)
	t.Cleanup(transport.close)
	v := newTokenVerifier(context.Background(), testOptions(t), client, func() time.Time { return now })
	if _, err := v.verify(context.Background(), f.token(t, now, nil)); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		claims map[string]any
	}{
		{"wrong issuer", map[string]any{"iss": "https://attacker.cloudflareaccess.com"}},
		{"wrong owner", map[string]any{"sub": "different-owner"}},
		{"missing subject", map[string]any{"sub": nil}},
		{"service token", map[string]any{"sub": "", "common_name": "client.access"}},
		{"service claim with subject", map[string]any{"common_name": "client.access"}},
		{"wrong audience", map[string]any{"aud": []string{"other"}}},
		{"multiple audiences", map[string]any{"aud": []string{testAUD, "other"}}},
		{"duplicate audiences", map[string]any{"aud": []string{testAUD, testAUD}}},
		{"missing audience", map[string]any{"aud": nil}},
		{"global token", map[string]any{"type": "org"}},
		{"missing type", map[string]any{"type": nil}},
		{"expired", map[string]any{"exp": now.Add(-time.Second).Unix()}},
		{"expires now", map[string]any{"exp": now.Unix()}},
		{"missing expiry", map[string]any{"exp": nil}},
		{"missing issuance", map[string]any{"iat": nil}},
		{"future issuance", map[string]any{"iat": now.Add(time.Second).Unix()}},
		{"future not before within library skew", map[string]any{"nbf": now.Add(time.Second).Unix()}},
		{"invalid numeric claim", map[string]any{"exp": "tomorrow"}},
		{"fractional timestamp", map[string]any{"iat": float64(now.Unix()) - 0.5}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := v.verify(context.Background(), f.token(t, now, test.claims)); err != errUnauthorized {
				t.Fatalf("expected closed authorization, got %v", err)
			}
		})
	}
	valid := f.token(t, now, nil)
	parts := strings.Split(valid, ".")
	parts[0] = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"first"}`))
	for _, raw := range []string{"", "malformed", strings.Join(parts, "."), valid + "," + valid, strings.Repeat("a", maxTokenBytes+1)} {
		if _, err := v.verify(context.Background(), raw); err != errUnauthorized {
			t.Fatalf("untrusted token was accepted: %v", err)
		}
	}
	parts = strings.Split(valid, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"forged-owner"}`))
	if _, err := v.verify(context.Background(), strings.Join(parts, ".")); err != errUnauthorized {
		t.Fatalf("tampered token was accepted: %v", err)
	}
}

func TestSigningKeysCacheAndRotateByKeyID(t *testing.T) {
	f := newKeyFixture(t)
	now := time.Now()
	client, transport := newJWKSClient(context.Background(), testIssuer, f.client)
	t.Cleanup(transport.close)
	var elapsed atomic.Int64
	transport.now = func() time.Time { return now.Add(time.Duration(elapsed.Load())) }
	v := newTokenVerifier(context.Background(), testOptions(t), client, time.Now)
	raw := f.token(t, now, nil)
	for range 2 {
		if _, err := v.verify(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("expected cached signing key, got %d requests", got)
	}
	f.mu.Lock()
	f.keys = []jose.JSONWebKey{{Key: &f.second.PublicKey, KeyID: "second", Algorithm: "RS256", Use: "sig"}}
	f.mu.Unlock()
	claims := map[string]any{"iss": testIssuer, "sub": testOwner, "aud": []string{testAUD}, "type": "app", "exp": now.Add(14 * time.Minute).Unix(), "iat": now.Add(-time.Minute).Unix()}
	rotated := signedToken(t, f.second, "second", claims)
	for range 8 {
		if _, err := v.verify(context.Background(), rotated); err != errUnauthorized {
			t.Fatalf("unknown key was accepted inside refresh window: %v", err)
		}
	}
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("sequential unknown key IDs triggered a request storm: %d requests", got)
	}
	elapsed.Store(int64(keyRefreshDelay))
	if _, err := v.verify(context.Background(), rotated); err != nil {
		t.Fatalf("rotated signing key was not discovered: %v", err)
	}
	if got := f.calls.Load(); got != 2 {
		t.Fatalf("expected rotation refresh, got %d requests", got)
	}
	f.setMode("error")
	if _, err := v.verify(context.Background(), signedToken(t, f.first, "unknown", claims)); err != errUnauthorized {
		t.Fatalf("unknown key must fail closed while JWKS is unavailable: %v", err)
	}
}

func TestSigningKeyFailuresBackOffAndRecoverAfterRefreshWindow(t *testing.T) {
	f := newKeyFixture(t)
	f.setMode("error")
	now := time.Now()
	client, transport := newJWKSClient(context.Background(), testIssuer, f.client)
	t.Cleanup(transport.close)
	var elapsed atomic.Int64
	transport.now = func() time.Time { return now.Add(time.Duration(elapsed.Load())) }
	v := newTokenVerifier(context.Background(), testOptions(t), client, time.Now)
	raw := f.token(t, now, nil)
	for range 8 {
		if _, err := v.verify(context.Background(), raw); err != errUnauthorized {
			t.Fatalf("failed key fetch authorized a request: %v", err)
		}
	}
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("failed sequential refreshes triggered a request storm: %d requests", got)
	}
	elapsed.Store(int64(keyRefreshDelay))
	if _, err := v.verify(context.Background(), raw); err != errUnauthorized {
		t.Fatalf("retry after window must still fail closed: %v", err)
	}
	if got := f.calls.Load(); got != 2 {
		t.Fatalf("retry window did not permit one new fetch: %d", got)
	}
	f.setMode("")
	if _, err := v.verify(context.Background(), raw); err != errUnauthorized {
		t.Fatalf("negative cache was bypassed before the next window: %v", err)
	}
	elapsed.Store(int64(2 * keyRefreshDelay))
	if _, err := v.verify(context.Background(), raw); err != nil {
		t.Fatalf("key refresh did not recover after retry window: %v", err)
	}
	if got := f.calls.Load(); got != 3 {
		t.Fatalf("unexpected recovered fetch count: %d", got)
	}
}

func TestSigningKeyFetchesRejectUntrustedResponses(t *testing.T) {
	f := newKeyFixture(t)
	for _, mode := range []string{"redirect", "error", "invalid", "large"} {
		t.Run(mode, func(t *testing.T) {
			f.setMode(mode)
			client, transport := newJWKSClient(context.Background(), testIssuer, f.client)
			defer transport.close()
			v := newTokenVerifier(context.Background(), testOptions(t), client, time.Now)
			if _, err := v.verify(context.Background(), f.token(t, time.Now(), nil)); err != errUnauthorized {
				t.Fatalf("untrusted key response accepted: %v", err)
			}
		})
	}
}

func TestSigningKeyClientIsRestrictedAndCloseCancelsRefresh(t *testing.T) {
	client, transport := newJWKSClient(context.Background(), testIssuer, nil)
	base := transport.base.(*http.Transport)
	if base.Proxy != nil || base.TLSClientConfig == nil || base.TLSClientConfig.InsecureSkipVerify || client.Timeout != identityTimeout {
		t.Fatal("production key fetches must use bounded HTTPS without environment proxies")
	}
	for _, endpoint := range []string{"http://owner.cloudflareaccess.com/cdn-cgi/access/certs", testIssuer + "/other", "https://attacker.example.com/cdn-cgi/access/certs"} {
		request, _ := http.NewRequest(http.MethodGet, endpoint, nil)
		if _, err := client.Do(request); err == nil {
			t.Fatal("unexpected key endpoint was accepted")
		}
	}
	transport.close()
	f := newKeyFixture(t)
	f.setMode("slow")
	client, transport = newJWKSClient(context.Background(), testIssuer, f.client)
	v := newTokenVerifier(context.Background(), testOptions(t), client, time.Now)
	done := make(chan error, 1)
	raw := f.token(t, time.Now(), nil)
	go func() { _, err := v.verify(context.Background(), raw); done <- err }()
	select {
	case <-f.start:
	case <-time.After(3 * time.Second):
		t.Fatal("key request did not start")
	}
	transport.close()
	select {
	case err := <-done:
		if err != errUnauthorized {
			t.Fatalf("cancelled refresh authorized a request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing ingress did not cancel the detached JWKS refresh")
	}
}

func TestSigningKeyRefreshHonorsRequestAndClientTimeouts(t *testing.T) {
	f := newKeyFixture(t)
	f.setMode("slow")
	client, transport := newJWKSClient(context.Background(), testIssuer, f.client)
	defer transport.close()
	v := newTokenVerifier(context.Background(), testOptions(t), client, time.Now)
	raw := f.token(t, time.Now(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := v.verify(ctx, raw); err != errUnauthorized || ctx.Err() == nil {
		t.Fatalf("request timeout did not fail closed: %v", err)
	}
	// RemoteKeySet intentionally detaches the shared refresh from a single
	// request. Its private HTTP client must still bound the complete fetch.
	started := time.Now()
	if _, err := v.verify(context.Background(), raw); err != errUnauthorized {
		t.Fatalf("HTTP client timeout did not fail closed: %v", err)
	}
	if elapsed := time.Since(started); elapsed > identityTimeout+2*time.Second {
		t.Fatalf("key refresh exceeded its HTTP deadline: %v", elapsed)
	}
}
