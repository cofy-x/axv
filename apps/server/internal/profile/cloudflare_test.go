package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func completeCloudflare() Cloudflare {
	return Cloudflare{Enabled: true, Listen: DefaultCloudflareListen, Origin: "https://axv.example.com", Issuer: "https://my-team.cloudflareaccess.com", Audience: "application-audience", OwnerSubject: "owner-stable-subject"}
}

func TestCloudflareConfigurationRequiresAnExplicitPublicOriginAndIdentity(t *testing.T) {
	valid := completeCloudflare()
	for _, c := range []Cloudflare{{}, {Origin: valid.Origin}, valid, {Listen: "[::1]:4002"}} {
		if err := ValidateCloudflare(c); err != nil {
			t.Fatalf("rejected valid configuration %+v: %v", c, err)
		}
	}
	for _, c := range []Cloudflare{
		{Enabled: true}, {Enabled: true, Origin: valid.Origin, Issuer: valid.Issuer, Audience: valid.Audience},
		{Listen: "0.0.0.0:3002"}, {Listen: "localhost:3002"}, {Listen: "127.0.0.1:0"}, {Listen: "[::]:3002"}, {Listen: "127.0.0.1:65536"},
		{Audience: "audience secret"}, {Audience: strings.Repeat("a", 257)}, {OwnerSubject: "owner\nprivate"}, {OwnerSubject: "owner\u00a0subject"},
		{Issuer: "http://my-team.cloudflareaccess.com"}, {Issuer: "https://nested.my-team.cloudflareaccess.com"}, {Issuer: "https://my-team.cloudflareaccess.com/"},
		{Issuer: "https://my-team.cloudflareaccess.com.attacker.com"}, {Issuer: "https://my-team.cloudflareaccess.com:443"},
	} {
		if err := ValidateCloudflare(c); err == nil {
			t.Fatalf("accepted invalid configuration %+v", c)
		}
	}
	for _, origin := range []string{
		"http://axv.example.com", "https://localhost", "https://preview.localhost", "https://127.0.0.1", "https://[::1]", "https://axv.internal", "https://axv.local",
		"https://AXV.example.com", "https://axv", "https://axv.999999999999999999999999", "https://axv.example.com:443", "https://axv.example.com/", "https://private@axv.example.com",
		"https://axv.example.com?", "https://axv.example.com?token=private", "https://axv.example.com#private", "https://axv_.example.com", "https://-axv.example.com",
	} {
		if err := ValidateCloudflare(Cloudflare{Origin: origin}); err == nil {
			t.Fatalf("accepted unsafe origin %q while disabled", origin)
		}
	}
}

func TestCloudflareReadIsIndependentAndDoesNotBlockOtherOwners(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	settings := completeCloudflare()
	encoded, _ := json.Marshal(settings)
	if err := os.WriteFile(path, []byte(`{"desktop":42,"remote":42,"compute":42,"proxy_url":false,"cloudflare":`+string(encoded)+`}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCloudflare(home); err != nil || got != settings {
		t.Fatalf("other owners blocked Cloudflare: %+v %v", got, err)
	}
	for _, raw := range []string{`null`, `42`, `{"enabled":"true"}`, `{"enabled":true}`, `{"listen":"0.0.0.0:3002"}`, `{"origin":"https://localhost"}`} {
		original := `{"runtime_version":1,"remote":{"hostname":"axv"},"compute":{"mode":"disabled"},"cloudflare":` + raw + `,"future":true}`
		if err := os.WriteFile(path, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadCloudflare(home); err == nil {
			t.Fatalf("invalid Cloudflare configuration accepted: %s", raw)
		}
		for _, read := range []func(string) (Profile, error){Read, ReadDesktop, ReadCompute, InitializeDesktop} {
			if _, err := read(home); err != nil {
				t.Fatalf("Cloudflare blocked local or VM work: %s: %v", raw, err)
			}
		}
		if _, err := ReadRemote(home); err != nil {
			t.Fatalf("Cloudflare blocked Tailscale: %v", err)
		}
		p, err := ReadDesktop(home)
		if err != nil || p.CloudflareError == "" {
			t.Fatalf("desktop lost the separate configuration error: %+v %v", p, err)
		}
		if err := SaveCompute(home, Compute{Mode: "disabled"}); err != nil {
			t.Fatalf("Cloudflare blocked compute configuration: %v", err)
		}
		var values map[string]json.RawMessage
		data, _ := os.ReadFile(path)
		if json.Unmarshal(data, &values) != nil || string(values["cloudflare"]) != raw {
			// JSON object whitespace may change when the containing profile is saved.
			var got, want any
			if json.Unmarshal(values["cloudflare"], &got) != nil || json.Unmarshal([]byte(raw), &want) != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("another owner replaced invalid Cloudflare settings: %s", data)
			}
		}
	}
}

func TestCloudflareSwitchPreservesOtherOwnersAndUnknownFields(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	settings := completeCloudflare()
	settings.Enabled = false
	fields, _ := json.Marshal(settings)
	original := `{"runtime_version":1,"desktop":42,"remote":null,"compute":42,"future":{"keep":[1,2]},"cloudflare":` + strings.TrimSuffix(string(fields), "}") + `,"future":{"private":"preserve"}}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if err := json.Unmarshal([]byte(original), &expected); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false, false} {
		lock, err := Lock(home)
		if err != nil {
			t.Fatal(err)
		}
		got, err := SaveCloudflareEnabled(home, enabled)
		closeErr := lock.Close()
		settings.Enabled = enabled
		if err != nil || closeErr != nil || got != settings {
			t.Fatalf("switch failed: %+v %v %v", got, err, closeErr)
		}
		expected["cloudflare"].(map[string]any)["enabled"] = enabled
		data, err := os.ReadFile(path)
		var actual map[string]any
		if err != nil || json.Unmarshal(data, &actual) != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("switch overwrote other configuration: %s %v", data, err)
		}
		info, err := os.Stat(path)
		if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal("switch lost private profile permissions")
		}
	}
}

func TestCloudflareMissingAndInvalidProfilesAreNotInitializedOrReplaced(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	if got, err := ReadCloudflare(home); err != nil || got != (Cloudflare{Listen: DefaultCloudflareListen}) {
		t.Fatalf("default configuration: %+v %v", got, err)
	}
	if _, err := SaveCloudflareEnabled(home, false); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveCloudflareEnabled(home, true); err == nil {
		t.Fatal("unconfigured enable accepted")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("disabled or failed enable initialized profile state")
	}
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.json")
	for _, original := range []string{`{"cloudflare":null}`, `{"cloudflare":42}`, `{"cloudflare":{"enabled":"true"}}`, `{"cloudflare":{"listen":"0.0.0.0:3002"}}`, `{"runtime_version":99}`, `{} {}`} {
		if err := os.WriteFile(path, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := SaveCloudflareEnabled(home, false); err == nil {
			t.Fatalf("malformed profile replaced: %s", original)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != original {
			t.Fatalf("failed switch changed profile: %s %v", data, err)
		}
	}
	// An incomplete enabled profile can still be explicitly disabled without
	// inventing identity settings or discarding its draft configuration.
	if err := os.WriteFile(path, []byte(`{"cloudflare":{"enabled":true,"origin":"https://axv.example.com"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveCloudflareEnabled(home, false); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCloudflare(home); err != nil || got.Enabled || got.Origin != "https://axv.example.com" {
		t.Fatalf("incomplete configuration could not be disabled safely: %+v %v", got, err)
	}
}
