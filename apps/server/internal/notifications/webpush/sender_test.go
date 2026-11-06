package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// This receiver uses Go's ECDH/HKDF/AES primitives independently of the sender
// dependency, checking the RFC8291 wire envelope rather than mirroring options.
func decryptNotification(t *testing.T, body []byte, subscription Subscription, receiver *ecdh.PrivateKey) []byte {
	t.Helper()
	if len(body) != 4096 || binary.BigEndian.Uint32(body[16:20]) != 4096 || body[20] != 65 {
		t.Fatal("Invalid bounded aes128gcm record")
	}
	senderPublic, err := ecdh.P256().NewPublicKey(body[21:86])
	if err != nil {
		t.Fatal(err)
	}
	shared, err := receiver.ECDH(senderPublic)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := base64.RawURLEncoding.DecodeString(subscription.Keys.Auth)
	if err != nil {
		t.Fatal(err)
	}
	info := append([]byte("WebPush: info\x00"), receiver.PublicKey().Bytes()...)
	info = append(info, senderPublic.Bytes()...)
	ikm, err := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	if err != nil {
		t.Fatal(err)
	}
	key, err := hkdf.Key(sha256.New, ikm, body[:16], "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, body[:16], "Content-Encoding: nonce\x00", 12)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := aead.Open(nil, nonce, body[86:], nil)
	if err != nil {
		t.Fatal(err)
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		t.Fatal("Invalid record padding delimiter")
	}
	return plain[:len(plain)-1]
}

func verifyAuthorization(t *testing.T, authorization, publicKey, subject string) {
	t.Helper()
	prefix := "vapid t="
	if !strings.HasPrefix(authorization, prefix) {
		t.Fatal("Missing standard VAPID scheme")
	}
	values := strings.Split(strings.TrimPrefix(authorization, prefix), ", k=")
	if len(values) != 2 || values[1] != publicKey {
		t.Fatal("Wrong application public key")
	}
	parts := strings.Split(values[0], ".")
	if len(parts) != 3 {
		t.Fatal("Malformed VAPID JWT")
	}
	decode := func(part string) []byte {
		value, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(decode(parts[0]), &header); err != nil || header.Alg != "ES256" {
		t.Fatal("Wrong signing algorithm")
	}
	var claims struct {
		Audience string `json:"aud"`
		Subject  string `json:"sub"`
		Expires  int64  `json:"exp"`
	}
	if err := json.Unmarshal(decode(parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Audience != "https://fcm.googleapis.com" || claims.Subject != subject || time.Until(time.Unix(claims.Expires, 0)) < 11*time.Hour || time.Until(time.Unix(claims.Expires, 0)) > 12*time.Hour+time.Second {
		t.Fatal("Invalid VAPID audience/contact/expiry")
	}
	public, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), public)
	signature := decode(parts[2])
	if x == nil || len(signature) != 64 {
		t.Fatal("Malformed signing key/signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		t.Fatal("VAPID signature did not verify")
	}
}

func TestEncryptedInteroperableDeliveryAndHourlyAuthorizationReuse(t *testing.T) {
	subscription, receiver := testSubscription(t)
	private, public, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"version":1,"storeId":"opaque-store","id":"opaque-event"}`)
	var mu sync.Mutex
	var headers []string
	var bodies [][]byte
	sender, _, _ := serviceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("TTL") != "60" || r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("Urgency") != "normal" || r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Error("Unexpected Web Push request contract")
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil {
			t.Error(err)
		}
		if bytes.Contains(body, payload) {
			t.Error("Payload was sent unencrypted")
		}
		mu.Lock()
		headers = append(headers, r.Header.Get("Authorization"))
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	for i := 0; i < 2; i++ {
		if outcome := sender.Send(context.Background(), subscription, private, public, payload, time.Minute, "mailto:owner@example.test"); outcome.Status != Accepted {
			t.Fatalf("Delivery was not accepted: %v", outcome)
		}
	}
	if len(headers) != 2 || headers[0] != headers[1] {
		t.Fatal("Authorization refreshed within the reuse window")
	}
	verifyAuthorization(t, headers[0], public, "mailto:owner@example.test")
	for _, body := range bodies {
		if !bytes.Equal(decryptNotification(t, body, subscription, receiver), payload) {
			t.Fatal("Received plaintext differs from the original payload")
		}
	}
	if bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("Delivery reused the per-message salt/ECDH key")
	}
	if outcome := sender.Send(context.Background(), subscription, private, public, payload, time.Minute, "https://example.test/contact"); outcome.Status != Accepted {
		t.Fatal(outcome)
	}
	if headers[2] == headers[0] {
		t.Fatal("Authorization was reused for another reviewed contact")
	}
	verifyAuthorization(t, headers[2], public, "https://example.test/contact")
}

func TestProviderOutcomes(t *testing.T) {
	subscription, _ := testSubscription(t)
	private, public, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		code   int
		status Status
	}{
		{201, Accepted}, {200, Uncertain}, {202, Uncertain}, {400, Rejected}, {401, Rejected}, {403, Rejected}, {404, Expired}, {410, Expired}, {413, Rejected}, {429, RateLimited}, {500, Uncertain}, {503, Uncertain},
	} {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			sender, _, _ := serviceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, "Private provider body must never escape the adapter.")
			}))
			outcome := sender.Send(context.Background(), subscription, private, public, []byte("metadata only"), time.Minute, "owner@example.test")
			if outcome.Status != tc.status {
				t.Fatalf("Wrong outcome: %v", outcome)
			}
			if tc.status == RateLimited && outcome.RetryAfter != 2*time.Minute {
				t.Fatal("Lost bounded provider retry delay")
			}
		})
	}
}

func TestInvalidSendNeverResolvesOrDials(t *testing.T) {
	subscription, _ := testSubscription(t)
	private, public, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	sender := NewSender()
	sender.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		t.Fatal("Invalid send performed DNS")
		return nil, errors.New("unexpected DNS")
	}
	cases := []struct {
		s               Subscription
		private, public string
		payload         []byte
		ttl             time.Duration
		subject         string
	}{
		{subscription, private, public, nil, time.Minute, "owner@example.test"},
		{subscription, private, public, make([]byte, MaxPayloadBytes+1), time.Minute, "owner@example.test"},
		{subscription, private, public, []byte("test"), time.Millisecond, "owner@example.test"},
		{subscription, private, public, []byte("test"), 25 * time.Hour, "owner@example.test"},
		{subscription, "bad-private", public, []byte("test"), time.Minute, "owner@example.test"},
		{subscription, private, public, []byte("test"), time.Minute, ""},
		{Subscription{Endpoint: "https://127.0.0.1/private", Keys: subscription.Keys}, private, public, []byte("test"), time.Minute, "owner@example.test"},
	}
	for _, tc := range cases {
		if outcome := sender.Send(context.Background(), tc.s, tc.private, tc.public, tc.payload, tc.ttl, tc.subject); outcome.Status != Rejected {
			t.Fatal(outcome)
		}
	}
}

func TestDNSFailureAndCancellationAreSafeUncertainty(t *testing.T) {
	subscription, _ := testSubscription(t)
	private, public, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	sender := NewSender()
	sender.lookupIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) { <-ctx.Done(); return nil, ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if outcome := sender.Send(ctx, subscription, private, public, []byte("test"), time.Minute, "owner@example.test"); outcome.Status != Uncertain {
		t.Fatal(outcome)
	}
	sender.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return nil, errors.New("sensitive DNS diagnostic")
	}
	if outcome := sender.Send(context.Background(), subscription, private, public, []byte("test"), time.Minute, "owner@example.test"); outcome.Status != Uncertain {
		t.Fatal(outcome)
	}
	sender.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	if outcome := sender.Send(context.Background(), subscription, private, public, []byte("test"), time.Minute, "owner@example.test"); outcome.Status != Rejected {
		t.Fatal(outcome)
	}
}

func TestRetryAfterIsBounded(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"", time.Hour}, {"bogus", time.Hour}, {"0", time.Minute}, {"30", time.Minute}, {"120", 2 * time.Minute}, {"86400", 24 * time.Hour}, {"999999999999999999999", time.Hour},
		{now.Add(2 * time.Hour).Format(http.TimeFormat), 2 * time.Hour}, {now.Add(48 * time.Hour).Format(http.TimeFormat), 24 * time.Hour}, {now.Add(-time.Hour).Format(http.TimeFormat), time.Minute},
	} {
		if got := retryAfter(tc.header, now); got != tc.want {
			t.Errorf("Retry-After %q: got %v want %v", tc.header, got, tc.want)
		}
	}
}

func TestConcurrentSendsShareOnlyApplicationAuthorization(t *testing.T) {
	subscription, receiver := testSubscription(t)
	private, public, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"version":1,"id":"same-opaque-observation"}`)
	bodies := make(chan []byte, 8)
	headers := make(chan string, 8)
	sender, _, _ := serviceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil {
			t.Error(err)
		}
		bodies <- body
		headers <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	outcomes := make(chan Outcome, 8)
	for i := 0; i < 8; i++ {
		go func() {
			outcomes <- sender.Send(context.Background(), subscription, private, public, payload, time.Minute, "owner@example.test")
		}()
	}
	for i := 0; i < 8; i++ {
		if outcome := <-outcomes; outcome.Status != Accepted {
			t.Fatal(outcome)
		}
	}
	unique := map[string]bool{}
	var authorization string
	for i := 0; i < 8; i++ {
		body := <-bodies
		if !bytes.Equal(decryptNotification(t, body, subscription, receiver), payload) {
			t.Fatal("Concurrent message changed")
		}
		unique[string(body)] = true
		header := <-headers
		if i == 0 {
			authorization = header
		} else if header != authorization {
			t.Fatal("Concurrent sends generated different cached application authorization")
		}
	}
	if len(unique) != 8 {
		t.Fatal("Concurrent sends reused encryption salt or ephemeral keys")
	}
}
