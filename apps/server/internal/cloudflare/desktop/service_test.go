package desktop

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/internal/accessfixture"
	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

func startFixtureService(t *testing.T, f *accessfixture.Fixture, options Options) *Service {
	t.Helper()
	service, err := start(context.Background(), options, dependencies{client: f.Client})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close ingress: %v", err)
		}
	})
	return service
}

func TestIngressRejectsUnsafeConfiguration(t *testing.T) {
	for _, address := range []string{"", ":3002", "0.0.0.0:3002", "[::]:3002", "192.0.2.1:3002", "localhost:3002", "127.0.0.1:0", "127.0.0.1:03002", "unix:/tmp/axv.sock"} {
		t.Run(address, func(t *testing.T) {
			options := testOptions(t)
			options.Listen = address
			if service, err := Start(context.Background(), options); err == nil {
				_ = service.Close()
				t.Fatal("unsafe listen address was accepted")
			}
		})
	}
	for _, origin := range []string{"http://axv.example.com", "https://localhost", "https://127.0.0.1", "https://axv.local", "https://AXV.example.com", testOrigin + ":443", testOrigin + "/", testOrigin + "?query", "https://user@axv.example.com", "https://axv.example.com#fragment"} {
		options := testOptions(t)
		options.Origin = origin
		if err := validateOptions(options); err == nil {
			t.Fatalf("unsafe origin %q was accepted", origin)
		}
	}
	for _, issuer := range []string{"https://cloudflareaccess.com", "https://nested.owner.cloudflareaccess.com", "https://owner.cloudflareaccess.com.attacker.com", "http://owner.cloudflareaccess.com", testIssuer + "/", testIssuer + ":443"} {
		options := testOptions(t)
		options.Issuer = issuer
		if err := validateOptions(options); err == nil {
			t.Fatalf("unsafe issuer %q was accepted", issuer)
		}
	}
	for _, change := range []func(*Options){
		func(o *Options) { o.Audience = "" }, func(o *Options) { o.Audience = "two audiences" },
		func(o *Options) { o.OwnerSubject = "" }, func(o *Options) { o.OwnerSubject = "owner\nsubject" },
		func(o *Options) { o.Handler = nil }, func(o *Options) { o.AllowRoute = nil },
	} {
		options := testOptions(t)
		change(&options)
		if err := validateOptions(options); err == nil {
			t.Fatal("incomplete authentication configuration was accepted")
		}
	}
	options := testOptions(t)
	options.Listen = "[::1]:3002"
	if err := validateOptions(options); err != nil {
		t.Fatalf("numeric IPv6 loopback should be valid: %v", err)
	}
}

func TestEveryRequestRequiresJWTAndPreservesActualTransport(t *testing.T) {
	f := accessfixture.New(t)
	options := testOptions(t)
	var handled atomic.Int64
	options.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled.Add(1)
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(peer).IsLoopback() || r.TLS != nil || r.Host != "axv.example.com" || !httpaccess.AllowRequest(r, false) || httpaccess.LocalRequest(r, false) {
			t.Error("authenticated ingress did not preserve the actual HTTP transport boundary")
		}
		_, _ = io.WriteString(w, "authorized")
	})
	options.AllowRoute = func(r *http.Request) bool { return !strings.HasPrefix(r.URL.Path, "/api/unknown") }
	options.LocalOnlyRoute = func(r *http.Request) bool { return r.URL.Path == "/api/local" }
	service := startFixtureService(t, f, options)
	status := service.Status()
	if status.State != "listening" || status.URL != testOrigin || status.Listen != options.Listen || status.Error != "" {
		t.Fatalf("unexpected local ingress status: %#v", status)
	}
	raw := f.Token(t, time.Now(), nil)
	for _, test := range []struct {
		name, path, method, host, origin, token string
		duplicateJWT, duplicateOrigin, tls      bool
		peer                                    string
		want                                    int
	}{
		{name: "UI without token", path: "/", want: 401},
		{name: "asset without token", path: "/assets/app.js", want: 401},
		{name: "manifest without token", path: "/manifest.webmanifest", want: 401},
		{name: "small icon without token", path: "/icons/axv-192.png", want: 401},
		{name: "large icon without token", path: "/icons/axv-512.png", want: 401},
		{name: "Apple icon without token", path: "/icons/apple-touch-icon.png", want: 401},
		{name: "manifest HEAD without token", path: "/manifest.webmanifest", method: "HEAD", want: 401},
		{name: "document read without token", path: "/api/files", want: 401},
		{name: "forged identity headers", path: "/api/files", token: "forged", want: 401},
		{name: "authorized UI", path: "/", token: raw, want: 200},
		{name: "authorized manifest", path: "/manifest.webmanifest", token: raw, want: 200},
		{name: "authorized small icon", path: "/icons/axv-192.png", token: raw, want: 200},
		{name: "authorized large icon", path: "/icons/axv-512.png", token: raw, want: 200},
		{name: "authorized Apple icon", path: "/icons/apple-touch-icon.png", token: raw, want: 200},
		{name: "authorized read origin optional", path: "/api/files", token: raw, want: 200},
		{name: "authorized write", path: "/api/files", method: "POST", origin: testOrigin, token: raw, want: 200},
		{name: "write missing origin", method: "POST", token: raw, want: 403},
		{name: "read wrong origin", origin: "https://attacker.example.com", token: raw, want: 403},
		{name: "read empty origin", origin: "empty", token: raw, want: 403},
		{name: "duplicate origin", origin: testOrigin, token: raw, duplicateOrigin: true, want: 403},
		{name: "local-only", path: "/api/local", token: raw, want: 403},
		{name: "unknown API", path: "/api/unknown", token: raw, want: 404},
		{name: "local host bypass", host: "127.0.0.1:3002", token: raw, want: 403},
		{name: "wrong public host", host: "attacker.example.com", token: raw, want: 403},
		{name: "host with port", host: "axv.example.com:443", token: raw, want: 403},
		{name: "non-loopback peer", peer: "192.0.2.1:1234", token: raw, want: 403},
		{name: "forwarded remote peer", peer: "invalid-peer", token: raw, want: 403},
		{name: "TLS is not this ingress", token: raw, tls: true, want: 403},
		{name: "duplicate JWT", token: raw, duplicateJWT: true, want: 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			request := httptest.NewRequest(method, "http://axv.example.com"+test.path, nil)
			request.RemoteAddr = "127.0.0.1:4321"
			if test.peer != "" {
				request.RemoteAddr = test.peer
			}
			if test.host != "" {
				request.Host = test.host
			}
			if test.origin != "" {
				origin := test.origin
				if origin == "empty" {
					origin = ""
				}
				request.Header.Add("Origin", origin)
			}
			if test.duplicateOrigin {
				request.Header.Add("Origin", testOrigin)
			}
			if test.token != "" {
				request.Header.Add("Cf-Access-Jwt-Assertion", test.token)
			}
			if test.duplicateJWT {
				request.Header.Add("Cf-Access-Jwt-Assertion", test.token)
			}
			if test.tls {
				request.TLS = &tls.ConnectionState{}
			}
			request.Header.Set("Cf-Access-Authenticated-User-Email", "owner@example.com")
			request.Header.Set("X-Forwarded-For", "127.0.0.1")
			request.Header.Set("X-Forwarded-Host", "axv.example.com")
			request.Header.Set("X-Forwarded-Proto", "https")
			request.Header.Set("Authorization", "Bearer "+raw)
			request.AddCookie(&http.Cookie{Name: "CF_Authorization", Value: raw})
			response := httptest.NewRecorder()
			service.serveHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("got %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if strings.Contains(response.Body.String(), raw) || strings.Contains(response.Body.String(), testOwner) {
				t.Fatal("authentication material leaked in response")
			}
		})
	}
	if got := handled.Load(); got != 7 {
		t.Fatalf("unauthorized request reached shared handler: %d calls", got)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if service.Status().State != "stopped" {
		t.Fatal("close did not publish stopped status")
	}
}

func TestHTTPIngressUsesPublicHostAndCannotBypassJWT(t *testing.T) {
	f := accessfixture.New(t)
	service := startFixtureService(t, f, testOptions(t))
	client := &http.Client{Timeout: 3 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	for _, authorized := range []bool{false, true} {
		request, _ := http.NewRequest(http.MethodGet, "http://"+service.Status().Listen+"/", nil)
		request.Host = "axv.example.com"
		if authorized {
			request.Header.Set("Cf-Access-Jwt-Assertion", f.Token(t, time.Now(), nil))
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		want := http.StatusUnauthorized
		if authorized {
			want = http.StatusNoContent
		}
		if response.StatusCode != want {
			t.Fatalf("HTTP ingress got %d, want %d", response.StatusCode, want)
		}
	}
}

func TestAuthenticatedFeaturesNeverReceiveCloudflareCredentials(t *testing.T) {
	f := accessfixture.New(t)
	options := testOptions(t)
	raw := f.Token(t, time.Now(), nil)
	request := httptest.NewRequest(http.MethodPost, "http://axv.example.com/api/files", nil)
	request.RemoteAddr = "127.0.0.1:4321"
	request.Header.Set("Origin", testOrigin)
	request.Header.Set("Cf-Access-Jwt-Assertion", raw)
	request.Header.Set("Cf-Access-Authenticated-User-Email", "owner@example.com")
	request.Header.Set("Cf-Access-Client-Id", "client-id")
	request.Header.Set("Cf-Access-Client-Secret", "client-secret")
	request.Header["cF-aCcEsS-Custom"] = []string{"custom-credential"}
	request.Header.Set("X-Application-Header", "application-value")
	request.Header.Set("Authorization", "Bearer application-token")
	request.Header.Add("Cookie", "app_session=application-session; CF_Authorization="+raw+`; preferences="dark"`)
	request.Header.Add("Cookie", "CF_Binding=binding-secret; another=keep; CF_AppSession=app-secret; CF_Session=session-secret; CF_Device=device-secret")
	request.Header.Add("Cookie", "cf_authorization=alternate-case-secret; CF_Binding=binding-duplicate")
	original := request.Clone(request.Context())
	var handled bool
	options.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		if r == request || r.Host != request.Host || r.RemoteAddr != request.RemoteAddr || r.TLS != request.TLS || r.Header.Get("Origin") != testOrigin || !httpaccess.AllowRequest(r, true) {
			t.Error("sanitization changed transport identity or failed to clone the request")
		}
		for name := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "cf-access-") {
				t.Errorf("Access header reached the shared handler: %s", name)
			}
		}
		cookies := make(map[string]string)
		for _, cookie := range r.Cookies() {
			cookies[cookie.Name] = cookie.Value
		}
		want := map[string]string{"app_session": "application-session", "preferences": "dark", "another": "keep"}
		if !reflect.DeepEqual(cookies, want) || len(r.Header.Values("Cookie")) != 2 {
			t.Errorf("application cookies were not preserved: %#v", cookies)
		}
		if r.Header.Get("X-Application-Header") != "application-value" || r.Header.Get("Authorization") != "Bearer application-token" {
			t.Error("unrelated application headers were removed")
		}
		for _, values := range r.Header {
			for _, value := range values {
				if strings.Contains(value, raw) || strings.Contains(value, "binding-secret") {
					t.Error("Access credentials escaped authentication")
				}
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	options.AllowRoute = func(r *http.Request) bool {
		if r.Header.Get("Cf-Access-Jwt-Assertion") != "" {
			t.Error("route policy received the Access token")
		}
		return true
	}
	service := startFixtureService(t, f, options)
	response := httptest.NewRecorder()
	service.serveHTTP(response, request)
	if response.Code != http.StatusNoContent || !handled {
		t.Fatalf("authorized request did not reach handler: %d", response.Code)
	}
	if !reflect.DeepEqual(request.Header, original.Header) || request.Host != original.Host || request.RemoteAddr != original.RemoteAddr || request.TLS != original.TLS {
		t.Fatal("sanitization mutated the original request")
	}
}

func TestWebSocketRequiresOriginAndEndsAtJWTExpiry(t *testing.T) {
	f := accessfixture.New(t)
	options := testOptions(t)
	entered := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	options.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"axv.example.com"}})
		if err != nil {
			t.Errorf("accept authorized WebSocket: %v", err)
			return
		}
		defer connection.CloseNow()
		entered <- struct{}{}
		_, _, _ = connection.Read(r.Context())
		stopped <- struct{}{}
	})
	service := startFixtureService(t, f, options)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, service.Status().Listen)
	}
	t.Cleanup(transport.CloseIdleConnections)
	header := make(http.Header)
	header.Set("Cf-Access-Jwt-Assertion", f.Token(t, time.Now(), nil))
	_, response, err := websocket.Dial(context.Background(), "ws://axv.example.com/socket", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: header})
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("WebSocket without Origin was accepted")
	}
	// The signed expiry is only a few seconds away, so the real HTTP server
	// must cancel and close a hijacked connection without another frame arriving.
	expiry := time.Now().Add(2 * time.Second).Truncate(time.Second)
	header.Set("Cf-Access-Jwt-Assertion", f.Token(t, time.Now(), map[string]any{"exp": expiry.Unix()}))
	header.Set("Origin", testOrigin)
	connection, _, err := websocket.Dial(context.Background(), "ws://axv.example.com/socket", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, _, err := connection.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("WebSocket did not close at token expiry: %v", err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("expired request handler did not finish")
	}
	service.requests.Wait()
	service.mu.Lock()
	remaining := len(service.connections)
	service.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expired hijacked connection was retained: %d", remaining)
	}
	header.Set("Cf-Access-Jwt-Assertion", f.Token(t, time.Now(), nil))
	connection, _, err = websocket.Dial(context.Background(), "ws://axv.example.com/socket", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	<-entered
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("Close did not terminate the hijacked WebSocket: %v", err)
	}
	<-stopped
}

func TestUpgradeHeaderVariantsAlwaysRequireOrigin(t *testing.T) {
	f := accessfixture.New(t)
	service := startFixtureService(t, f, testOptions(t))
	raw := f.Token(t, time.Now(), nil)
	for _, values := range [][]string{{"WebSocket"}, {"foo, websocket"}, {"foo", "websocket"}, {"", "websocket"}} {
		request := httptest.NewRequest(http.MethodGet, testOrigin+"/socket", nil)
		request.RemoteAddr = "127.0.0.1:1234"
		request.TLS = nil
		request.Header.Set("Cf-Access-Jwt-Assertion", raw)
		request.Header["Upgrade"] = values
		response := httptest.NewRecorder()
		service.serveHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("Upgrade %q bypassed Origin requirement: %d", values, response.Code)
		}
	}
}

func TestCloseCancelsIngressStreamsAndLeavesOtherServersRunning(t *testing.T) {
	f := accessfixture.New(t)
	options := testOptions(t)
	entered := make(chan struct{})
	stopped := make(chan struct{})
	options.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: attached\n\n")
		_ = http.NewResponseController(w).Flush()
		close(entered)
		<-r.Context().Done()
		close(stopped)
	})
	service := startFixtureService(t, f, options)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "independent") }))
	defer other.Close()
	request, _ := http.NewRequest(http.MethodGet, "http://"+service.Status().Listen+"/events", nil)
	request.Host = "axv.example.com"
	request.Header.Set("Cf-Access-Jwt-Assertion", f.Token(t, time.Now(), nil))
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-entered
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Close did not join its stream handler")
	}
	if response, err := client.Get(other.URL); err != nil {
		t.Fatalf("independent HTTP service was interrupted: %v", err)
	} else {
		_ = response.Body.Close()
	}
	status, _ := json.Marshal(service.Status())
	for _, secret := range []string{testIssuer, testAUD, testOwner} {
		if strings.Contains(string(status), secret) {
			t.Fatal("private configuration leaked in transport status")
		}
	}
}

func testOptions(t *testing.T) Options {
	t.Helper()
	// Configuration requires an explicit, nonzero port. Reserve an OS-selected
	// loopback port briefly without changing the production validation rules.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return Options{Listen: address, Origin: testOrigin, Issuer: testIssuer, Audience: testAUD, OwnerSubject: testOwner,
		Handler:    http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		AllowRoute: func(*http.Request) bool { return true }}
}

const (
	testOrigin = accessfixture.Origin
	testIssuer = accessfixture.Issuer
	testAUD    = accessfixture.Audience
	testOwner  = accessfixture.Owner
)
