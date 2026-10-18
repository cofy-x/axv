//go:build linux || darwin

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute/artifacts"
)

func TestCacheReadOnlyCommandsDoNotCreateProfileLogs(t *testing.T) {
	home := filepath.Join(t.TempDir(), "profile-not-created")
	t.Setenv("AXV_HOME", home)
	t.Setenv("AXV_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	for _, args := range [][]string{{"cache", "list", "--json"}, {"cache", "verify"}, {"cache", "fetch", "--help"}} {
		var output bytes.Buffer
		if err := Run(args, &output, "test-version"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("read-only cache command initialized profile: %v", err)
		}
	}
}

func TestCacheImportRecordsFailureWithoutChangingCLIOutput(t *testing.T) {
	home := filepath.Join(t.TempDir(), "profile")
	t.Setenv("AXV_HOME", home)
	t.Setenv("AXV_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("AXV_ARTIFACT_MIRRORS", "")
	source := filepath.Join(t.TempDir(), "downloaded-image")
	payload := "fixture content must not enter an operational log"
	if err := os.WriteFile(source, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	artifact, ok := artifacts.Lookup("ubuntu-arm64")
	if !ok {
		t.Fatal("missing reviewed fixture artifact")
	}
	err := Run([]string{"cache", "import", "ubuntu-arm64", source, "--json"}, &output, "test-version")
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") || output.Len() != 0 {
		t.Fatalf("import failure/output: %v %q", err, output.String())
	}
	raw, err := os.ReadFile(filepath.Join(home, "logs", "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record["event"] == "artifact.cache.failed" && record["command"] == "import" && record["artifactId"] == artifact.ID && record["sha256"] == artifact.SHA256 {
			found = true
		}
	}
	if !found || bytes.Contains(raw, []byte(payload)) {
		t.Fatalf("missing failure event or leaked payload: %s", raw)
	}
	for _, path := range []string{"config.json", "data", "run"} {
		if _, err := os.Stat(filepath.Join(home, path)); !os.IsNotExist(err) {
			t.Fatalf("cache logging initialized unrelated desktop state: %s %v", path, err)
		}
	}
}
