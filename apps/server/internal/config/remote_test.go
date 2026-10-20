package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestRemoteSettingsRemainOutsideCapturedDesktopConfiguration(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing")
	var baseline Config
	for _, remote := range []profile.Remote{{}, {Enabled: true}, {Enabled: true, Hostname: "axv-office", OwnerUserID: "123456"}} {
		p := profile.Profile{Home: home, Remote: remote}
		got, err := Resolve(Overrides{}, p, func(key string) string {
			switch key {
			case "TS_AUTHKEY", "TS_CLIENT_SECRET", "AXV_REMOTE_OWNER_USER_ID", "AXV_REMOTE_HOSTNAME":
				return "must-not-be-used"
			}
			return ""
		}, os.UserHomeDir)
		if err != nil {
			t.Fatal(err)
		}
		if baseline == (Config{}) {
			baseline = got
		}
		encoded, _ := json.Marshal(got)
		if got != baseline || strings.Contains(string(encoded), `"Remote"`) {
			t.Fatalf("mutable remote configuration entered captured desktop settings: %s", encoded)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("resolution created remote or profile state: %v", err)
	}
}

func TestRemoteResolutionRejectsMalformedExplicitFields(t *testing.T) {
	for _, remote := range []profile.Remote{{Hostname: "axv.example"}, {OwnerUserID: "01"}, {Enabled: true, OwnerUserID: "invalid"}} {
		if _, err := Resolve(Overrides{}, profile.Profile{Remote: remote}, func(string) string { return "" }, os.UserHomeDir); err == nil {
			t.Fatalf("accepted malformed remote configuration: %+v", remote)
		}
	}
}
