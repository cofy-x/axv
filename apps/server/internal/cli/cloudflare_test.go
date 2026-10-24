package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCloudflareStatusIsReadOnlyAndDistinguishesLocalListener(t *testing.T) {
	home := localStatusFixture(t, "desktop-fixture", "/api/cloudflare/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"instanceId": "desktop-fixture", "enabled": true, "configured": true,
			"state": "listening", "url": "https://axv.example.com", "listen": "127.0.0.1:3002",
		})
	})
	t.Setenv("AXV_HOME", home)
	var output bytes.Buffer
	if err := Execute(context.Background(), []string{"cloudflare", "status"}, nil, &output, nil, "dev"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Cloudflare entrypoint: listening", "Configured: true", "127.0.0.1:3002", "Public tunnel connectivity has not been verified."} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q: %s", want, output.String())
		}
	}
	output.Reset()
	if err := Execute(context.Background(), []string{"cloudflare", "status", "--json"}, nil, &output, nil, "dev"); err != nil {
		t.Fatal(err)
	}
	var value cloudflareStatus
	if err := json.Unmarshal(output.Bytes(), &value); err != nil || value.Enabled == nil || !*value.Enabled || value.Configured == nil || !*value.Configured {
		t.Fatalf("status lost flags: %s %v", output.String(), err)
	}
	for _, name := range []string{"config.json", "cloudflare", "tailscale", "data"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatalf("status initialized %s: %v", name, err)
		}
	}
}

func TestCloudflareStatusRequiresVerifiedDesktopAndDoesNotCreateProfile(t *testing.T) {
	var contacted atomic.Bool
	home := localStatusFixture(t, "another-instance", "/api/cloudflare/status", func(w http.ResponseWriter, _ *http.Request) {
		contacted.Store(true)
	})
	if _, err := readCloudflareStatus(context.Background(), home); err == nil || contacted.Load() {
		t.Fatalf("queried an unverified desktop: %v", err)
	}
	home = filepath.Join(t.TempDir(), "absent")
	value, err := readCloudflareStatus(context.Background(), home)
	if err != nil || value.State != "stopped" {
		t.Fatalf("stopped status = %+v %v", value, err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("status created a profile: %v", err)
	}
}
