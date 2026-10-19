package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteConfigurationValidatesIdentityAndHostname(t *testing.T) {
	for _, value := range []Remote{
		{}, {Enabled: true}, {Enabled: true, Hostname: "axv-workstation", OwnerUserID: "123456"},
		{Hostname: strings.Repeat("a", 63), OwnerUserID: "9223372036854775807"},
	} {
		if err := ValidateRemote(value); err != nil {
			t.Fatalf("rejected valid remote configuration %+v: %v", value, err)
		}
	}
	for _, hostname := range []string{"AXV", "axv.local", "-axv", "axv-", "axv_name", " axv", "axv\n", "主机", strings.Repeat("a", 64)} {
		if err := ValidateRemote(Remote{Hostname: hostname}); err == nil {
			t.Fatalf("accepted invalid hostname %q while disabled", hostname)
		}
	}
	for _, owner := range []string{"0", "-1", "+1", "01", "1.0", " 1", "1\n", "alice@example.com", "9223372036854775808"} {
		if err := ValidateRemote(Remote{OwnerUserID: owner}); err == nil {
			t.Fatalf("accepted invalid owner %q while disabled", owner)
		}
	}
}

func TestInvalidRemoteProfileFailsDesktopWithoutBeingReplaced(t *testing.T) {
	for _, value := range []string{`42`, `{"enabled":"true"}`, `{"hostname":"bad.name"}`, `{"enabled":false,"owner_user_id":"01"}`, `{"owner_user_id":123}`} {
		t.Run(value, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "config.json")
			original := `{"runtime_version":1,"remote":` + value + `,"compute":{"mode":"disabled"},"future":{"preserve":true}}`
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadDesktop(home); err == nil {
				t.Fatal("desktop accepted invalid remote configuration")
			}
			if _, err := InitializeDesktop(home); err == nil {
				t.Fatal("initialization accepted invalid remote configuration")
			}
			p, err := ReadCompute(home)
			if err != nil || p.ComputeError != "" || p.Remote != (Remote{}) {
				t.Fatalf("remote configuration blocked independent VM administration: %+v, %v", p, err)
			}
			current, err := os.ReadFile(path)
			if err != nil || string(current) != original {
				t.Fatalf("invalid configuration was replaced: %v", err)
			}
		})
	}
}

func TestStarterRemoteIsDisabledAndComputeUpdatesPreserveIt(t *testing.T) {
	home := t.TempDir()
	p, err := InitializeDesktop(home)
	if err != nil || p.Remote != (Remote{Hostname: "axv"}) {
		t.Fatalf("starter remote configuration: %+v, %v", p.Remote, err)
	}
	path := filepath.Join(home, "config.json")
	original := `{"remote":{"enabled":true,"hostname":"axv-custom","owner_user_id":"123","future":true},"compute":{"mode":"disabled"}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveCompute(home, Compute{Mode: "disabled"}); err != nil {
		t.Fatal(err)
	}
	p, err = ReadDesktop(home)
	if err != nil || p.Remote != (Remote{Enabled: true, Hostname: "axv-custom", OwnerUserID: "123"}) {
		t.Fatalf("compute update lost remote configuration: %+v, %v", p.Remote, err)
	}
	current, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(current), `"future": true`) {
		t.Fatalf("compute update lost unknown remote fields: %s, %v", current, err)
	}
}
