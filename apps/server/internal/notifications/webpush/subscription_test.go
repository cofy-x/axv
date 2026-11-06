package webpush

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func testSubscription(t *testing.T) (Subscription, *ecdh.PrivateKey) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Subscription{Endpoint: "https://fcm.googleapis.com/wp/opaque-subscription", Keys: Keys{
		P256DH: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:   base64.RawURLEncoding.EncodeToString([]byte("test-auth-secret")),
	}}, key
}

func TestSubscriptionValidation(t *testing.T) {
	subscription, _ := testSubscription(t)
	for _, endpoint := range []string{
		"https://fcm.googleapis.com/wp/token",
		"https://fcm.googleapis.com/fcm/send/token",
		"https://fcm.googleapis.com:443/wp/token?opaque=capability",
		"https://updates.push.services.mozilla.com/wpush/v2/token",
		"https://web.push.apple.com/Qtoken",
		"https://WEB.PUSH.APPLE.COM/Qtoken",
	} {
		subscription.Endpoint = endpoint
		if err := Validate(subscription); err != nil {
			t.Errorf("Valid endpoint rejected: %s: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{
		"", "https://fcm.googleapis.com", "https://fcm.googleapis.com/", "http://fcm.googleapis.com/wp/token",
		"https://fcm.googleapis.com:444/wp/token", "https://user:secret@fcm.googleapis.com/wp/token",
		"https://fcm.googleapis.com/wp/token#secret", "https://fcm.googleapis.com/wp/token#",
		"https://localhost/wp/token", "https://127.0.0.1/wp/token", "https://[::1]/wp/token",
		"https://fcm.googleapis.com.evil.test/wp/token", "https://evilpush.apple.com/token", "https://push.apple.com/token",
		"https://.push.apple.com/token", "https://bad-.push.apple.com/token", "https://a..push.apple.com/token",
		"https://web.push.apple.com./token", "https://updates.push.services.mozilla.com.evil.test/token",
		"https://fcm.googleapis.com/wp/a b", "https://fcm.googleapis.com/wp/token\n", "https://fcm.googleapis.com/wp/é",
		"https://fcm.googleapis.com/wp/" + strings.Repeat("x", maxEndpointBytes),
	} {
		subscription.Endpoint = endpoint
		if err := Validate(subscription); err == nil {
			t.Errorf("Unsafe endpoint accepted: %q", endpoint)
		}
	}
	subscription, _ = testSubscription(t)
	for _, mutate := range []func(*Subscription){
		func(s *Subscription) { s.Keys.Auth += "==" },
		func(s *Subscription) { s.Keys.Auth = strings.Repeat("A", 21) + "B" },
		func(s *Subscription) { s.Keys.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 15)) },
		func(s *Subscription) { s.Keys.P256DH += "=" },
		func(s *Subscription) { s.Keys.P256DH = base64.RawURLEncoding.EncodeToString(make([]byte, 65)) },
		func(s *Subscription) {
			s.Keys.P256DH = base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))
		},
		func(s *Subscription) { s.Keys.P256DH = "_" + s.Keys.P256DH[1:] },
	} {
		candidate := subscription
		mutate(&candidate)
		if err := Validate(candidate); err == nil {
			t.Error("Malformed subscription key accepted")
		}
	}
}

func TestVAPIDKeysAndExplicitContact(t *testing.T) {
	private, public, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateVAPIDKeys(private, public); err != nil {
		t.Fatal(err)
	}
	_, otherPublic, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, keys := range [][2]string{{private, otherPublic}, {private + "=", public}, {"", public}, {base64.RawURLEncoding.EncodeToString(make([]byte, 32)), public}, {private, ""}} {
		if ValidateVAPIDKeys(keys[0], keys[1]) == nil {
			t.Error("Invalid application key pair accepted")
		}
	}
	for _, subject := range []string{"owner@example.test", "mailto:owner@example.test", "https://example.test/contact", "https://example.test/contact?team=axv"} {
		if ValidateSubject(subject) != nil {
			t.Errorf("Valid reviewed contact rejected: %s", subject)
		}
	}
	for _, subject := range []string{"", "mailto:", "mailto:mailto:owner@example.test", "http://example.test/contact", "https://user:password@example.test/contact", "https://example.test/#secret", "name <owner@example.test>", "owner@example.test?subject=secret", "owner\n@example.test", "https://", "https://localhost/contact", "https://device.localhost/contact", "https://device.local/contact", "https://127.0.0.1/contact", "https://[::1]/contact", "https://192.168.1.1/contact", "https://example.test:65536/contact", "https://example.test:/contact", "owner@localhost"} {
		if ValidateSubject(subject) == nil {
			t.Errorf("Invalid contact accepted: %s", subject)
		}
	}
}
