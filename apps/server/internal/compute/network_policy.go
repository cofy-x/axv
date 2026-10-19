package compute

import "net/netip"

// IsPublicIPv4 matches the guest image's public-resolver requirement and rejects
// multicast destinations blocked by provider isolation. Go's IsGlobalUnicast
// alone also accepts shared, documentation and other special-purpose networks.
func IsPublicIPv4(address netip.Addr) bool {
	if !address.Is4() || !address.IsGlobalUnicast() {
		return false
	}
	// These globally reachable anycast addresses are the exceptions within the
	// IANA IPv4 protocol-assignment block, also recognized by Python ipaddress.
	for _, exception := range publicIPv4Exceptions {
		if address == exception {
			return true
		}
	}
	for _, prefix := range nonPublicIPv4 {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

// NonPublicIPv4Prefixes returns independent policy data for provider firewalls.
func NonPublicIPv4Prefixes() []netip.Prefix {
	return append([]netip.Prefix(nil), nonPublicIPv4[:]...)
}

// PublicIPv4Exceptions returns globally reachable addresses inside denied blocks.
func PublicIPv4Exceptions() []netip.Addr {
	return append([]netip.Addr(nil), publicIPv4Exceptions[:]...)
}

var publicIPv4Exceptions = [...]netip.Addr{
	netip.AddrFrom4([4]byte{192, 0, 0, 9}),
	netip.AddrFrom4([4]byte{192, 0, 0, 10}),
}

var nonPublicIPv4 = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
}
