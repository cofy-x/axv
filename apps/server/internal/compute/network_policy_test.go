package compute

import (
	"net/netip"
	"testing"
)

func TestPublicIPv4ResolverPolicy(t *testing.T) {
	for _, address := range []string{"1.1.1.1", "8.8.8.8", "9.9.9.9", "223.5.5.5", "100.63.255.255", "100.128.0.1", "192.0.0.9", "192.0.0.10"} {
		if !IsPublicIPv4(netip.MustParseAddr(address)) {
			t.Errorf("public IPv4 resolver %s rejected", address)
		}
	}
	for _, address := range []string{"0.1.2.3", "10.0.0.53", "100.64.0.53", "100.127.255.255", "127.0.0.1", "169.254.1.53", "172.16.0.53", "192.0.0.8", "192.0.0.11", "192.0.0.170", "192.0.2.53", "192.168.1.53", "198.18.0.53", "198.51.100.53", "203.0.113.53", "224.0.0.53", "240.0.0.53", "255.255.255.255", "::1", "::ffff:1.1.1.1", "2606:4700:4700::1111"} {
		if IsPublicIPv4(netip.MustParseAddr(address)) {
			t.Errorf("nonpublic IPv4 resolver %s accepted", address)
		}
	}
	if IsPublicIPv4(netip.Addr{}) {
		t.Fatal("invalid address accepted")
	}
}

func TestPublicIPv4PolicyAccessorsPreserveOwnership(t *testing.T) {
	prefixes, exceptions := NonPublicIPv4Prefixes(), PublicIPv4Exceptions()
	prefixes[0] = netip.MustParsePrefix("1.0.0.0/8")
	exceptions[0] = netip.MustParseAddr("0.1.2.3")
	if IsPublicIPv4(netip.MustParseAddr("0.1.2.3")) || !IsPublicIPv4(netip.MustParseAddr("1.1.1.1")) || !IsPublicIPv4(netip.MustParseAddr("192.0.0.9")) {
		t.Fatal("a provider modified the shared public-address policy")
	}
	blocked := false
	for _, prefix := range NonPublicIPv4Prefixes() {
		blocked = blocked || prefix.Contains(netip.MustParseAddr("224.0.0.1"))
	}
	if !blocked {
		t.Fatal("firewall policy omitted multicast destinations")
	}
}
