package remoteaccess

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	preparehttp "github.com/cofy-x/axv/apps/server/internal/compute/prepare/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

const testOrigin = "https://axv.example.ts.net"

func ownerIdentity() Identity { return Identity{UserID: "123", NodeID: "node-owner"} }

func options() MiddlewareOptions {
	return MiddlewareOptions{Origin: testOrigin, OwnerUserID: "123", WhoIs: func(context.Context, string) (Identity, error) { return ownerIdentity(), nil }, AllowRoute: func(*http.Request) bool { return true }}
}

func newMiddleware(t *testing.T, options MiddlewareOptions) *Middleware {
	t.Helper()
	m, err := NewMiddleware(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

func request(method, path string) *http.Request {
	r := httptest.NewRequest(method, testOrigin+path, nil)
	r.RemoteAddr = "100.101.102.103:45678"
	return r
}

func TestMiddlewareRequiresExplicitHTTPSOwnerResolverAndRoutePolicy(t *testing.T) {
	for _, origin := range []string{"http://axv.example.ts.net", "https://axv.example.ts.net/", "https://user@axv.example.ts.net", "https://axv.example.ts.net?", "https://axv.example.ts.net#", "https://axv.example.ts.net:0", "https://axv.example.ts.net:65536"} {
		input := options()
		input.Origin = origin
		if _, err := NewMiddleware(input); err == nil {
			t.Errorf("unsafe HTTPS origin accepted: %q", origin)
		}
	}
	for _, userID := range []string{"", "0", "-1", "00123", "+123", "userid:123", "123\n"} {
		input := options()
		input.OwnerUserID = userID
		if _, err := NewMiddleware(input); err == nil {
			t.Errorf("noncanonical owner ID accepted: %q", userID)
		}
	}
	input := options()
	input.WhoIs = nil
	if _, err := NewMiddleware(input); err == nil {
		t.Fatal("missing identity resolver accepted")
	}
	input = options()
	input.AllowRoute = nil
	if _, err := NewMiddleware(input); err == nil {
		t.Fatal("implicit route exposure accepted")
	}
}

func TestMiddlewareAuthorizesActualPeerAndPreservesTransport(t *testing.T) {
	input := options()
	var lookupContext context.Context
	input.WhoIs = func(ctx context.Context, remote string) (Identity, error) {
		if remote != "100.101.102.103:45678" {
			t.Fatalf("identity lookup trusted forwarded address: %q", remote)
		}
		lookupContext = ctx
		return ownerIdentity(), nil
	}
	m := newMiddleware(t, input)
	r := request(http.MethodPost, "/api/entries")
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("Tailscale-User-ID", "456")
	called := false
	wrapped := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, authenticated *http.Request) {
		called = true
		if authenticated.RemoteAddr != r.RemoteAddr || authenticated.Host != r.Host || authenticated.TLS != r.TLS || !localaccess.AllowRequest(authenticated, true) || localaccess.Request(authenticated, true) {
			t.Fatal("remote request bypassed authentication by rewriting its transport")
		}
		if lookupContext.Err() == nil || authenticated.Context().Err() != nil {
			t.Fatal("short identity timeout leaked into the authenticated request lifetime")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if !called || w.Code != http.StatusNoContent {
		t.Fatalf("owner request rejected: %d %s", w.Code, w.Body)
	}
}

func TestMiddlewareRejectsUnverifiedOrTaggedIdentitiesDespiteForgedHeaders(t *testing.T) {
	for _, identity := range []Identity{{UserID: "456", NodeID: "node-other"}, {UserID: "123", NodeID: "node-owner", Tagged: true}, {NodeID: "node-owner"}, {UserID: "123"}, {UserID: "userid:123", NodeID: "node-owner"}, {UserID: "123", NodeID: "invalid node"}} {
		input := options()
		input.WhoIs = func(context.Context, string) (Identity, error) { return identity, nil }
		m := newMiddleware(t, input)
		r := request(http.MethodGet, "/api/entries")
		r.Header.Set("Tailscale-User-ID", "123")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		w := httptest.NewRecorder()
		m.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unapproved identity reached documents") })).ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("identity %#v accepted: %d %s", identity, w.Code, w.Body)
		}
	}
}

func TestMiddlewareRejectsInvalidTransportOrOriginBeforeIdentityLookup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"plain HTTP with forwarded TLS", func(r *http.Request) { r.TLS = nil; r.Header.Set("X-Forwarded-Proto", "https") }},
		{"different host", func(r *http.Request) {
			r.Host = "attacker.example"
			r.Header.Set("X-Forwarded-Host", "axv.example.ts.net")
		}},
		{"invalid peer", func(r *http.Request) { r.RemoteAddr = "unknown" }},
		{"cross origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }},
		{"loopback origin", func(r *http.Request) { r.Header.Set("Origin", "http://localhost:3001") }},
		{"origin with path", func(r *http.Request) { r.Header.Set("Origin", testOrigin+"/") }},
		{"duplicate origin", func(r *http.Request) { r.Header.Add("Origin", testOrigin); r.Header.Add("Origin", testOrigin) }},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }},
		{"empty origin", func(r *http.Request) { r.Header.Set("Origin", "") }},
		{"mutation without origin", func(r *http.Request) { r.Method = http.MethodPost }},
		{"WebSocket without origin", func(r *http.Request) { r.Header.Set("Upgrade", "websocket") }},
		{"WebSocket upgrade token list without origin", func(r *http.Request) { r.Header.Set("Upgrade", "other, websocket") }},
		{"multiple upgrade headers without origin", func(r *http.Request) { r.Header.Add("Upgrade", "other"); r.Header.Add("Upgrade", "websocket") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := options()
			input.WhoIs = func(context.Context, string) (Identity, error) {
				t.Fatal("invalid request reached WhoIs")
				return Identity{}, nil
			}
			m := newMiddleware(t, input)
			r := request(http.MethodGet, "/api/entries")
			tc.change(r)
			w := httptest.NewRecorder()
			m.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid request reached documents") })).ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestMiddlewareDistinguishesLocalControlsFromUnknownRoutesAfterAuthentication(t *testing.T) {
	input := options()
	input.AllowRoute = func(*http.Request) bool { return false }
	input.LocalOnlyRoute = func(r *http.Request) bool { return r.URL.Path == "/api/remote" }
	m := newMiddleware(t, input)
	wrapped := m.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("excluded route reached shared handler") }))
	for _, tc := range []struct {
		path   string
		status int
		code   string
	}{{"/api/remote", 403, "remote_local_only"}, {"/v1/machines", 404, "not_found"}} {
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, request(http.MethodGet, tc.path))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("route boundary: %d %s", w.Code, w.Body)
		}
	}
	input.WhoIs = func(context.Context, string) (Identity, error) {
		return Identity{}, errors.New("private provider error")
	}
	denied := newMiddleware(t, input)
	w := httptest.NewRecorder()
	denied.Wrap(http.NotFoundHandler()).ServeHTTP(w, request(http.MethodGet, "/unknown"))
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "private provider") {
		t.Fatalf("routing or provider error leaked before authentication: %d %s", w.Code, w.Body)
	}
}

func TestRemoteAuthorityDoesNotAuthorizeMachineLocalPreparation(t *testing.T) {
	m := newMiddleware(t, options())
	w := httptest.NewRecorder()
	m.Wrap(preparehttp.Unavailable("instance", errors.New("private preparation failure"))).ServeHTTP(w, request(http.MethodGet, "/api/compute/preparation/check"))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "preparation_forbidden") {
		t.Fatalf("remote context bypassed pure local access: %d %s", w.Code, w.Body)
	}
}

func TestMiddlewareIdentityTimeoutIsBoundedAndPrivate(t *testing.T) {
	input := options()
	input.IdentityTimeout = 10 * time.Millisecond
	input.WhoIs = func(ctx context.Context, _ string) (Identity, error) {
		<-ctx.Done()
		return Identity{}, errors.New("secret provider detail")
	}
	m := newMiddleware(t, input)
	w := httptest.NewRecorder()
	m.Wrap(http.NotFoundHandler()).ServeHTTP(w, request(http.MethodGet, "/"))
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "secret provider") {
		t.Fatalf("identity timeout leaked provider data: %d %s", w.Code, w.Body)
	}
}

func TestMiddlewareRevalidatesTheOriginalPeerUntilTheHandlerFinishes(t *testing.T) {
	for _, revoked := range []struct {
		name     string
		identity Identity
		err      error
	}{
		{"tagged", Identity{UserID: "123", NodeID: "node-owner", Tagged: true}, nil},
		{"different owner", Identity{UserID: "456", NodeID: "node-owner"}, nil},
		{"replaced node", Identity{UserID: "123", NodeID: "node-replacement"}, nil},
		{"removed node", Identity{}, errors.New("private identity lookup failure")},
	} {
		t.Run(revoked.name, func(t *testing.T) {
			var calls atomic.Int32
			input := options()
			input.IdentityRefresh = time.Millisecond
			input.WhoIs = func(_ context.Context, peer string) (Identity, error) {
				if peer != "100.101.102.103:45678" {
					t.Errorf("peer changed during revalidation: %q", peer)
				}
				if calls.Add(1) == 1 {
					return ownerIdentity(), nil
				}
				return revoked.identity, revoked.err
			}
			m := newMiddleware(t, input)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				m.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					<-r.Context().Done()
					if localaccess.AllowRequest(r, false) {
						t.Error("revoked request retained remote authority")
					}
				})).ServeHTTP(httptest.NewRecorder(), request(http.MethodGet, "/api/terminal"))
			}()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("revoked peer retained its active request")
			}
		})
	}
}

func TestMiddlewareCompletedRequestsPreserveKeepAlive(t *testing.T) {
	server := httptest.NewUnstartedServer(nil)
	input := options()
	input.Origin = "https://" + server.Listener.Addr().String()
	input.IdentityRefresh = time.Millisecond
	m := newMiddleware(t, input)
	server.Config.Handler = m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Millisecond)
		_, _ = io.WriteString(w, "ready")
	}))
	server.StartTLS()
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = time.Second
	for i := range 2 {
		reused := false
		ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }})
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "ready" || i == 1 && !reused {
			t.Fatalf("completed request poisoned its reusable connection: status=%d body=%q reused=%v err=%v", response.StatusCode, body, reused, err)
		}
	}
}

func TestMiddlewareCloseInterruptsPartialRequestBodyAndWaitsForHandler(t *testing.T) {
	server := httptest.NewUnstartedServer(nil)
	input := options()
	input.Origin = "https://" + server.Listener.Addr().String()
	m := newMiddleware(t, input)
	started := make(chan struct{})
	finished := make(chan error, 1)
	server.Config.Handler = m.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		_, err := io.ReadAll(r.Body)
		finished <- err
	}))
	server.StartTLS()
	t.Cleanup(server.Close)
	config := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	conn, err := tls.Dial("tcp", server.Listener.Addr().String(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "POST /api/entries HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nContent-Length: 100\r\n\r\n{", server.Listener.Addr(), input.Origin); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("partial request did not enter its handler")
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("remote shutdown remained blocked behind a partial JSON body")
	}
	if err := <-finished; err == nil {
		t.Fatal("partial request body reported completion during shutdown")
	}
	w := httptest.NewRecorder()
	m.Wrap(http.NotFoundHandler()).ServeHTTP(w, request(http.MethodGet, "/"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed entrypoint accepted another request: %d", w.Code)
	}
}
