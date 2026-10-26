package policy

import (
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// CookieSite returns the browser's registrable cookie boundary,
// including PRIVATE PSL entries. Unknown suffixes cannot establish isolation.
func CookieSite(hostname string) (string, error) {
	if !ValidOrigin("https://" + hostname) {
		return "", errors.New("Cloudflare isolation requires a canonical public DNS hostname")
	}
	suffix, icann := publicsuffix.PublicSuffix(hostname)
	if !icann && !strings.Contains(suffix, ".") {
		return "", errors.New("Cloudflare isolation requires a known public suffix")
	}
	if suffix == hostname {
		// A public-suffix host has no registrable parent; its own host is its site.
		return hostname, nil
	}
	site, err := publicsuffix.EffectiveTLDPlusOne(hostname)
	if err != nil {
		return "", errors.New("Cloudflare isolation could not establish the cookie site")
	}
	return site, nil
}

// AllocationMode permits either one permanently dedicated registrable
// site or direct children of an existing public suffix. Arbitrary nested domains
// cannot be used as an isolation boundary.
func AllocationMode(domain string) (string, error) {
	site, err := CookieSite(domain)
	if err != nil {
		return "", err
	}
	suffix, _ := publicsuffix.PublicSuffix(domain)
	if suffix == domain {
		return "public-suffix", nil
	}
	if site != domain {
		return "", errors.New("Cloudflare publishing needs a dedicated registrable domain or an existing public suffix")
	}
	return "dedicated-site", nil
}

func ValidateAllocationDomain(domain string) error {
	_, err := AllocationMode(domain)
	return err
}

// ValidateIsolation checks the allocation configuration. The publisher
// separately checks each generated host against every retained site receipt.
func ValidateIsolation(desktopOrigin, allocationDomain string) error {
	if allocationDomain == "" {
		return nil
	}
	mode, err := AllocationMode(allocationDomain)
	if err != nil {
		return err
	}
	if desktopOrigin == "" {
		return nil
	}
	if !ValidOrigin(desktopOrigin) {
		return errors.New("Cloudflare desktop isolation requires a canonical HTTPS origin")
	}
	origin, _ := url.Parse(desktopOrigin)
	site, err := CookieSite(origin.Hostname())
	if err != nil {
		return err
	}
	if mode == "dedicated-site" && site == allocationDomain {
		return errors.New("Cloudflare desktop and published applications must use different cookie sites")
	}
	return nil
}
