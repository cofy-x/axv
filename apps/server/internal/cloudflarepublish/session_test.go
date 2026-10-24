package cloudflarepublish

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/projectpreview"
)

// The verifier's signature, exact owner and fixed issuance-based lease are
// tested in cloudflare. Here the trusted hook supplies that original deadline
// to exercise the publication's actual HTTP/stream/proxy cancellation boundary.
func sessionSurface(t *testing.T, guest http.Handler) (*Manager, *http.Client, time.Time) {
	t.Helper()
	upstream := httptest.NewServer(guest)
	t.Cleanup(upstream.Close)
	origin, _ := url.Parse(upstream.URL)
	proxy := httputil.NewSingleHostReverseProxy(origin)
	transport := &http.Transport{Proxy: nil}
	proxy.Transport = transport
	proxy.ModifyResponse = applicationResponse
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusBadGateway) }
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{ctx: ctx, cancel: cancel, live: map[string]*runtimeEntry{}, runtimeErrors: map[string]string{}}
	if err = m.ensureSurface(address); err != nil {
		cancel()
		t.Fatal(err)
	}
	entryContext, entryCancel := context.WithCancel(ctx)
	entry := &runtimeEntry{manager: m, id: "original-publication", hostname: "lease.preview.example.net", ctx: entryContext, cancel: entryCancel, visibility: "owner", connections: map[net.Conn]int{}, target: projectpreview.Target{Handler: proxy, Validate: func(context.Context) error { return nil }, Close: transport.CloseIdleConnections}}
	m.mu.Lock()
	m.live[entry.hostname] = entry
	end := time.Now().Add(2 * time.Second)
	m.options.Authorize = func(_ context.Context, _ Settings, r *http.Request) (time.Time, error) {
		if r.Header.Get("Cf-Access-Jwt-Assertion") != "original-lease" {
			return time.Time{}, errors.New("a different owner")
		}
		return end, nil
	}
	m.mu.Unlock()
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Errorf("close publication session fixture: %v", err)
		}
	})
	clientTransport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	t.Cleanup(clientTransport.CloseIdleConnections)
	return m, &http.Client{Transport: clientTransport}, end
}

func sessionRequest(ctx context.Context, path string) *http.Request {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://lease.preview.example.net"+path, nil)
	request.Header.Set("Origin", "https://lease.preview.example.net")
	request.Header.Set("Cf-Access-Jwt-Assertion", "original-lease")
	return request
}

func TestPublicationOwnerLeaseClosesHTTPAndSSEWithoutRenewingReconnects(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "HTTP"
		if stream {
			name = "SSE"
		}
		t.Run(name, func(t *testing.T) {
			finished := make(chan struct{})
			_, client, end := sessionSurface(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "public, max-age=86400")
					w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=86400")
					_, _ = io.WriteString(w, "data: original-project\n\n")
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Errorf("guest SSE did not flush: %v", err)
						return
					}
				}
				<-r.Context().Done()
			}))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response, err := client.Do(sessionRequest(ctx, "/events"))
			if stream {
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Cloudflare-CDN-Cache-Control") != "no-store" {
					t.Fatal("guest cache headers escaped the publication boundary")
				}
				reader := bufio.NewReader(response.Body)
				line, readErr := reader.ReadString('\n')
				if readErr != nil || line != "data: original-project\n" || !time.Now().Before(end) {
					t.Fatalf("SSE was buffered until the lease closed: %q, %v", line, readErr)
				}
				_, err = io.ReadAll(reader)
			}
			if err == nil || ctx.Err() != nil {
				t.Fatalf("publication %s survived its original lease or needed a client timeout: %v", name, err)
			}
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("the old application kept forwarding after its lease")
			}
			response, err = client.Do(sessionRequest(ctx, "/events"))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("reconnect renewed the old publication lease or cached its refusal")
			}
		})
	}
}

func TestPublicationOwnerLeaseClosesUpgradedStreamsAndRejectsOldReconnects(t *testing.T) {
	finished := make(chan struct{})
	_, client, _ := sessionSurface(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=86400")
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"lease.preview.example.net"}})
		if err != nil {
			t.Errorf("guest WebSocket was rejected: %v", err)
			return
		}
		defer close(finished)
		defer connection.CloseNow()
		_, _, _ = connection.Read(r.Context())
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := sessionRequest(ctx, "/ws").Header
	connection, response, err := websocket.Dial(ctx, "ws://lease.preview.example.net/ws", &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	for _, name := range []string{"Cache-Control", "Cloudflare-CDN-Cache-Control"} {
		values := response.Header.Values(name)
		if len(values) != 1 || values[0] != "no-store" {
			t.Fatalf("guest upgraded headers replaced the no-store policy: %s %q", name, values)
		}
	}
	if _, _, err = connection.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("the old publication WebSocket did not close at the finite lease: %v", err)
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("the old guest WebSocket outlived its revoked forwarding stream")
	}
	connection, response, err = websocket.Dial(ctx, "ws://lease.preview.example.net/ws", &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
	if err == nil {
		connection.CloseNow()
		t.Fatal("a WebSocket reconnect renewed the old publication lease")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired publication reconnect did not fail closed: %v", err)
	}
}
