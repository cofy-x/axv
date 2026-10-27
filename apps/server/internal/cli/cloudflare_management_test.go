package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/publish"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/spf13/cobra"
)

func TestCloudflareJSONOutputKeepsExactCounters(t *testing.T) {
	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	input := json.RawMessage(`{"requests":9007199254740993,"bytes":18446744073709551615}`)
	if err := printCloudflareJSON(cmd, input); err != nil {
		t.Fatal(err)
	}
	var result struct{ Requests, Bytes uint64 }
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Requests != 9007199254740993 || result.Bytes != 18446744073709551615 {
		t.Fatalf("Cloudflare JSON output rounded counters: %s, error: %v", output.String(), err)
	}
	if err := printCloudflareJSON(cmd, append(input, []byte(` {}`)...)); err == nil {
		t.Fatal("Cloudflare JSON output accepted trailing data")
	}
}

func cloudflareCommandFixture(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	home := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runtime", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": "desktop-fixture", "launchId": "cloudflare-cli"})
	})
	mux.HandleFunc("/", handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	if err := os.MkdirAll(filepath.Join(home, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.Write(filepath.Join(home, "run", "desktop.json"), launch.State{InstanceID: "desktop-fixture", LaunchID: "cloudflare-cli", URL: server.URL}, 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCloudflareCLIReadCommandsUseVerifiedDesktopAndNoProfileWrites(t *testing.T) {
	var paths []string
	home := cloudflareCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.Method != "GET" {
			t.Errorf("read command mutated: %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "healthy", "supported": false, "reason": "fixture"})
	})
	t.Setenv("AXV_HOME", home)
	for _, args := range [][]string{{"cloudflare", "check", "--force"}, {"cloudflare", "traffic", "--hours", "168"}, {"cloudflare", "config"}, {"cloudflare", "publications"}} {
		var output bytes.Buffer
		if err := Execute(context.Background(), args, nil, &output, nil, "dev"); err != nil || !json.Valid(output.Bytes()) {
			t.Fatalf("%v: %s %v", args, output.String(), err)
		}
	}
	if strings.Join(paths, ",") != "/api/cloudflare/health?force=true,/api/cloudflare/traffic?hours=168,/api/cloudflare/config,/api/cloudflare-publications" {
		t.Fatalf("unexpected calls: %v", paths)
	}
	if _, err := os.Stat(filepath.Join(home, "config.json")); !os.IsNotExist(err) {
		t.Fatal("read commands created profile configuration")
	}
}

func TestCloudflareCLIConfigureCapturesRevisionAndNeverPrintsInputToken(t *testing.T) {
	var saved atomic.Bool
	home := cloudflareCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cloudflare/config" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method == "PUT" {
			var input map[string]any
			if json.NewDecoder(r.Body).Decode(&input) != nil || input["instanceId"] != "desktop-fixture" || input["expectedRevision"] != "reviewed-revision" || input["token"] != "private-cli-token" || r.Header.Get("Origin") == "" {
				t.Error("configuration did not retain its original review and local Origin")
			}
			saved.Store(true)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"instanceId": "desktop-fixture", "revision": "reviewed-revision", "account": map[string]any{"hasToken": true}})
	})
	t.Setenv("AXV_HOME", home)
	var output bytes.Buffer
	input := strings.NewReader(`{"access":{},"account":{},"tokenAction":"replace","token":"private-cli-token"}`)
	if err := Execute(context.Background(), []string{"cloudflare", "configure", "--file", "-"}, input, &output, nil, "dev"); err != nil || !saved.Load() || strings.Contains(output.String(), "private-cli-token") {
		t.Fatalf("configure: %s %v", output.String(), err)
	}
}

func TestCloudflareCLIUnpublishAddressesOriginalRecordAndHost(t *testing.T) {
	var revoked atomic.Bool
	home := cloudflareCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]any{"instanceId": "publications-original", "publications": []map[string]any{{"id": "original-publication", "binding": map[string]any{"hostId": "original-host"}}}})
			return
		}
		if r.URL.Path != "/api/cloudflare-publications/original-publication/unpublish" || r.Header.Get("X-AXV-Host-ID") != "original-host" {
			t.Error("unpublish changed the target")
		}
		revoked.Store(true)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "original-publication", "state": "removing"})
	})
	t.Setenv("AXV_HOME", home)
	var output bytes.Buffer
	if err := Execute(context.Background(), []string{"cloudflare", "unpublish", "original-publication"}, nil, &output, nil, "dev"); err != nil || !revoked.Load() {
		t.Fatalf("unpublish: %s %v", output.String(), err)
	}
}

func TestCloudflareCLIRejectsUnverifiedDesktopAndUnsafePublishRecovery(t *testing.T) {
	var contacted atomic.Bool
	home := localStatusFixture(t, "other-instance", "/api/cloudflare/config", func(http.ResponseWriter, *http.Request) { contacted.Store(true) })
	t.Setenv("AXV_HOME", home)
	if _, err := desktopCloudflareCall(context.Background(), "GET", "/api/cloudflare/config", nil, ""); err == nil || contacted.Load() {
		t.Fatal("contacted an unverified desktop")
	}
	for _, args := range [][]string{{"cloudflare", "publish", "project", "--port", "80", "--name", "app"}, {"cloudflare", "publish", "project", "--port", "3000", "--name", "app", "--request-id", "original"}, {"cloudflare", "publish", "project", "--port", "3000", "--name", "app", "--visibility", "implicit"}} {
		if err := Execute(context.Background(), args, nil, nil, nil, "dev"); err == nil || ExitCode(err) != 2 {
			t.Fatalf("accepted ambiguous publication command: %v %v", args, err)
		}
	}
}

func TestCloudflareCLIPublishKeepsOriginalReceiptAndRequestsAFreshHostname(t *testing.T) {
	file, original := projectBindingFixture(t)
	var submitted atomic.Bool
	home := cloudflareCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Retained receipt recovery remains available after account settings
			// are cleared; only a new publication needs the current domain.
			_ = json.NewEncoder(w).Encode(map[string]any{"instanceId": "original-publications", "supported": false})
			return
		}
		var request publish.Registration
		if r.URL.Path != "/api/cloudflare-publications" || r.Header.Get("X-AXV-Host-ID") != original.Binding.HostID || json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Errorf("publication did not use its original desktop and host")
		}
		if request.Name != "editor" || request.Domain != "example.net" || request.Binding != original.Binding || request.RequestID != "publication-original" || request.InstanceID != "original-publications" || request.Port != 3000 || request.Visibility != "owner" {
			t.Errorf("publication changed its reviewed payload: %+v", request)
		}
		submitted.Store(true)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "publication", "name": "editor", "generation": strings.Repeat("a", 32), "domain": "example.net", "hostname": "editor-" + strings.Repeat("a", 32) + ".example.net"})
	})
	t.Setenv("AXV_HOME", home)
	var output bytes.Buffer
	err := Execute(context.Background(), []string{"cloudflare", "publish", original.Project.ID, "--port", "3000", "--name", "editor", "--domain", "example.net", "--binding-file", file, "--request-id", "publication-original", "--instance-id", "original-publications"}, nil, &output, nil, "dev")
	if err != nil || !submitted.Load() || !strings.Contains(output.String(), "editor-"+strings.Repeat("a", 32)+".example.net") {
		t.Fatalf("publication receipt failed: %s %v", output.String(), err)
	}
}

func TestCloudflareCLIPublishRecoveryCannotFollowAReplacementJournal(t *testing.T) {
	file, original := projectBindingFixture(t)
	var mutated atomic.Bool
	home := cloudflareCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutated.Store(true)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"instanceId": "replacement-journal", "domain": "example.net", "supported": true})
	})
	t.Setenv("AXV_HOME", home)
	err := Execute(context.Background(), []string{"cloudflare", "publish", original.Project.ID, "--port", "3000", "--name", "editor", "--domain", "example.net", "--binding-file", file, "--request-id", "publication-original", "--instance-id", "original-journal"}, nil, nil, nil, "dev")
	if err == nil || !strings.Contains(err.Error(), "not redirected") || mutated.Load() {
		t.Fatalf("recovery selected a replacement journal: %v, mutated=%v", err, mutated.Load())
	}
}
