package access

import (
	"context"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/internal/accessfixture"
)

func TestOwnerLeaseUsesOriginalIssuanceAndCannotRenewOnReconnect(t *testing.T) {
	fixture := accessfixture.New(t)
	issued := time.Now().Truncate(time.Second)
	now := issued
	verifier, err := newVerifier(context.Background(), Options{Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner}, dependencies{client: fixture.Client, now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	expires := issued.Add(LeaseDuration)
	raw := fixture.Token(t, issued, map[string]any{"iat": issued.Unix(), "nbf": issued.Unix(), "exp": expires.Unix()})
	for _, elapsed := range []time.Duration{0, 5 * time.Minute, 14*time.Minute + 59*time.Second} {
		now = issued.Add(elapsed)
		deadline, err := verifier.Verify(context.Background(), raw)
		if err != nil || !deadline.Equal(expires) {
			t.Fatalf("reconnect changed the original lease at %v: %v, %v", elapsed, deadline, err)
		}
	}
	now = expires
	if _, err := verifier.Verify(context.Background(), raw); err == nil {
		t.Fatal("reconnecting renewed an expired lease")
	}
	now = issued
	for _, changes := range []map[string]any{
		{"exp": expires.Add(time.Second).Unix()},
		{"iat": issued.Add(-LeaseDuration).Unix(), "exp": issued.Add(time.Second).Unix()},
		{"exp": issued.Add(24 * time.Hour).Unix()},
	} {
		if _, err := verifier.Verify(context.Background(), fixture.Token(t, issued, changes)); err == nil {
			t.Fatalf("an overlong signed lease was accepted: %+v", changes)
		}
	}
}

func TestSigningKeyOutageDoesNotExtendAnExistingOwnerLease(t *testing.T) {
	fixture := accessfixture.New(t)
	issued := time.Now().Truncate(time.Second)
	now := issued
	client, keys := newJWKSClient(context.Background(), testIssuer, fixture.Client)
	defer keys.close()
	keys.now = func() time.Time { return now }
	verifier := newTokenVerifier(context.Background(), Options{Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner}, client, func() time.Time { return now })
	expires := issued.Add(LeaseDuration)
	raw := fixture.Token(t, issued, map[string]any{"iat": issued.Unix(), "nbf": issued.Unix(), "exp": expires.Unix()})
	if _, err := verifier.verify(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	fixture.SetMode("error")
	now = issued.Add(14 * time.Minute)
	deadline, err := verifier.verify(context.Background(), raw)
	if err != nil || !deadline.Equal(expires) {
		t.Fatal("a cached signing key lost or renewed the finite signed lease")
	}
	unknown := accessfixture.SignedToken(t, fixture.Second, "unavailable-key", map[string]any{"iss": testIssuer, "sub": testOwner, "aud": []string{testAUD}, "type": "app", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix()})
	if _, err := verifier.verify(context.Background(), unknown); err == nil {
		t.Fatal("an unknown key bypassed the signing-key outage")
	}
	if fixture.Calls.Load() != 2 {
		t.Fatal("the outage case did not exercise a real failed signing-key fetch")
	}
	now = expires
	if _, err := verifier.verify(context.Background(), raw); err == nil {
		t.Fatal("cached keys or outage handling extended the old lease")
	}
}
