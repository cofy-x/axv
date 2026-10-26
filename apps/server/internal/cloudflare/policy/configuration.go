// Package policy owns Cloudflare configuration values and deterministic validation.
// It performs no filesystem or network operations.
package policy

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const DefaultDesktopListen = "127.0.0.1:3002"

// Configuration configures desktop Access and optional account-managed publishing.
// Tunnel connector credentials and the cloudflared process remain external.
type Configuration struct {
	Enabled      bool    `json:"enabled"`
	Listen       string  `json:"listen,omitempty"`
	Origin       string  `json:"origin,omitempty"`
	Issuer       string  `json:"issuer,omitempty"`
	Audience     string  `json:"audience,omitempty"`
	OwnerSubject string  `json:"owner_subject,omitempty"`
	Management   Account `json:"management,omitempty"`
}

// Validate permits an incomplete disabled configuration, while
// validating every populated field and requiring all identity settings to enable.
func Validate(c Configuration) error {
	if c.Listen != "" {
		host, port, err := net.SplitHostPort(c.Listen)
		ip := net.ParseIP(host)
		n, portErr := strconv.Atoi(port)
		if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return fmt.Errorf("cloudflare.listen must be a numeric loopback address with a port from 1 to 65535")
		}
	}
	if c.Origin != "" && !ValidOrigin(c.Origin) {
		return fmt.Errorf("cloudflare.origin must be an exact HTTPS origin with a public DNS hostname and no port")
	}
	if c.Issuer != "" {
		u, ok := exactHTTPSOrigin(c.Issuer)
		if !ok || !strings.HasSuffix(u.Host, ".cloudflareaccess.com") || !dnsLabel(strings.TrimSuffix(u.Host, ".cloudflareaccess.com")) {
			return fmt.Errorf("cloudflare.issuer must be https://<team>.cloudflareaccess.com")
		}
	}
	if c.Audience != "" && !cloudflareToken(c.Audience) {
		return fmt.Errorf("cloudflare.audience must be a bounded Access audience token")
	}
	if c.OwnerSubject != "" && !cloudflareToken(c.OwnerSubject) {
		return fmt.Errorf("cloudflare.owner_subject must be a bounded stable Access subject")
	}
	if c.Enabled && (c.Origin == "" || c.Issuer == "" || c.Audience == "" || c.OwnerSubject == "") {
		return fmt.Errorf("enabled Cloudflare access requires origin, issuer, audience and owner_subject")
	}
	if err := ValidateAccount(c.Management); err != nil {
		return err
	}
	if err := ValidateIsolation(c.Origin, c.Management.Domain); err != nil {
		return err
	}
	if c.Management.Listen != "" && c.Management.Listen == c.Listen {
		return fmt.Errorf("Cloudflare desktop and publication listeners must use different addresses")
	}
	return nil
}

// ValidOrigin validates the public browser origin without resolving DNS or
// performing network requests during local startup and status projection.
func ValidOrigin(value string) bool {
	u, ok := exactHTTPSOrigin(value)
	if !ok || len(u.Host) > 253 || net.ParseIP(u.Host) != nil || !strings.Contains(u.Host, ".") {
		return false
	}
	for _, reserved := range []string{"localhost", "local", "internal", "home.arpa", "test", "invalid", "example"} {
		if u.Host == reserved || strings.HasSuffix(u.Host, "."+reserved) {
			return false
		}
	}
	labels := strings.Split(u.Host, ".")
	for _, label := range labels {
		if !dnsLabel(label) {
			return false
		}
	}
	for _, c := range labels[len(labels)-1] {
		if c < '0' || c > '9' {
			return true
		}
	}
	return false
}

func exactHTTPSOrigin(value string) (*url.URL, bool) {
	u, err := url.Parse(value)
	return u, err == nil && u != nil && u.Scheme == "https" && u.Host != "" && u.Host == u.Hostname() && u.User == nil &&
		u.Opaque == "" && u.Path == "" && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery &&
		u.Fragment == "" && u.RawFragment == "" && u.String() == value
}

func dnsLabel(value string) bool {
	if value == "" || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, c := range value {
		if c < 'a' || c > 'z' {
			if c < '0' || c > '9' {
				if c != '-' {
					return false
				}
			}
		}
	}
	return true
}

func cloudflareToken(value string) bool {
	if len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, c := range value {
		if unicode.IsSpace(c) || unicode.IsControl(c) {
			return false
		}
	}
	return value != ""
}
