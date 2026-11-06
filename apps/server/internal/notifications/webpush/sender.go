package webpush

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	provider "github.com/SherClockHolmes/webpush-go"
)

const MaxPayloadBytes = 2048

// Status describes provider acceptance, not notification receipt or display.
type Status string

const (
	Accepted    Status = "accepted"
	Uncertain   Status = "uncertain"
	Rejected    Status = "rejected"
	Expired     Status = "expired"
	RateLimited Status = "rate_limited"
)

type Outcome struct {
	Status     Status
	RetryAfter time.Duration
}

func GenerateVAPIDKeys() (privateKey, publicKey string, err error) {
	return provider.GenerateVAPIDKeys()
}

// Send sends one encrypted, finite-lived message. The caller owns durable
// attempt receipts and bounded retries; this package never retries a POST.
func (s *Sender) Send(ctx context.Context, subscription Subscription, privateKey, publicKey string, payload []byte, ttl time.Duration, subject string) Outcome {
	if ctx == nil || s == nil || s.lookupIP == nil || s.dialContext == nil || Validate(subscription) != nil || ValidateVAPIDKeys(privateKey, publicKey) != nil || ValidateSubject(subject) != nil || len(payload) == 0 || len(payload) > MaxPayloadBytes || ttl < time.Second || ttl > 24*time.Hour {
		return Outcome{Status: Rejected}
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	endpoint, _ := endpointURL(subscription.Endpoint)
	client, cleanup, err := s.client(ctx, endpoint.Hostname())
	if err != nil {
		if errors.Is(err, errUnsafeAddress) {
			return Outcome{Status: Rejected}
		}
		return Outcome{Status: Uncertain}
	}
	defer cleanup()
	// The library accepts a bare email and adds the mailto scheme itself.
	contact := strings.TrimPrefix(subject, "mailto:")
	response, err := provider.SendNotificationWithContext(ctx, append([]byte(nil), payload...), &provider.Subscription{
		Endpoint: endpoint.String(),
		Keys:     provider.Keys{P256dh: subscription.Keys.P256DH, Auth: subscription.Keys.Auth},
	}, &provider.Options{
		HTTPClient:      authorizedClient{sender: s, client: client, scope: authorizationScope{origin: endpoint.Scheme + "://" + endpoint.Host, publicKey: publicKey, subject: subject}},
		RecordSize:      4096,
		Subscriber:      contact,
		TTL:             int(ttl / time.Second),
		Urgency:         provider.UrgencyNormal,
		VAPIDPrivateKey: privateKey,
		VAPIDPublicKey:  publicKey,
		VapidExpiration: time.Now().Add(12 * time.Hour),
	})
	if err != nil {
		return Outcome{Status: Uncertain}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
	switch {
	case response.StatusCode == http.StatusCreated:
		return Outcome{Status: Accepted}
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		return Outcome{Status: Expired}
	case response.StatusCode == http.StatusTooManyRequests:
		return Outcome{Status: RateLimited, RetryAfter: retryAfter(response.Header.Get("Retry-After"), time.Now())}
	case response.StatusCode >= 500 || response.StatusCode >= 200 && response.StatusCode < 300:
		return Outcome{Status: Uncertain}
	default:
		return Outcome{Status: Rejected}
	}
}

func retryAfter(value string, now time.Time) time.Duration {
	delay := time.Hour
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= 86400 {
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(value); err == nil {
		delay = at.Sub(now)
	}
	if delay < time.Minute {
		return time.Minute
	}
	if delay > 24*time.Hour {
		return 24 * time.Hour
	}
	return delay
}
