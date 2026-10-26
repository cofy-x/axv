package policy

import (
	"strings"
	"testing"
)

func completeCloudflare() Configuration {
	return Configuration{Enabled: true, Listen: DefaultDesktopListen, Origin: "https://axv.example.com", Issuer: "https://my-team.cloudflareaccess.com", Audience: "application-audience", OwnerSubject: "owner-stable-subject"}
}

func TestCloudflareConfigurationRequiresAnExplicitPublicOriginAndIdentity(t *testing.T) {
	valid := completeCloudflare()
	for _, c := range []Configuration{{}, {Origin: valid.Origin}, valid, {Listen: "[::1]:4002"}} {
		if err := Validate(c); err != nil {
			t.Fatalf("rejected valid configuration %+v: %v", c, err)
		}
	}
	for _, c := range []Configuration{
		{Enabled: true}, {Enabled: true, Origin: valid.Origin, Issuer: valid.Issuer, Audience: valid.Audience},
		{Listen: "0.0.0.0:3002"}, {Listen: "localhost:3002"}, {Listen: "127.0.0.1:0"}, {Listen: "[::]:3002"}, {Listen: "127.0.0.1:65536"},
		{Audience: "audience secret"}, {Audience: strings.Repeat("a", 257)}, {OwnerSubject: "owner\nprivate"}, {OwnerSubject: "owner\u00a0subject"},
		{Issuer: "http://my-team.cloudflareaccess.com"}, {Issuer: "https://nested.my-team.cloudflareaccess.com"}, {Issuer: "https://my-team.cloudflareaccess.com/"},
		{Issuer: "https://my-team.cloudflareaccess.com.attacker.com"}, {Issuer: "https://my-team.cloudflareaccess.com:443"},
	} {
		if err := Validate(c); err == nil {
			t.Fatalf("accepted invalid configuration %+v", c)
		}
	}
	for _, origin := range []string{
		"http://axv.example.com", "https://localhost", "https://preview.localhost", "https://127.0.0.1", "https://[::1]", "https://axv.internal", "https://axv.local",
		"https://AXV.example.com", "https://axv", "https://axv.999999999999999999999999", "https://axv.example.com:443", "https://axv.example.com/", "https://private@axv.example.com",
		"https://axv.example.com?", "https://axv.example.com?token=private", "https://axv.example.com#private", "https://axv_.example.com", "https://-axv.example.com",
	} {
		if err := Validate(Configuration{Origin: origin}); err == nil {
			t.Fatalf("accepted unsafe origin %q while disabled", origin)
		}
	}
}
