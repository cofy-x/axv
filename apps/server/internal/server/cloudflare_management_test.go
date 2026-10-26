package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
	cfhttpapi "github.com/cofy-x/axv/apps/server/internal/cloudflare/httpapi"
	cfmanagement "github.com/cofy-x/axv/apps/server/internal/cloudflare/management"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type cloudflareFixtureTransport func(*http.Request) (*http.Response, error)

func (f cloudflareFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func cloudflareManagementFixture(t *testing.T) (*cloudflareManagementFixtureState, http.Handler, *atomic.Int64) {
	t.Helper()
	c, _, _ := cloudflareControllerFixture(t)
	m := &cloudflareManagementFixtureState{home: c.home, instanceID: "desktop-management", desktop: c, busy: func() bool { return false }}
	calls := &atomic.Int64{}
	m.client = func(token string) (*cfclient.Client, error) {
		return cfclient.New(cfclient.Options{Token: token, HTTPClient: &http.Client{Transport: cloudflareFixtureTransport(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			value := `{"success":true,"result":[]}`
			switch r.URL.Path {
			case "/client/v4/user/tokens/verify":
				value = `{"success":true,"result":{"id":"token-id","status":"active"}}`
			case "/client/v4/accounts/account/cfd_tunnel/tunnel":
				value = `{"success":true,"result":{"id":"tunnel","name":"Fixture tunnel","status":"healthy","remote_config":true}}`
			case "/client/v4/zones/zone":
				value = `{"success":true,"result":{"id":"zone","name":"example.net","account":{"id":"account"}}}`
			case "/client/v4/graphql":
				value = `{"data":{"viewer":{"zones":[{"series":[]}]}}}`
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(value)), Request: r}, nil
		})}})
	}
	m.Service = cfmanagement.New(c.ctx, cfmanagement.Options{Home: m.home, InstanceID: m.instanceID, Desktop: c, Busy: func() bool { return m.busy() }, ValidateDesktopOrigin: func(origin string) error {
		if m.validateDesktopOrigin != nil {
			return m.validateDesktopOrigin(origin)
		}
		return nil
	}, Client: func(token string) (*cfclient.Client, error) { return m.client(token) }})
	t.Cleanup(m.Close)
	mux := http.NewServeMux()
	cfhttpapi.New(m.Service).Register(mux)
	return m, mux, calls
}

func managementRequest(handler http.Handler, method, path, body string, remote bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:3001"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "http://localhost:3001")
	r.Header.Set("Content-Type", "application/json")
	if remote {
		r.Host = "axv.example.com"
		r.Header.Set("Origin", "https://axv.example.com")
		var err error
		r, err = localaccess.WithAuthenticatedTunnel(r, "https://axv.example.com")
		if err != nil {
			panic(err)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestCloudflareManagementLocalConfigDoesNotExposeCredentials(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	c, revision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		t.Fatal(err)
	}
	c.Management = policy.Account{APIToken: "private-api-token", AccountID: "account", ZoneID: "zone", TunnelID: "tunnel", Domain: "example.net", Listen: policy.DefaultPublishListen}
	if _, _, err := profile.SaveCloudflareConfiguration(m.home, revision, c); err != nil {
		t.Fatal(err)
	}
	w := managementRequest(handler, "GET", "/api/cloudflare/config", "", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-api-token") || !strings.Contains(w.Body.String(), `"hasToken":true`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unsafe config view: %d %s", w.Code, w.Body.String())
	}
	for _, entry := range []struct{ method, path, body string }{{"GET", "/api/cloudflare/config", ""}, {"PUT", "/api/cloudflare/config", `{}`}, {"POST", "/api/cloudflare/wizard", `{"step":"token"}`}, {"PUT", "/api/cloudflare/token", `{}`}} {
		w := managementRequest(handler, entry.method, entry.path, entry.body, true)
		if w.Code != 403 || strings.Contains(w.Body.String(), "private-api-token") || strings.Contains(w.Body.String(), "private-owner") {
			t.Fatalf("remote accessed configuration: %d %s", w.Code, w.Body.String())
		}
	}
	w = managementRequest(handler, "GET", "/api/cloudflare/capabilities", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configure":false`) || !strings.Contains(w.Body.String(), `"publish":true`) {
		t.Fatalf("remote capabilities: %d %s", w.Code, w.Body.String())
	}
}

func TestCloudflareManagementSaveChecksRevisionAndLiveOwners(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	w := managementRequest(handler, "GET", "/api/cloudflare/config", "", false)
	var view cfmanagement.ConfigView
	if json.Unmarshal(w.Body.Bytes(), &view) != nil {
		t.Fatal(w.Body.String())
	}
	payload := map[string]any{"instanceId": view.InstanceID, "expectedRevision": view.Revision, "access": map[string]any{"listen": view.Access.Listen, "origin": "https://new.example.com", "issuer": view.Access.Issuer, "audience": view.Access.Audience, "ownerSubject": view.Access.OwnerSubject}, "account": map[string]any{"listen": view.Account.Listen}, "tokenAction": "keep"}
	data, _ := json.Marshal(payload)
	m.busy = func() bool { return true }
	if w := managementRequest(handler, "PUT", "/api/cloudflare/config", string(data), false); w.Code != 409 {
		t.Fatalf("replaced active publication settings: %d %s", w.Code, w.Body.String())
	}
	m.busy = func() bool { return false }
	w = managementRequest(handler, "PUT", "/api/cloudflare/config", string(data), false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://new.example.com") {
		t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
	}
	before, _ := os.ReadFile(filepath.Join(m.home, "config.json"))
	w = managementRequest(handler, "PUT", "/api/cloudflare/config", string(data), false)
	after, _ := os.ReadFile(filepath.Join(m.home, "config.json"))
	if w.Code != 409 || string(before) != string(after) {
		t.Fatalf("stale save replaced configuration: %d %s", w.Code, w.Body.String())
	}
	cloudflareSwitch(t, m.desktop, true)
	if w := managementRequest(handler, "PUT", "/api/cloudflare/config", string(data), false); w.Code != 409 || !strings.Contains(w.Body.String(), "cloudflare_disable_required") {
		t.Fatalf("changed live identity: %d %s", w.Code, w.Body.String())
	}
}

func TestCloudflareWizardRejectsAmbiguousInputAndKeepsSecretsOutOfReplies(t *testing.T) {
	_, handler, calls := cloudflareManagementFixture(t)
	for _, body := range []string{`null`, `[]`, `{"step":"token","token":"private-api-token","unknown":true}`, `{"step":"token"} {}`, `{"step":"access","access":null}`, `{"step":"access","access":{"origin":"https://bad\ud800.example.com"}}`} {
		w := managementRequest(handler, "POST", "/api/cloudflare/wizard", body, false)
		if w.Code != 400 || strings.Contains(w.Body.String(), "private-api-token") {
			t.Fatalf("accepted invalid request: %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request contacted Cloudflare")
	}
	w := managementRequest(handler, "POST", "/api/cloudflare/wizard", `{"step":"token","token":"private-api-token"}`, false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-api-token") || calls.Load() != 2 {
		t.Fatalf("token validation: %d %s", w.Code, w.Body.String())
	}
	w = managementRequest(handler, "POST", "/api/cloudflare/wizard", `{"step":"domain","token":"private-api-token","accountId":"account","zoneId":"zone","domain":"attacker.example.org"}`, false)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "cloudflare_domain_mismatch") {
		t.Fatalf("accepted unrelated domain: %d %s", w.Code, w.Body.String())
	}
}

func TestCloudflareWizardRejectsSharedCookieSitesBeforeDNSChecks(t *testing.T) {
	_, handler, calls := cloudflareManagementFixture(t)
	for _, body := range []string{
		`{"step":"domain","token":"private-api-token","accountId":"account","zoneId":"zone","domain":"apps.example.net"}`,
		`{"step":"domain","token":"private-api-token","accountId":"account","zoneId":"zone","domain":"example.net","access":{"origin":"https://desktop.example.net"}}`,
	} {
		before := calls.Load()
		w := managementRequest(handler, "POST", "/api/cloudflare/wizard", body, false)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "cloudflare_domain_isolation") || strings.Contains(w.Body.String(), "private-api-token") || calls.Load() != before+1 {
			t.Fatalf("shared browser site passed domain validation: %d %s", w.Code, w.Body.String())
		}
	}
	w := managementRequest(handler, "POST", "/api/cloudflare/wizard", `{"step":"domain","token":"private-api-token","accountId":"account","zoneId":"zone","domain":"example.net","access":{"origin":"https://desktop.example.com"}}`, false)
	if w.Code != 200 {
		t.Fatalf("independent registrable domain was rejected: %d %s", w.Code, w.Body.String())
	}
}

func TestCloudflareConfigurationCannotMoveDesktopIntoRetiredPublicationSite(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	w := managementRequest(handler, "GET", "/api/cloudflare/config", "", false)
	var view cfmanagement.ConfigView
	if json.Unmarshal(w.Body.Bytes(), &view) != nil {
		t.Fatal(w.Body.String())
	}
	m.validateDesktopOrigin = func(origin string) error {
		if origin != "https://desktop.example.net" {
			t.Fatalf("historical site guard received a different desktop: %s", origin)
		}
		return errors.New("This site belongs to a retained publication.")
	}
	payload := map[string]any{"instanceId": view.InstanceID, "expectedRevision": view.Revision, "access": map[string]any{"listen": view.Access.Listen, "origin": "https://desktop.example.net", "issuer": view.Access.Issuer, "audience": view.Access.Audience, "ownerSubject": view.Access.OwnerSubject}, "account": map[string]any{"listen": view.Account.Listen}, "tokenAction": "keep"}
	data, _ := json.Marshal(payload)
	before, err := os.ReadFile(filepath.Join(m.home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	w = managementRequest(handler, "PUT", "/api/cloudflare/config", string(data), false)
	after, err := os.ReadFile(filepath.Join(m.home, "config.json"))
	if err != nil || w.Code != 409 || !strings.Contains(w.Body.String(), "cloudflare_desktop_site_reserved") || string(before) != string(after) {
		t.Fatalf("desktop reconfiguration bypassed historical publication sites: %d %s", w.Code, w.Body.String())
	}
}

func TestCloudflarePublishingKeepsTheActuallyBoundDesktopSiteAfterProfileDrift(t *testing.T) {
	for _, failedClose := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed-close-%t", failedClose), func(t *testing.T) {
			desktop, services, _ := cloudflareControllerFixture(t)
			cloudflareSwitch(t, desktop, true)
			c, _, err := profile.ReadCloudflareConfiguration(desktop.home)
			if err != nil {
				t.Fatal(err)
			}
			c.Origin = "https://desktop.example.org"
			c.Management.Domain = "example.com"
			data, err := json.Marshal(map[string]any{"cloudflare": c})
			if err != nil {
				t.Fatal(err)
			}
			// Simulate an administrator's direct file edit. The configuration API
			// correctly rejects this change while desktop access is enabled.
			if err = os.WriteFile(filepath.Join(desktop.home, "config.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = cloudflarePublicationSettings(desktop.home, desktop); err == nil {
				t.Fatal("a profile edit gave a publication the running desktop's cookie site")
			}
			if failedClose {
				(*services)[0].close = func() error { return errors.New("fixture close failed") }
				if err = desktop.SetEnabled(context.Background(), false); err == nil {
					t.Fatal("the simulated close failure was hidden")
				}
				if _, err = cloudflarePublicationSettings(desktop.home, desktop); err == nil {
					t.Fatal("a failed close released the still-bound desktop cookie site")
				}
				return
			}
			cloudflareSwitch(t, desktop, false)
			if _, err = cloudflarePublicationSettings(desktop.home, desktop); err != nil {
				t.Fatalf("a successfully closed entrypoint retained an unbound site: %v", err)
			}
		})
	}
}

func TestCloudflareHealthCachesBySettingsAndSeparatesCloudFromLocalListening(t *testing.T) {
	m, handler, calls := cloudflareManagementFixture(t)
	c, revision, _ := profile.ReadCloudflareConfiguration(m.home)
	c.Management = policy.Account{APIToken: "private-api-token", AccountID: "account", ZoneID: "zone", TunnelID: "tunnel", Domain: "example.net", Listen: policy.DefaultPublishListen}
	if _, _, err := profile.SaveCloudflareConfiguration(m.home, revision, c); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w := managementRequest(handler, "GET", "/api/cloudflare/health", "", true)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"healthy"`) || !strings.Contains(w.Body.String(), "reachability has not been verified") || strings.Contains(w.Body.String(), "private-api-token") {
			t.Fatalf("health: %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("health was not cached: %d calls", calls.Load())
	}
	if m.desktop.Status().State != "disabled" {
		t.Fatal("health started the desktop entrypoint")
	}
	w := managementRequest(handler, "GET", "/api/cloudflare/health?force=true", "", true)
	if w.Code != 200 || calls.Load() != 6 {
		t.Fatalf("force health: %d calls", calls.Load())
	}
}

func TestCloudflareTokenRotationPreservesActiveDesktopAndPublicationSettings(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	cloudflareSwitch(t, m.desktop, true)
	_, revision, _ := profile.ReadCloudflareConfiguration(m.home)
	m.busy = func() bool { return true }
	data, _ := json.Marshal(map[string]string{"instanceId": m.instanceID, "expectedRevision": revision, "token": "new-private-api-token"})
	w := managementRequest(handler, "PUT", "/api/cloudflare/token", string(data), false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "new-private-api-token") {
		t.Fatalf("rotation: %d %s", w.Code, w.Body.String())
	}
	c, _, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil || !c.Enabled || c.Management.APIToken != "new-private-api-token" || m.desktop.Status().State != "listening" {
		t.Fatal("token rotation interrupted the active entrypoint")
	}
}

func TestCloudflareRemoteRoutePolicyOnlyOpensSafeAccountReads(t *testing.T) {
	for _, path := range []string{"/api/cloudflare/health", "/api/cloudflare/traffic", "/api/cloudflare/capabilities", "/api/cloudflare-publications"} {
		if !remoteRoute(httptest.NewRequest("GET", "https://axv.example.com"+path, nil)) {
			t.Fatalf("safe route excluded: %s", path)
		}
	}
	for _, path := range []string{"/api/cloudflare/config", "/api/cloudflare/token", "/api/cloudflare/wizard", "/api/cloudflare/health/unknown", "/api/cloudflare/status"} {
		if remoteRoute(httptest.NewRequest("GET", "https://axv.example.com"+path, nil)) {
			t.Fatalf("private route opened: %s", path)
		}
	}
	if remoteRoute(httptest.NewRequest("POST", "https://axv.example.com/api/cloudflare/health", nil)) {
		t.Fatal("health mutation opened remotely")
	}
}

func TestCloudflareTrafficUsesTheRequestedNumberOfCompleteHourlyBuckets(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	c, revision, _ := profile.ReadCloudflareConfiguration(m.home)
	c.Management = policy.Account{APIToken: "private-api-token", AccountID: "account", ZoneID: "zone", TunnelID: "tunnel", Domain: "example.net", Listen: policy.DefaultPublishListen}
	if _, _, err := profile.SaveCloudflareConfiguration(m.home, revision, c); err != nil {
		t.Fatal(err)
	}
	w := managementRequest(handler, "GET", "/api/cloudflare/traffic?hours=24", "", true)
	var result struct {
		Supported    bool `json:"supported"`
		Since, Until time.Time
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || !result.Supported || result.Until.Sub(result.Since) != 24*time.Hour || result.Since != result.Since.Truncate(time.Hour) || result.Until != result.Until.Truncate(time.Hour) {
		t.Fatalf("traffic window exceeds the requested provider interval: %d %s", w.Code, w.Body.String())
	}
}

func TestCloudflareTrafficPreservesExactProviderCounters(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	configureManagementAccount(t, m)
	const requests, bytes, cached uint64 = 9007199254740993, 18446744073709551615, 9007199254740991
	m.client = func(token string) (*cfclient.Client, error) {
		return cfclient.New(cfclient.Options{Token: token, HTTPClient: &http.Client{Transport: cloudflareFixtureTransport(func(r *http.Request) (*http.Response, error) {
			var query struct {
				Variables map[string]string `json:"variables"`
			}
			if r.URL.Path != "/client/v4/graphql" || json.NewDecoder(r.Body).Decode(&query) != nil {
				t.Fatal("unexpected traffic request")
			}
			body := fmt.Sprintf(`{"data":{"viewer":{"zones":[{"series":[{"dimensions":{"datetime":%q},"sum":{"requests":%d,"bytes":%d,"cachedRequests":%d}}]}]}}}`, query.Variables["since"], requests, bytes, cached)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}})
	}
	w := managementRequest(handler, "GET", "/api/cloudflare/traffic?hours=24", "", true)
	var result struct {
		Supported                       bool `json:"supported"`
		Requests, Bytes, CachedRequests uint64
		Series                          []cfclient.TrafficPoint
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || !result.Supported || result.Requests != requests || result.Bytes != bytes || result.CachedRequests != cached || len(result.Series) != 1 || result.Series[0].Requests != requests || result.Series[0].Bytes != bytes || result.Series[0].CachedRequests != cached {
		t.Fatalf("provider counters lost precision: HTTP %d: %s, error: %v", w.Code, w.Body.String(), err)
	}
}

func TestUnavailableCloudflareTrafficOmitsSuccessfulMetrics(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	for _, configured := range []bool{false, true} {
		if configured {
			configureManagementAccount(t, m)
			m.client = func(token string) (*cfclient.Client, error) {
				return cfclient.New(cfclient.Options{Token: token, HTTPClient: &http.Client{Transport: cloudflareFixtureTransport(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errors":[{"message":"not supported"}]}`)), Request: r}, nil
				})}})
			}
		}
		w := managementRequest(handler, "GET", "/api/cloudflare/traffic", "", true)
		var result map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || string(result["supported"]) != "false" || len(result["reason"]) == 0 || len(result["since"]) == 0 || len(result["until"]) == 0 {
			t.Fatalf("unavailable analytics lost its reason or window: %d %s", w.Code, w.Body.String())
		}
		for _, field := range []string{"requests", "bytes", "cachedRequests", "series", "scope", "dataset"} {
			if _, exists := result[field]; exists {
				t.Fatalf("unavailable analytics fabricated %s: %s", field, w.Body.String())
			}
		}
	}
}

func configureManagementAccount(t *testing.T, m *cloudflareManagementFixtureState) string {
	t.Helper()
	c, revision, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		t.Fatal(err)
	}
	c.Management = policy.Account{APIToken: "original-api-token", AccountID: "account", ZoneID: "zone", TunnelID: "tunnel", Domain: "example.net", Listen: policy.DefaultPublishListen}
	lock, err := profile.Lock(m.home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	_, next, err := profile.SaveCloudflareConfiguration(m.home, revision, c)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func managementContextRequest(ctx context.Context, handler http.Handler, method, path, body string) (*httptest.ResponseRecorder, <-chan struct{}) {
	r := httptest.NewRequest(method, "http://localhost:3001"+path, strings.NewReader(body)).WithContext(ctx)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "http://localhost:3001")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); handler.ServeHTTP(w, r) }()
	return w, done
}

func awaitManagement(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("management request did not finish after cancellation or release")
	}
}

func blockFirstManagementVerification(t *testing.T, m *cloudflareManagementFixtureState, block func(*http.Request)) <-chan struct{} {
	t.Helper()
	started := make(chan struct{})
	var first atomic.Bool
	m.client = func(token string) (*cfclient.Client, error) {
		return cfclient.New(cfclient.Options{Token: token, HTTPClient: &http.Client{Transport: cloudflareFixtureTransport(func(r *http.Request) (*http.Response, error) {
			value := `{"success":true,"result":[]}`
			switch r.URL.Path {
			case "/client/v4/user/tokens/verify":
				if first.CompareAndSwap(false, true) {
					close(started)
					block(r)
				}
				value = `{"success":true,"result":{"status":"active"}}`
			case "/client/v4/accounts/account/cfd_tunnel/tunnel":
				value = `{"success":true,"result":{"id":"tunnel","status":"healthy","config_src":"cloudflare"}}`
			}
			if err := r.Context().Err(); err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(value))}, nil
		})}})
	}
	return started
}

func TestCancelledHealthDoesNotPoisonCurrentCache(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	configureManagementAccount(t, m)
	started := blockFirstManagementVerification(t, m, func(r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, done := managementContextRequest(ctx, handler, "GET", "/api/cloudflare/health", "")
	awaitManagement(t, started)
	cancel()
	awaitManagement(t, done)
	w := managementRequest(handler, "GET", "/api/cloudflare/health", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"healthy"`) {
		t.Fatalf("cancelled health poisoned next check: %d %s", w.Code, w.Body.String())
	}
}

func TestHealthRejectsSettingsChangedDuringProviderRead(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	revision := configureManagementAccount(t, m)
	release := make(chan struct{})
	started := blockFirstManagementVerification(t, m, func(*http.Request) { <-release })
	w, done := managementContextRequest(context.Background(), handler, "GET", "/api/cloudflare/health", "")
	awaitManagement(t, started)
	lock, err := profile.Lock(m.home)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	_, _, err = profile.SaveCloudflareAPIToken(m.home, revision, "rotated-api-token")
	_ = lock.Close()
	close(release)
	awaitManagement(t, done)
	if err != nil || w.Code != 409 || !strings.Contains(w.Body.String(), "cloudflare_config_conflict") {
		t.Fatalf("published obsolete health: %d %s err=%v", w.Code, w.Body.String(), err)
	}
}

func TestManagementCloseCancelsOnlyItsProviderRequests(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	configureManagementAccount(t, m)
	started := blockFirstManagementVerification(t, m, func(r *http.Request) { <-r.Context().Done() })
	w, done := managementContextRequest(context.Background(), handler, "POST", "/api/cloudflare/wizard", `{"step":"token"}`)
	awaitManagement(t, started)
	m.Close()
	awaitManagement(t, done)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "cloudflare_stopping") || m.desktop.ctx.Err() != nil {
		t.Fatalf("management close failed or cancelled the desktop: %d %s", w.Code, w.Body.String())
	}
}

func TestCancelledSettingsAndTokenWaitersNeverWrite(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		m, handler, _ := cloudflareManagementFixture(t)
		c, revision, _ := profile.ReadCloudflareConfiguration(m.home)
		path := "/api/cloudflare/config"
		payload := map[string]any{"instanceId": m.instanceID, "expectedRevision": revision, "access": map[string]any{"listen": c.Listen}, "account": map[string]any{"listen": c.Management.Listen}, "tokenAction": "clear"}
		if rotate {
			path = "/api/cloudflare/token"
			payload = map[string]any{"instanceId": m.instanceID, "expectedRevision": revision, "token": "cancelled-token"}
		}
		body, _ := json.Marshal(payload)
		before, _ := os.ReadFile(filepath.Join(m.home, "config.json"))
		m.desktop.op.Lock()
		ctx, cancel := context.WithCancel(context.Background())
		_, done := managementContextRequest(ctx, handler, "PUT", path, string(body))
		cancel()
		m.desktop.op.Unlock()
		awaitManagement(t, done)
		after, _ := os.ReadFile(filepath.Join(m.home, "config.json"))
		if string(before) != string(after) {
			t.Fatal("cancelled mutation wrote configuration after acquiring the lock")
		}
	}
}

func TestCloudflareSaveRejectsUnusableProviderIDsWithoutWriting(t *testing.T) {
	m, handler, _ := cloudflareManagementFixture(t)
	c, revision, _ := profile.ReadCloudflareConfiguration(m.home)
	body, _ := json.Marshal(map[string]any{"instanceId": m.instanceID, "expectedRevision": revision,
		"access": map[string]any{"listen": c.Listen}, "account": map[string]any{"accountId": "account/other"}, "tokenAction": "keep"})
	before, _ := os.ReadFile(filepath.Join(m.home, "config.json"))
	w := managementRequest(handler, "PUT", "/api/cloudflare/config", string(body), false)
	after, _ := os.ReadFile(filepath.Join(m.home, "config.json"))
	if w.Code != 400 || string(before) != string(after) {
		t.Fatalf("invalid provider ID was saved: %d %s", w.Code, w.Body.String())
	}
}

// Keep provider and lifecycle controls in the integration fixture rather than
// exposing the management service's private cache or synchronization state.
type cloudflareManagementFixtureState struct {
	*cfmanagement.Service
	home, instanceID      string
	desktop               *cloudflareController
	busy                  func() bool
	validateDesktopOrigin func(string) error
	client                func(string) (*cfclient.Client, error)
}
