package access

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/internal/accessfixture"
)

func TestVerifyRequiresSignedOwnerApplicationToken(t *testing.T) {
	f := accessfixture.New(t)
	now := time.Now().Truncate(time.Second)
	client, transport := newJWKSClient(context.Background(), testIssuer, f.Client)
	t.Cleanup(transport.close)
	v := newTokenVerifier(context.Background(), Options{Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner}, client, func() time.Time { return now })
	if _, err := v.verify(context.Background(), f.Token(t, now, nil)); err != nil {
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
			if _, err := v.verify(context.Background(), f.Token(t, now, test.claims)); err != errUnauthorized {
				t.Fatalf("expected closed authorization, got %v", err)
			}
		})
	}
	valid := f.Token(t, now, nil)
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
	f := accessfixture.New(t)
	now := time.Now()
	client, transport := newJWKSClient(context.Background(), testIssuer, f.Client)
	t.Cleanup(transport.close)
	var elapsed atomic.Int64
	transport.now = func() time.Time { return now.Add(time.Duration(elapsed.Load())) }
	v := newTokenVerifier(context.Background(), Options{Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner}, client, time.Now)
	raw := f.Token(t, now, nil)
	for range 2 {
		if _, err := v.verify(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.Calls.Load(); got != 1 {
		t.Fatalf("expected cached signing key, got %d requests", got)
	}
	f.Rotate()
	claims := map[string]any{"iss": testIssuer, "sub": testOwner, "aud": []string{testAUD}, "type": "app", "exp": now.Add(14 * time.Minute).Unix(), "iat": now.Add(-time.Minute).Unix()}
	rotated := accessfixture.SignedToken(t, f.Second, "second", claims)
	for range 8 {
		if _, err := v.verify(context.Background(), rotated); err != errUnauthorized {
			t.Fatalf("unknown key was accepted inside refresh window: %v", err)
		}
	}
	if got := f.Calls.Load(); got != 1 {
		t.Fatalf("sequential unknown key IDs triggered a request storm: %d requests", got)
	}
	elapsed.Store(int64(keyRefreshDelay))
	if _, err := v.verify(context.Background(), rotated); err != nil {
		t.Fatalf("rotated signing key was not discovered: %v", err)
	}
	if got := f.Calls.Load(); got != 2 {
		t.Fatalf("expected rotation refresh, got %d requests", got)
	}
	f.SetMode("error")
	if _, err := v.verify(context.Background(), accessfixture.SignedToken(t, f.First, "unknown", claims)); err != errUnauthorized {
		t.Fatalf("unknown key must fail closed while JWKS is unavailable: %v", err)
	}
}

func TestSigningKeyFailuresBackOffAndRecoverAfterRefreshWindow(t *testing.T) {
	f := accessfixture.New(t)
	f.SetMode("error")
	now := time.Now()
	client, transport := newJWKSClient(context.Background(), testIssuer, f.Client)
	t.Cleanup(transport.close)
	var elapsed atomic.Int64
	transport.now = func() time.Time { return now.Add(time.Duration(elapsed.Load())) }
	v := newTokenVerifier(context.Background(), Options{Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner}, client, time.Now)
	raw := f.Token(t, now, nil)
	for range 8 {
		if _, err := v.verify(context.Background(), raw); err != errUnauthorized {
			t.Fatalf("failed key fetch authorized a request: %v", err)
		}
	}
	if got := f.Calls.Load(); got != 1 {
		t.Fatalf("failed sequential refreshes triggered a request storm: %d requests", got)
	}
	elapsed.Store(int64(keyRefreshDelay))
	if _, err := v.verify(context.Background(), raw); err != errUnauthorized {
		t.Fatalf("retry after window must still fail closed: %v", err)
	}
	if got := f.Calls.Load(); got != 2 {
		t.Fatalf("retry window did not permit one new fetch: %d", got)
	}
	f.SetMode("")
	if _, err := v.verify(context.Background(), raw); err != errUnauthorized {
		t.Fatalf("negative cache was bypassed before the next window: %v", err)
	}
	elapsed.Store(int64(2 * keyRefreshDelay))
	if _, err := v.verify(context.Background(), raw); err != nil {
		t.Fatalf("key refresh did not recover after retry window: %v", err)
	}
	if got := f.Calls.Load(); got != 3 {
		t.Fatalf("unexpected recovered fetch count: %d", got)
	}
}

func TestSigningKeyFetchesRejectUntrustedResponses(t *testing.T) {
	f := accessfixture.New(t)
	for _, mode := range []string{"redirect", "error", "invalid", "large"} {
		t.Run(mode, func(t *testing.T) {
			f.SetMode(mode)
			client, transport := newJWKSClient(context.Background(), testIssuer, f.Client)
			defer transport.close()
			v := newTokenVerifier(context.Background(), Options{Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner}, client, time.Now)
			if _, err := v.verify(context.Background(), f.Token(t, time.Now(), nil)); err != errUnauthorized {
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
	f := accessfixture.New(t)
	f.SetMode("slow")
	client, transport = newJWKSClient(context.Background(), testIssuer, f.Client)
	v := newTokenVerifier(context.Background(), Options{Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner}, client, time.Now)
	done := make(chan error, 1)
	raw := f.Token(t, time.Now(), nil)
	go func() { _, err := v.verify(context.Background(), raw); done <- err }()
	select {
	case <-f.Start:
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
	f := accessfixture.New(t)
	f.SetMode("slow")
	client, transport := newJWKSClient(context.Background(), testIssuer, f.Client)
	defer transport.close()
	v := newTokenVerifier(context.Background(), Options{Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner}, client, time.Now)
	raw := f.Token(t, time.Now(), nil)
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

const (
	testIssuer = accessfixture.Issuer
	testAUD    = accessfixture.Audience
	testOwner  = accessfixture.Owner
)
