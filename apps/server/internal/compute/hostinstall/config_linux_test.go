//go:build linux && amd64

package hostinstall

import "testing"

func TestGuestSubnet(t *testing.T) {
	for _, test := range []struct{ routes, want string }{
		{`[{"dst":"default"}]`, "172.30.0.0/22"},
		{`[{"dst":"172.30.0.4"}]`, "172.30.4.0/22"},
		{`[{"dst":"172.30.0.0/16","flags":["linkdown"]}]`, "172.16.0.0/22"},
		{`[{"dst":"172.16.0.0/12"},{"dst":"192.168.0.0/16"}]`, "10.0.0.0/22"},
		{`[{"dst":"172.16.0.0/12"},{"dst":"10.0.0.0/8"},{"dst":"192.168.0.0/16"}]`, ""},
		{`[{"dst":"broken"}]`, ""},
	} {
		got, err := guestSubnet([]byte(test.routes))
		if test.want == "" && err != nil {
			continue
		}
		if err != nil || got.String() != test.want {
			t.Errorf("guestSubnet(%s) = %v, %v; want %q", test.routes, got, err, test.want)
		}
	}
}

func TestJailIDsAvoidAssignedUsersAndDelegatedRanges(t *testing.T) {
	if base, err := jailIDBase([]idRange{{60001, 60001}, {60110, 60250}}); err != nil || base != 60303 {
		t.Fatalf("first free range: %d, %v", base, err)
	}
	if base, err := jailIDBase([]idRange{{50000, 65000}}); err == nil {
		t.Fatalf("allocated overlapping IDs at %d", base)
	}
}

func TestSubordinateIDRangeBounds(t *testing.T) {
	for _, input := range []string{
		"operator:4294967294:2",
		"operator:4294967295:1",
		"operator:1:0",
		"operator:-1:3",
		"operator:1:9223372036854775807",
		"operator:1:18446744073709551616",
	} {
		if got, ok := subordinateIDRange(input); ok {
			t.Errorf("subordinateIDRange(%q) accepted %v", input, got)
		}
	}
	const entry = "operator:4294967293:2"
	if got, ok := subordinateIDRange(entry); !ok || got != (idRange{4294967293, 4294967294}) {
		t.Errorf("subordinateIDRange(%q) = %v, %t", entry, got, ok)
	}
}
