package cloudflare

import (
	"context"
	"testing"
	"time"
)

func TestReusableAccessVerifierKeepsApplicationOwnerAndLifetimeBoundaries(t *testing.T) {
	fixture := newKeyFixture(t)
	now := time.Now().Truncate(time.Second)
	verifier, err := newAccessVerifier(context.Background(), AccessOptions{Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner}, dependencies{client: fixture.client, now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	valid := fixture.token(t, now, nil)
	expires, err := verifier.Verify(context.Background(), valid)
	if err != nil || !expires.Equal(now.Add(14*time.Minute)) {
		t.Fatalf("valid publishing identity was not verified: %v", err)
	}
	for _, changes := range []map[string]any{{"sub": "another-owner"}, {"aud": []string{"another-application"}}, {"common_name": "service-token"}} {
		if _, err = verifier.Verify(context.Background(), fixture.token(t, now, changes)); err == nil {
			t.Fatalf("foreign publishing authority accepted: %+v", changes)
		}
	}
	verifier.Close()
	if _, err = verifier.Verify(context.Background(), valid); err == nil {
		t.Fatal("closed verifier authorized a cached token")
	}
	for _, options := range []AccessOptions{{}, {Issuer: testIssuer, Audience: testAUD}, {Issuer: "https://foreign.example.com", Audience: testAUD, OwnerSubject: testOwner}} {
		if _, err = NewAccessVerifier(context.Background(), options); err == nil {
			t.Fatalf("incomplete or foreign issuer accepted: %+v", options)
		}
	}
}
