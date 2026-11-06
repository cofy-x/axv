// Package webpush delivers bounded encrypted notifications to supported browser
// push services. Subscription data and provider errors are never logged.
package webpush

import (
	"crypto/ecdh"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/mail"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const maxEndpointBytes = 4096

var (
	ErrSubscription       = errors.New("invalid Web Push subscription")
	ErrUnsupportedService = errors.New("unsupported Web Push service")
	ErrVAPIDKeys          = errors.New("invalid Web Push application keys")
	ErrSubject            = errors.New("invalid Web Push contact")
)

type Keys struct {
	P256DH string `json:"p256dh"`
	Auth   string `json:"auth"`
}

type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     Keys   `json:"keys"`
}

// Validate checks subscription format and supported service policy without DNS
// or any other network activity. The endpoint path and query remain opaque.
func Validate(subscription Subscription) error {
	if _, err := endpointURL(subscription.Endpoint); err != nil {
		return err
	}
	public, err := decodeKey(subscription.Keys.P256DH, 65)
	if err != nil {
		return ErrSubscription
	}
	if _, err := ecdh.P256().NewPublicKey(public); err != nil {
		return ErrSubscription
	}
	if _, err := decodeKey(subscription.Keys.Auth, 16); err != nil {
		return ErrSubscription
	}
	return nil
}

func endpointURL(endpoint string) (*url.URL, error) {
	if len(endpoint) == 0 || len(endpoint) > maxEndpointBytes || !printableASCII(endpoint) {
		return nil, ErrSubscription
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" || strings.Contains(endpoint, "#") || u.Host == "" || u.Path == "" || u.Path == "/" || (u.Port() != "" && u.Port() != "443") {
		return nil, ErrSubscription
	}
	host := strings.ToLower(u.Hostname())
	if !supportedHost(host) {
		return nil, ErrUnsupportedService
	}
	// Canonicalize only the origin, never the capability URL's opaque path or
	// query. A default explicit port is the same origin for VAPID's audience.
	u.Host = host
	return u, nil
}

func supportedHost(host string) bool {
	if host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" {
		return true
	}
	if !strings.HasSuffix(host, ".push.apple.com") || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if char != '-' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}

func printableASCII(value string) bool {
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func decodeKey(value string, size int) ([]byte, error) {
	if len(value) != base64.RawURLEncoding.EncodedLen(size) {
		return nil, ErrSubscription
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != size {
		return nil, ErrSubscription
	}
	return decoded, nil
}

func ValidateVAPIDKeys(privateKey, publicKey string) error {
	private, err := decodeKey(privateKey, 32)
	if err != nil {
		return ErrVAPIDKeys
	}
	key, err := ecdh.P256().NewPrivateKey(private)
	if err != nil {
		return ErrVAPIDKeys
	}
	public, err := decodeKey(publicKey, 65)
	if err != nil || subtle.ConstantTimeCompare(key.PublicKey().Bytes(), public) != 1 {
		return ErrVAPIDKeys
	}
	return nil
}

// ValidateSubject requires an explicitly reviewed vendor-visible contact. It
// never derives a contact from the workstation's potentially private origin.
func ValidateSubject(subject string) error {
	if len(subject) == 0 || len(subject) > 1024 || !printableASCII(subject) {
		return ErrSubject
	}
	if strings.HasPrefix(subject, "https://") {
		u, err := url.Parse(subject)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.Contains(subject, "#") {
			return ErrSubject
		}
		host := strings.ToLower(u.Hostname())
		if address, err := netip.ParseAddr(host); err == nil {
			if !publicAddress(address) {
				return ErrSubject
			}
		} else if !publicContactHost(host) {
			return ErrSubject
		}
		if strings.HasSuffix(u.Host, ":") {
			return ErrSubject
		}
		if port := u.Port(); port != "" {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 {
				return ErrSubject
			}
		}
		return nil
	}
	address := strings.TrimPrefix(subject, "mailto:")
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Address != address || !strings.Contains(address, "@") || strings.ContainsAny(address, "?&#") || !publicContactHost(strings.ToLower(address[strings.LastIndex(address, "@")+1:])) {
		return ErrSubject
	}
	return nil
}

// Contact hosts are checked syntactically only. The contact is never fetched
// or resolved, so this is not a claim that an owner-supplied domain is reachable.
func publicContactHost(host string) bool {
	if !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if char != '-' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}
