package policy

import (
	"strings"
	"testing"
)

func TestAccountValidation(t *testing.T) {
	for _, c := range []Account{{Domain: "localhost"}, {Domain: "apps.example.com/path"}, {APIToken: "secret\nvalue"}, {Listen: "0.0.0.0:3003"}, {Audience: "two words"}} {
		if ValidateAccount(c) == nil {
			t.Fatal("accepted invalid management configuration")
		}
	}
}

func TestCloudflareResourceIdentifiersMatchProviderClient(t *testing.T) {
	for _, identifier := range []string{"account/other", "cloud☁", "../account", "account?id=x", strings.Repeat("a", 129)} {
		for _, settings := range []Account{{AccountID: identifier}, {ZoneID: identifier}, {TunnelID: identifier}} {
			if ValidateAccount(settings) == nil {
				t.Fatalf("accepted unusable provider resource ID %q", identifier)
			}
		}
	}
	if err := ValidateAccount(Account{AccountID: "account-1_scoped", ZoneID: strings.Repeat("f", 32), TunnelID: "f70ff985-a4ef-4643-bbbc-4a0ed4fc8415"}); err != nil {
		t.Fatalf("rejected usable provider resource IDs: %v", err)
	}
}
