package policy

import "testing"

func TestCloudflareAllocationUsesICANNAndPrivateCookieSites(t *testing.T) {
	for _, tc := range []struct{ host, site, mode string }{
		{"example.com", "example.com", "dedicated-site"},
		{"example.co.uk", "example.co.uk", "dedicated-site"},
		{"github.io", "github.io", "public-suffix"},
		{"co.uk", "co.uk", "public-suffix"},
		{"project.github.io", "project.github.io", "dedicated-site"},
	} {
		site, err := CookieSite(tc.host)
		if err != nil || site != tc.site {
			t.Fatalf("cookie site %s: %s %v", tc.host, site, err)
		}
		mode, err := AllocationMode(tc.host)
		if err != nil || mode != tc.mode || ValidateAllocationDomain(tc.host) != nil {
			t.Fatalf("allocation %s: %s %v", tc.host, mode, err)
		}
	}
	for _, host := range []string{"apps.example.com", "app.example.co.uk", "app.project.github.io", "example.notarealsuffix", "EXAMPLE.com", "example.com.", "localhost", "127.0.0.1", "https://example.com"} {
		if ValidateAllocationDomain(host) == nil {
			t.Fatalf("unsafe allocation domain accepted: %s", host)
		}
	}
	for _, tc := range []struct{ host, site string }{
		{"app.example.com", "example.com"}, {"app.example.co.uk", "example.co.uk"},
		{"a.github.io", "a.github.io"}, {"b.github.io", "b.github.io"},
	} {
		site, err := CookieSite(tc.host)
		if err != nil || site != tc.site {
			t.Fatalf("nested site %s: %s %v", tc.host, site, err)
		}
	}
}

func TestCloudflareAllocationRejectsDesktopSharedCookieSites(t *testing.T) {
	for _, tc := range []struct{ origin, domain string }{
		{"https://desktop.example.com", "example.com"},
		{"https://desktop.example.co.uk", "example.co.uk"},
		{"https://app.project.github.io", "project.github.io"},
		{"https://desktop.unknownsuffix", "example.net"},
	} {
		if ValidateIsolation(tc.origin, tc.domain) == nil {
			t.Fatalf("shared or unknown cookie boundary accepted: %+v", tc)
		}
	}
	for _, tc := range []struct{ origin, domain string }{
		{"https://desktop.example.com", "example.net"},
		{"https://desktop.github.io", "github.io"},
		{"", "example.net"},
	} {
		if err := ValidateIsolation(tc.origin, tc.domain); err != nil {
			t.Fatalf("independent allocation rejected: %+v %v", tc, err)
		}
	}
}
