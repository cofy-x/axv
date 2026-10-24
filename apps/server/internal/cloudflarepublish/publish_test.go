package cloudflarepublish

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/cloudflareapi"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/projectpreview"
)

type fakeCloud struct {
	mu                                                       sync.Mutex
	dns                                                      map[string]cloudflareapi.DNSRecord
	configuration                                            map[string]any
	version                                                  int
	dnsCreates, ingressWrites                                int
	failDNSAfterCreate, failIngressAfterWrite, failDeleteDNS bool
	configurationReads                                       int
	injectIngressAtRead                                      int
	injectedRule                                             map[string]any
	injectForeignDNS                                         bool
	zoneName                                                 string
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{zoneName: "github.io", dns: map[string]cloudflareapi.DNSRecord{}, configuration: map[string]any{"ingress": []any{map[string]any{"hostname": "other.example.com", "service": "http://127.0.0.1:7777", "originRequest": map[string]any{"keep": "foreign"}}, map[string]any{"service": "http_status:404"}}, "originRequest": map[string]any{"keep": "global"}}}
}
func (f *fakeCloud) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	status := 200
	var result any = map[string]any{}
	switch {
	case path == "/zones/zone":
		result = cloudflareapi.Zone{ID: "zone", Name: f.zoneName}
	case path == "/zones/zone/dns_records" && r.Method == "GET":
		if f.injectForeignDNS {
			f.injectForeignDNS = false
			f.dns["foreign-record"] = cloudflareapi.DNSRecord{ID: "foreign-record", Type: "CNAME", Name: r.URL.Query().Get("name.exact"), Content: "foreign.example.com", Proxied: true, Comment: "belongs to another application"}
		}
		items := []cloudflareapi.DNSRecord{}
		for _, record := range f.dns {
			if record.Name == r.URL.Query().Get("name.exact") {
				items = append(items, record)
			}
		}
		result = items
	case path == "/zones/zone/dns_records" && r.Method == "POST":
		var record cloudflareapi.DNSRecord
		_ = json.NewDecoder(r.Body).Decode(&record)
		record.ID = "dns-" + strings.ReplaceAll(record.Name, ".", "-")
		f.dns[record.ID] = record
		f.dnsCreates++
		result = record
		if f.failDNSAfterCreate {
			f.failDNSAfterCreate = false
			status = 503
		}
	case strings.HasPrefix(path, "/zones/zone/dns_records/"):
		id := strings.TrimPrefix(path, "/zones/zone/dns_records/")
		record, ok := f.dns[id]
		if !ok {
			status = 404
		} else if r.Method == "DELETE" {
			if f.failDeleteDNS {
				status = 503
			} else {
				delete(f.dns, id)
			}
		} else {
			result = record
		}
	case path == "/accounts/account/cfd_tunnel/tunnel":
		result = cloudflareapi.Tunnel{ID: "tunnel", RemoteConfig: true, Status: "healthy"}
	case path == "/accounts/account/cfd_tunnel/tunnel/configurations":
		if r.Method == "GET" {
			f.configurationReads++
			if f.configurationReads == f.injectIngressAtRead {
				if f.injectedRule["hostname"] == "" {
					for _, dns := range f.dns {
						if strings.HasPrefix(dns.Comment, "AXV ") {
							f.injectedRule["hostname"] = dns.Name
							f.injectedRule["originRequest"].(map[string]any)["httpHostHeader"] = dns.Name
							break
						}
					}
				}
				rules := f.configuration["ingress"].([]any)
				f.configuration["ingress"] = append(append(append([]any{}, rules[:len(rules)-1]...), f.injectedRule), rules[len(rules)-1])
				f.version++
			}
		}
		if r.Method == "PUT" {
			var body struct {
				Config map[string]any `json:"config"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.configuration = body.Config
			f.version++
			f.ingressWrites++
			if f.failIngressAfterWrite {
				f.failIngressAfterWrite = false
				status = 503
			}
		}
		result = map[string]any{"config": f.configuration, "version": f.version}
	default:
		status = 404
	}
	data, _ := json.Marshal(map[string]any{"success": status < 400, "result": result, "errors": []any{}})
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
}

func binding() compute.ProjectBinding {
	return compute.ProjectBinding{HostID: "host-one", ProjectID: "project-one", ProjectRevision: 1, VolumeID: "volume-one", VolumeRevision: 1, MachineID: "machine-one", BootID: "boot-one", InstanceID: "guest-one"}
}

type fixture struct {
	manager  *Manager
	options  Options
	cloud    *fakeCloud
	valid    *atomic.Bool
	captures *atomic.Int32
	client   *http.Client
	settings Settings
}

func publishingFixture(t *testing.T) fixture {
	t.Helper()
	return publishingFixtureWithDomain(t, "github.io")
}

func publishingFixtureWithDomain(t *testing.T, domain string) fixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	_ = listener.Close()
	home := t.TempDir()
	cloud := newFakeCloud()
	cloud.zoneName = domain
	valid := &atomic.Bool{}
	valid.Store(true)
	captures := &atomic.Int32{}
	settings := Settings{APIToken: "private-api-token", AccountID: "account", ZoneID: "zone", TunnelID: "tunnel", Domain: domain, Listen: listen, Issuer: "https://team.cloudflareaccess.com", Audience: "publishing-audience", OwnerSubject: "owner-one", DesktopOrigin: "https://desktop.example.com"}
	options := Options{Home: home, Settings: func() (Settings, error) { return settings, nil }, Client: func(s Settings) (*cloudflareapi.Client, error) {
		return cloudflareapi.New(cloudflareapi.Options{Token: s.APIToken, HTTPClient: &http.Client{Transport: cloud}})
	}, Capture: func(_ context.Context, b compute.ProjectBinding) (CapturedRoute, error) {
		captures.Add(1)
		if b != binding() {
			return CapturedRoute{}, errors.New("unexpected binding")
		}
		return CapturedRoute{Socket: filepath.Join(home, "original-host.sock"), ConfigFingerprint: "original-fingerprint"}, nil
	}, Resolve: func(_ context.Context, route CapturedRoute, b compute.ProjectBinding, port int) (projectpreview.Target, error) {
		if route.Socket != filepath.Join(home, "original-host.sock") || route.ConfigFingerprint != "original-fingerprint" || b != binding() || port != 5173 {
			return projectpreview.Target{}, errors.New("private routing changed")
		}
		return projectpreview.Target{Validate: func(context.Context) error {
			if !valid.Load() {
				return errors.New("boot changed")
			}
			return nil
		}, Close: func() {}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stream" {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "ready\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			if r.URL.Path == "/ws" {
				connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{r.Host}})
				if err != nil {
					return
				}
				defer connection.CloseNow()
				for {
					kind, data, err := connection.Read(r.Context())
					if err != nil {
						return
					}
					if connection.Write(r.Context(), kind, data) != nil {
						return
					}
				}
			}
			if r.Method == "HEAD" {
				w.WriteHeader(200)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"uri": r.URL.RequestURI(), "access": r.Header.Get("Cf-Access-Jwt-Assertion"), "identity": r.Header.Get("Cf-Access-Authenticated-User-Email"), "cookie": r.Header.Get("Cookie"), "authorization": r.Header.Get("Authorization"), "private": r.Header.Get("X-AXV-Host-ID")})
		})}, nil
	}, Authorize: func(_ context.Context, _ Settings, r *http.Request) (time.Time, error) {
		switch r.Header.Get("Cf-Access-Jwt-Assertion") {
		case "owner-token":
			return time.Now().Add(time.Hour), nil
		case "expiring-token":
			return time.Now().Add(250 * time.Millisecond), nil
		default:
			return time.Time{}, errors.New("another owner")
		}
	}}
	m, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, listen)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return fixture{m, options, cloud, valid, captures, &http.Client{Transport: transport, Timeout: 5 * time.Second}, settings}
}
func publish(t *testing.T, f fixture, visibility, name string) Publication {
	t.Helper()
	value, err := f.manager.Publish(context.Background(), Registration{InstanceID: f.manager.InstanceID(), RequestID: compute.NewID("publish"), Binding: binding(), Port: 5173, Name: name, Domain: f.settings.Domain, Visibility: visibility})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func waitState(t *testing.T, m *Manager, id, state string) Publication {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, value := range m.List() {
			if value.ID == id && value.State == state {
				return value
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("publication did not reach %s: %+v", state, m.List())
	return Publication{}
}
func applicationRequest(t *testing.T, f fixture, host, method, path, token, origin string) *http.Response {
	t.Helper()
	r, err := http.NewRequest(method, "http://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		r.Header.Set("Cf-Access-Jwt-Assertion", token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Cf-Access-Authenticated-User-Email", "private@example.com")
	r.Header.Set("Cookie", "CF_Authorization=secret; normal=application")
	r.Header.Set("Authorization", "Bearer application-token")
	r.Header.Set("X-AXV-Host-ID", "spoofed")
	response, err := f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestOwnerAndPublicOriginsStripCredentialsAndRejectDesktopRouting(t *testing.T) {
	f := publishingFixture(t)
	owner := publish(t, f, "owner", "app")
	waitState(t, f.manager, owner.ID, "published")
	for _, tc := range []struct {
		host, token, origin string
		status              int
	}{{owner.Hostname, "", "", 401}, {owner.Hostname, "other-owner", "", 401}, {owner.Hostname, "owner-token", "https://desktop.example.com", 403}, {"desktop.example.com", "owner-token", "", 404}} {
		response := applicationRequest(t, f, tc.host, "GET", "/", tc.token, tc.origin)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%+v: %d", tc, response.StatusCode)
		}
	}
	response := applicationRequest(t, f, owner.Hostname, "POST", "/api/bootstrap%2Fapp?q=one", "owner-token", "https://"+owner.Hostname)
	defer response.Body.Close()
	var received map[string]string
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&received) != nil {
		t.Fatalf("application response: %d", response.StatusCode)
	}
	if received["uri"] != "/api/bootstrap%2Fapp?q=one" || received["access"] != "" || received["identity"] != "" || received["private"] != "" || strings.Contains(received["cookie"], "secret") || !strings.Contains(received["cookie"], "normal=application") || received["authorization"] != "Bearer application-token" {
		t.Fatalf("application credentials or routing changed: %+v", received)
	}
	if !strings.Contains(response.Header.Get("Content-Security-Policy"), "sandbox") || response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("origin isolation headers missing")
	}
	public := publish(t, f, "public", "public")
	waitState(t, f.manager, public.ID, "published")
	response = applicationRequest(t, f, public.Hostname, "GET", "/", "", "")
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("explicit public request: %d", response.StatusCode)
	}
	input := Registration{InstanceID: f.manager.InstanceID(), RequestID: compute.NewID("same-origin"), Binding: binding(), Port: 5173, Name: "desktop.example.com", Domain: f.settings.Domain, Visibility: "public"}
	if _, err := f.manager.Publish(context.Background(), input); err == nil {
		t.Fatal("published on desktop hostname")
	}
}

func TestJournalRecoversExactReceiptAndOriginalPrivateRouteAcrossRestart(t *testing.T) {
	f := publishingFixture(t)
	input := Registration{InstanceID: f.manager.InstanceID(), RequestID: compute.NewID("original-request"), Binding: binding(), Port: 5173, Name: "app", Domain: f.settings.Domain, Visibility: "owner"}
	first, err := f.manager.Publish(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, first.ID, "published")
	if err = f.manager.Close(); err != nil {
		t.Fatal(err)
	}
	options := f.options
	options.Capture = func(context.Context, compute.ProjectBinding) (CapturedRoute, error) {
		t.Error("restart selected a current gateway instead of original route")
		return CapturedRoute{}, errors.New("must not capture")
	}
	restarted, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	suspended := waitState(t, restarted, first.ID, "unavailable")
	if suspended.Phase != "restart-required" || suspended.URL != "" || suspended.Hostname != first.Hostname || suspended.Generation != first.Generation {
		t.Fatalf("restart reopened or replaced the original publication: %+v", suspended)
	}
	replayed, err := restarted.Publish(context.Background(), input)
	if err != nil || replayed.ID != first.ID || replayed.Generation != first.Generation || replayed.Hostname != first.Hostname || replayed.Phase != "restart-required" {
		t.Fatalf("lost original receipt: %+v %v", replayed, err)
	}
	if _, err = restarted.Retry(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, restarted, first.ID, "published")
	input.Port++
	if _, err = restarted.Publish(context.Background(), input); err == nil {
		t.Fatal("changed receipt payload accepted")
	}
	f.cloud.mu.Lock()
	creates := f.cloud.dnsCreates
	f.cloud.mu.Unlock()
	if creates != 1 || f.captures.Load() != 1 {
		t.Fatalf("restart recreated identity: dns=%d captures=%d", creates, f.captures.Load())
	}
	data, err := os.ReadFile(filepath.Join(options.Home, "cloudflare", "publications.json"))
	if err != nil || strings.Contains(string(data), f.settings.APIToken) {
		t.Fatal("journal exposed API credential")
	}
	view, _ := json.Marshal(restarted.List())
	if strings.Contains(string(view), "original-host.sock") || strings.Contains(string(view), "original-fingerprint") || strings.Contains(string(view), "owner-one") {
		t.Fatalf("public projection exposed private route or identity: %s", view)
	}
}

func TestUnpublishRevokesWebSocketsBeforeCloudCleanupAndRetriesOriginalOwnership(t *testing.T) {
	f := publishingFixture(t)
	value := publish(t, f, "owner", "app")
	waitState(t, f.manager, value.ID, "published")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws://"+value.Hostname+"/ws", &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: http.Header{"Origin": {"https://" + value.Hostname}, "Cf-Access-Jwt-Assertion": {"owner-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err = connection.Write(ctx, websocket.MessageText, []byte("original")); err != nil {
		t.Fatal(err)
	}
	_, data, err := connection.Read(ctx)
	if err != nil || string(data) != "original" {
		t.Fatalf("WebSocket forwarding: %q %v", data, err)
	}
	f.cloud.mu.Lock()
	f.cloud.failDeleteDNS = true
	f.cloud.mu.Unlock()
	if _, err = f.manager.Unpublish(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = connection.Read(ctx); err == nil {
		t.Fatal("unpublished websocket stayed open")
	}
	failed := waitState(t, f.manager, value.ID, "error")
	if !failed.CleanupPending || failed.URL != "" || !f.manager.Busy() {
		t.Fatalf("cleanup failure lost intent: %+v", failed)
	}
	response := applicationRequest(t, f, value.Hostname, "GET", "/", "owner-token", "")
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("cloud cleanup failure restored local origin")
	}
	f.cloud.mu.Lock()
	f.cloud.failDeleteDNS = false
	f.cloud.mu.Unlock()
	if _, err = f.manager.Retry(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, value.ID, "removed")
	if f.manager.Busy() {
		t.Fatal("removed history blocked configuration")
	}
	f.cloud.mu.Lock()
	defer f.cloud.mu.Unlock()
	if len(f.cloud.dns) != 0 || len(f.cloud.configuration["ingress"].([]any)) != 2 {
		t.Fatalf("cleanup lost foreign resources or retained owned resources: %+v", f.cloud.configuration)
	}
}

func TestPreparedCloudWritesRecoverUnknownOutcomesAndCanBeRemovedWithoutRecordID(t *testing.T) {
	for _, phase := range []string{"dns", "ingress"} {
		t.Run(phase, func(t *testing.T) {
			f := publishingFixture(t)
			f.cloud.mu.Lock()
			if phase == "dns" {
				f.cloud.failDNSAfterCreate = true
			} else {
				f.cloud.failIngressAfterWrite = true
			}
			f.cloud.mu.Unlock()
			value := publish(t, f, "public", "app")
			failed := waitState(t, f.manager, value.ID, "error")
			if failed.Phase != phase {
				t.Fatalf("unexpected failed phase: %+v", failed)
			}
			if phase == "dns" {
				item, _ := f.manager.record(value.ID)
				if item.Resources.DNS == nil || item.Resources.DNS.RecordID != "" {
					t.Fatal("missing prepared DNS ownership")
				}
				if _, err := f.manager.Unpublish(context.Background(), value.ID); err != nil {
					t.Fatal(err)
				}
				waitState(t, f.manager, value.ID, "removed")
				f.cloud.mu.Lock()
				defer f.cloud.mu.Unlock()
				if len(f.cloud.dns) != 0 || f.cloud.dnsCreates != 1 {
					t.Fatal("cleanup recreated or leaked DNS after a lost response")
				}
			} else {
				if _, err := f.manager.Retry(context.Background(), value.ID); err != nil {
					t.Fatal(err)
				}
				waitState(t, f.manager, value.ID, "published")
				f.cloud.mu.Lock()
				defer f.cloud.mu.Unlock()
				if f.cloud.ingressWrites != 1 {
					t.Fatalf("unknown ingress response was blindly replayed: %d writes", f.cloud.ingressWrites)
				}
			}
		})
	}
}

func TestExpiredOwnerTokenClosesOnlyItsPublishedConnection(t *testing.T) {
	f := publishingFixture(t)
	value := publish(t, f, "owner", "app")
	waitState(t, f.manager, value.ID, "published")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws://"+value.Hostname+"/ws", &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: http.Header{"Origin": {"https://" + value.Hostname}, "Cf-Access-Jwt-Assertion": {"expiring-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	_, _, err = connection.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("signed expiry did not disconnect stream promptly: %v", err)
	}
	if waitState(t, f.manager, value.ID, "published").URL == "" {
		t.Fatal("one token expiry withdrew the publication itself")
	}
	response := applicationRequest(t, f, value.Hostname, "GET", "/", "owner-token", "")
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("expired connection blocked another valid owner session")
	}
}

func TestInvalidBindingRevokesOriginalOriginWithoutRetargeting(t *testing.T) {
	f := publishingFixture(t)
	value := publish(t, f, "public", "app")
	waitState(t, f.manager, value.ID, "published")
	f.valid.Store(false)
	response := applicationRequest(t, f, value.Hostname, "GET", "/", "", "")
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatalf("stale binding forwarded: %d", response.StatusCode)
	}
	waitState(t, f.manager, value.ID, "unavailable")
	if _, err := f.manager.Retry(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, value.ID, "unavailable")
	if f.captures.Load() != 1 {
		t.Fatal("retry selected a fresh gateway binding")
	}
}

func TestDisabledNewManagerDoesNotInitializeStateAndMalformedJournalIsPreserved(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	options := Options{Home: home, Settings: func() (Settings, error) { return Settings{}, nil }}
	m, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if m.Busy() || len(m.List()) != 0 {
		t.Fatal("fresh manager has publications")
	}
	_ = m.Close()
	if _, err = os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("disabled manager initialized profile state")
	}
	if err = os.MkdirAll(filepath.Join(home, "cloudflare"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "cloudflare", "publications.json")
	for _, data := range []string{`null`, `{"version":99,"instanceId":"unknown","records":{}}`, `{"version":2,"instanceId":"journal","records":{}} {}`, "{\"version\":2,\"instanceId\":\"\xff\",\"records\":{}}", `{"version":2,"instanceId":"\ud800","records":{}}`} {
		if err = os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = New(context.Background(), options); err == nil {
			t.Fatal("invalid journal silently initialized")
		}
		actual, _ := os.ReadFile(path)
		if string(actual) != data {
			t.Fatal("invalid journal was replaced")
		}
	}
	valid := `{"version":2,"instanceId":"repaired-journal","records":{}}`
	if err = os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	repaired, err := New(context.Background(), options)
	if err != nil {
		t.Fatalf("failed journal load leaked its lifetime lock after repair: %v", err)
	}
	if repaired.InstanceID() != "repaired-journal" {
		t.Fatal("repair identity was silently replaced")
	}
	if err = repaired.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPControlsRequireOwnerAndExactOriginAndDoNotReplayChangedRequests(t *testing.T) {
	f := publishingFixture(t)
	mux := http.NewServeMux()
	f.manager.Register(mux)
	for _, body := range []string{`null`, `{} {}`, `{"unknown":true}`, `{"visibility":"public","binding":null}`} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:3001/api/cloudflare-publications", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://127.0.0.1:3001")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid body accepted: %s %d", body, w.Code)
		}
	}
	for _, peer := range []string{"192.0.2.1:1234", "127.0.0.1:1234"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:3001/api/cloudflare-publications", strings.NewReader(`{}`))
		r.RemoteAddr = peer
		r.Header.Set("Origin", "https://outside.example.com")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("untrusted request reached publishing controls")
		}
	}
	lock, err := profile.Lock(f.options.Home)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.manager.Publish(context.Background(), Registration{InstanceID: f.manager.InstanceID(), RequestID: compute.NewID("locked"), Binding: binding(), Port: 5173, Name: "app", Domain: f.settings.Domain, Visibility: "public"})
	_ = lock.Close()
	var failure *Problem
	if !errors.As(err, &failure) || failure.Code != "publication_config_busy" || f.manager.Busy() {
		t.Fatalf("busy configuration created a publication: %v", err)
	}
}

func TestConcurrentPublicationsPreserveEveryOwnedAndForeignIngress(t *testing.T) {
	f := publishingFixture(t)
	first := publish(t, f, "public", "app")
	second := publish(t, f, "public", "public")
	waitState(t, f.manager, first.ID, "published")
	waitState(t, f.manager, second.ID, "published")
	f.cloud.mu.Lock()
	defer f.cloud.mu.Unlock()
	if len(f.cloud.configuration["ingress"].([]any)) != 4 || f.cloud.ingressWrites != 2 || len(f.cloud.dns) != 2 {
		t.Fatalf("concurrent publish lost a route: %+v", f.cloud.configuration)
	}
}

func TestFailedUnpublishSaveWithdrawsActiveIOAndRestartRequiresExplicitResume(t *testing.T) {
	f := publishingFixture(t)
	value := publish(t, f, "public", "app")
	waitState(t, f.manager, value.ID, "published")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws://"+value.Hostname+"/ws", &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: http.Header{"Origin": {"https://" + value.Hostname}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	stream := applicationRequest(t, f, value.Hostname, "GET", "/stream", "", "")
	defer stream.Body.Close()
	ready := make([]byte, len("ready\n"))
	if _, err = io.ReadFull(stream.Body, ready); err != nil || string(ready) != "ready\n" {
		t.Fatalf("stream did not become active: %q %v", ready, err)
	}
	before, err := os.ReadFile(f.manager.path)
	if err != nil {
		t.Fatal(err)
	}
	saved := f.manager.path + ".saved"
	if err = os.Rename(f.manager.path, saved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(f.manager.path, 0700); err != nil {
		t.Fatal(err)
	}
	_, err = f.manager.Unpublish(context.Background(), value.ID)
	var failure *Problem
	if !errors.As(err, &failure) || failure.Code != "publication_save_failed" {
		t.Fatalf("failed save hidden: %v", err)
	}
	if _, _, err = connection.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("failed durable unpublish left its websocket open: %v", err)
	}
	streamClosed := make(chan struct{})
	go func() { _, _ = io.ReadAll(stream.Body); close(streamClosed) }()
	select {
	case <-streamClosed:
	case <-ctx.Done():
		t.Fatal("failed durable unpublish left its HTTP stream open")
	}
	if ctx.Err() != nil {
		t.Fatal("unpublish did not close its active stream promptly")
	}
	response := applicationRequest(t, f, value.Hostname, "GET", "/", "", "")
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("failed durable unpublish still exposed a live route: %d", response.StatusCode)
	}
	view := waitState(t, f.manager, value.ID, "error")
	if view.Phase != "persistence" || view.URL != "" || !strings.Contains(view.Error, "withdrawn") || f.manager.stillWanted(value.ID) {
		t.Fatalf("failed storage still reported ready: %+v", view)
	}
	original, _ := f.manager.record(value.ID)
	replayed, err := f.manager.Publish(context.Background(), original.Input)
	if err != nil || replayed.State != "error" || replayed.Phase != "persistence" || replayed.URL != "" {
		t.Fatalf("receipt recovery hid the failed removal: %+v %v", replayed, err)
	}
	if _, err = f.manager.Retry(context.Background(), value.ID); !errors.As(err, &failure) || failure.Code != "publication_storage_failed" {
		t.Fatal("uncertain journal write was silently retried")
	}
	if err = os.Remove(f.manager.path); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(saved, f.manager.path); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(f.manager.path)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed removal changed the durable original receipt")
	}
	f.cloud.mu.Lock()
	unchanged := len(f.cloud.dns) == 1 && f.cloud.ingressWrites == 1
	f.cloud.mu.Unlock()
	if !unchanged {
		t.Fatal("failed local save modified cloud ownership")
	}
	if err = f.manager.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), f.options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	suspended := waitState(t, restarted, value.ID, "unavailable")
	if suspended.Phase != "restart-required" || suspended.URL != "" || suspended.Hostname != value.Hostname {
		t.Fatalf("restart silently reopened the failed removal: %+v", suspended)
	}
	restarted.mu.Lock()
	live := len(restarted.live)
	restarted.mu.Unlock()
	if live != 0 {
		t.Fatal("restart activated a retained publication without explicit owner review")
	}
	if _, err = restarted.Unpublish(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, restarted, value.ID, "removed")
}

func TestCapturedPrivateForwardingVerifiesHostAndPreservesApplicationSemantics(t *testing.T) {
	dir, err := os.MkdirTemp("", "axv-pub-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var replaced atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			host := "host-one"
			if replaced.Load() {
				host = "replacement-host"
			}
			_ = json.NewEncoder(w).Encode(compute.Runtime{Ready: true, ProtocolVersion: compute.ProtocolVersion, HostID: host, ConfigFingerprint: "original-fingerprint"})
			return
		}
		if r.URL.Path == "/v1/projects/project-one/binding" {
			_ = json.NewEncoder(w).Encode(compute.ProjectBindingResult{Binding: binding()})
			return
		}
		if r.URL.Path != "/v1/project-preview" {
			t.Errorf("public request escaped private preview route: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var captured compute.ProjectBinding
		encoded, _ := base64.RawURLEncoding.DecodeString(r.Header.Get(protocol.PreviewBindingHeader))
		if json.Unmarshal(encoded, &captured) != nil || captured != binding() || r.Header.Get("X-AXV-Host-ID") != "host-one" || r.Header.Get(protocol.PreviewPortHeader) != "5173" || r.Header.Get(protocol.PreviewPathHeader) != "/app%2Fpart" || r.URL.RawQuery != "q=one" || r.Header.Get("Authorization") != "" || r.Header.Get(protocol.PreviewAuthorizationHeader) != "Bearer app-token" || r.Header.Get("Cf-Access-Jwt-Assertion") != "" {
			t.Errorf("private forwarding lost original routing or credentials: %s %+v", r.URL, r.Header)
		}
		http.SetCookie(w, &http.Cookie{Name: "application", Value: "session", Path: "/", Domain: "github.io"})
		http.SetCookie(w, &http.Cookie{Name: "CF_Authorization", Value: "forged", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "__Host-AXV-Isolation", Value: "forged", Path: "/", Secure: true})
		http.SetCookie(w, &http.Cookie{Name: "__host-axv-isolation-challenge", Value: "forged", Path: "/", Secure: true})
		w.Header().Set("Clear-Site-Data", `"cookies"`)
		w.Header().Set("Content-Security-Policy", "default-src *")
		_, _ = io.WriteString(w, "application")
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	route := CapturedRoute{Socket: socket, ConfigFingerprint: "original-fingerprint"}
	target, err := resolveCaptured(context.Background(), route, binding(), 5173)
	if err != nil {
		t.Fatal(err)
	}
	if err = target.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://app.example.com/app%2Fpart?q=one", nil)
	r.Header.Set("Authorization", "Bearer app-token")
	r.Header.Set("Cf-Access-Jwt-Assertion", "must-not-forward")
	r.Header.Set("X-AXV-Host-ID", "spoofed")
	w := httptest.NewRecorder()
	target.Handler.ServeHTTP(w, r)
	target.Close()
	if w.Code != 200 || w.Body.String() != "application" || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Domain != "" || w.Header().Get("Clear-Site-Data") != "" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("application response escaped origin confinement: HTTP %d %+v", w.Code, w.Header())
	}
	replaced.Store(true)
	if _, err = resolveCaptured(context.Background(), route, binding(), 5173); err == nil {
		t.Fatal("the captured socket silently selected a replacement host")
	}
}

func TestLateInitializingManagerCannotOverwriteAnotherJournalsOwnership(t *testing.T) {
	f := publishingFixture(t)
	other, err := New(context.Background(), f.options)
	if err != nil {
		t.Fatal(err)
	}
	value, err := other.Publish(context.Background(), Registration{InstanceID: other.InstanceID(), RequestID: compute.NewID("first-owner"), Binding: binding(), Port: 5173, Name: "app", Domain: f.settings.Domain, Visibility: "public"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, other, value.ID, "published")
	if _, err = New(context.Background(), f.options); err == nil {
		t.Fatal("another manager read an existing journal without its lifetime lock")
	}
	if err = other.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.manager.path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.manager.Publish(context.Background(), Registration{InstanceID: f.manager.InstanceID(), RequestID: compute.NewID("stale-owner"), Binding: binding(), Port: 5173, Name: "public", Domain: f.settings.Domain, Visibility: "public"})
	var failure *Problem
	if !errors.As(err, &failure) || failure.Code != "publication_instance_changed" {
		t.Fatalf("late stale manager did not reject a new journal: %v", err)
	}
	after, err := os.ReadFile(f.manager.path)
	if err != nil || string(before) != string(after) {
		t.Fatal("stale manager overwrote committed original ownership")
	}
	if err = f.manager.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(context.Background(), f.options)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	waitState(t, recovered, value.ID, "unavailable")
	if len(recovered.List()) != 1 || recovered.List()[0].ID != value.ID {
		t.Fatal("original publication receipt was lost")
	}
}

func TestUnpublishWhileWaitingForCloudWriterDoesNotCreateNewResources(t *testing.T) {
	f := publishingFixture(t)
	resolved := make(chan struct{})
	original := f.manager.options.Resolve
	f.manager.options.Resolve = func(ctx context.Context, route CapturedRoute, b compute.ProjectBinding, port int) (projectpreview.Target, error) {
		target, err := original(ctx, route, b, port)
		close(resolved)
		return target, err
	}
	f.manager.cloudOp.Lock()
	locked := true
	defer func() {
		if locked {
			f.manager.cloudOp.Unlock()
		}
	}()
	value := publish(t, f, "public", "app")
	select {
	case <-resolved:
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not resolve its original target")
	}
	if _, err := f.manager.Unpublish(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	f.manager.cloudOp.Unlock()
	locked = false
	waitState(t, f.manager, value.ID, "removed")
	f.cloud.mu.Lock()
	defer f.cloud.mu.Unlock()
	if f.cloud.dnsCreates != 0 || f.cloud.ingressWrites != 0 || len(f.cloud.dns) != 0 {
		t.Fatal("a withdrawn queued publication created cloud resources")
	}
}

func TestKnownForeignDNSConflictDoesNotRetainFalseOwnershipOrBlockUnpublish(t *testing.T) {
	f := publishingFixture(t)
	f.cloud.mu.Lock()
	f.cloud.injectForeignDNS = true
	f.cloud.mu.Unlock()
	value := publish(t, f, "public", "app")
	waitState(t, f.manager, value.ID, "error")
	item, _ := f.manager.record(value.ID)
	if item.Resources.DNS != nil {
		t.Fatal("a known pre-write foreign conflict retained a prepared ownership claim")
	}
	if _, err := f.manager.Unpublish(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, value.ID, "removed")
	if f.manager.Busy() {
		t.Fatal("foreign resources blocked cleanup of an unwritten publication")
	}
	f.cloud.mu.Lock()
	defer f.cloud.mu.Unlock()
	if len(f.cloud.dns) != 1 || f.cloud.dnsCreates != 0 {
		t.Fatal("foreign DNS was modified during cleanup")
	}
}

func TestKnownIngressPreparationConflictCannotBeAdoptedByRetry(t *testing.T) {
	f := publishingFixture(t)
	f.cloud.mu.Lock()
	f.cloud.injectIngressAtRead = 2
	f.cloud.injectedRule = map[string]any{"hostname": "", "service": "http://" + f.settings.Listen, "originRequest": map[string]any{"httpHostHeader": ""}}
	f.cloud.mu.Unlock()
	value := publish(t, f, "public", "app")
	waitState(t, f.manager, value.ID, "error")
	item, _ := f.manager.record(value.ID)
	if item.Resources.Ingress != nil {
		t.Fatal("a proven pre-write conflict retained adoption authority")
	}
	if _, err := f.manager.Retry(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, value.ID, "error")
	item, _ = f.manager.record(value.ID)
	if item.Resources.Ingress != nil {
		t.Fatal("retry adopted a foreign rule matching the original desired content")
	}
	if _, err := f.manager.Unpublish(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, value.ID, "removed")
	f.cloud.mu.Lock()
	defer f.cloud.mu.Unlock()
	if len(f.cloud.configuration["ingress"].([]any)) != 3 || f.cloud.ingressWrites != 0 || len(f.cloud.dns) != 0 {
		t.Fatal("cleanup modified a foreign rule after known preparation conflict")
	}
}

func TestUnconfiguredListExplainsSetupWithoutCreatingState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	m, err := New(context.Background(), Options{Home: home, Settings: func() (Settings, error) { return Settings{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	mux := http.NewServeMux()
	m.Register(mux)
	r := httptest.NewRequest("GET", "http://127.0.0.1:3001/api/cloudflare-publications", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var result struct {
		Supported    bool          `json:"supported"`
		Reason       string        `json:"reason"`
		Publications []Publication `json:"publications"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Supported || result.Reason == "" || len(result.Publications) != 0 {
		t.Fatalf("unconfigured list implied publish readiness: %d %s", w.Code, w.Body)
	}
	if _, err = os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("unconfigured read initialized profile or journal")
	}
}
