package profile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
)

func TestCloudflareConfigurationPreservesOwnersAndRejectsStaleSaves(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, []byte(`{"runtime_version":1,"future":{"keep":true},"compute":42,"cloudflare":{"enabled":false,"future":true,"management":{"future":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	settings, revision, err := ReadCloudflareConfiguration(home)
	if err != nil {
		t.Fatal(err)
	}
	settings.Origin = "https://axv.example.com"
	settings.Management = policy.Account{APIToken: "secret-token", AccountID: "account", ZoneID: "zone", TunnelID: "tunnel", Domain: "example.net", Listen: policy.DefaultPublishListen}
	lock, err := Lock(home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	_, next, err := SaveCloudflareConfiguration(home, revision, settings)
	if err != nil || next == revision {
		t.Fatalf("save: %s %v", next, err)
	}
	var raw map[string]any
	data, _ := os.ReadFile(path)
	if json.Unmarshal(data, &raw) != nil || raw["compute"] != float64(42) || raw["future"].(map[string]any)["keep"] != true {
		t.Fatalf("lost unowned settings: %s", data)
	}
	cf := raw["cloudflare"].(map[string]any)
	if cf["future"] != true || cf["management"].(map[string]any)["future"] != true {
		t.Fatalf("lost future cloud fields: %s", data)
	}
	settings.Origin = "https://new.example.com"
	if _, _, err := SaveCloudflareConfiguration(home, revision, settings); !errors.Is(err, ErrCloudflareRevision) {
		t.Fatalf("accepted stale config: %v", err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(data) {
		t.Fatal("stale save changed configuration")
	}
}

func TestCloudflareSettingsCannotReplaceEnabledIdentity(t *testing.T) {
	home := t.TempDir()
	settings := completeCloudflare()
	raw, _ := json.Marshal(map[string]any{"cloudflare": settings})
	if err := os.WriteFile(filepath.Join(home, "config.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, revision, err := ReadCloudflareConfiguration(home)
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = false
	if _, _, err := SaveCloudflareConfiguration(home, revision, settings); err == nil {
		t.Fatal("replaced enabled identity")
	}
}

func TestCloudflareManagementValidation(t *testing.T) {
	home := t.TempDir()
	for _, raw := range []string{`null`, `{"runtime_version":999}`, `{"cloudflare":null}`, `{"cloudflare":{"management":null}}`, `{"cloudflare":[]} {}`} {
		if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadCloudflareConfiguration(home); err == nil {
			t.Fatalf("accepted invalid settings: %s", raw)
		}
	}
}
