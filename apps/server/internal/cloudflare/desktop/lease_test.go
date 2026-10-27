package desktop

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/access"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/internal/accessfixture"
)

func TestOwnerLeaseCancelsHTTPAndFlushesSSEBeforeExpiry(t *testing.T) {
	fixture := accessfixture.New(t)
	for _, stream := range []bool{false, true} {
		name := "HTTP"
		if stream {
			name = "SSE"
		}
		t.Run(name, func(t *testing.T) {
			options := testOptions(t)
			finished := make(chan struct{})
			options.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				w.Header().Set("Cache-Control", "public, max-age=86400")
				w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=86400")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: attached\n\n")
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Errorf("SSE flush failed: %v", err)
						return
					}
				}
				<-r.Context().Done()
			})
			service := startFixtureService(t, fixture, options)
			end := time.Now().Add(2 * time.Second).Truncate(time.Second)
			// This token was issued almost fifteen minutes ago. A newly opened
			// connection receives only the final seconds of its original lease.
			raw := fixture.Token(t, time.Now(), map[string]any{"iat": end.Add(-access.LeaseDuration).Unix(), "nbf": end.Add(-access.LeaseDuration).Unix(), "exp": end.Unix()})
			request, _ := http.NewRequest(http.MethodGet, "http://"+service.Status().Listen+"/stream", nil)
			request.Host = "axv.example.com"
			request.Header.Set("Cf-Access-Jwt-Assertion", raw)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := &http.Client{Transport: &http.Transport{Proxy: nil}}
			defer client.CloseIdleConnections()
			response, err := client.Do(request.WithContext(ctx))
			if stream {
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Cloudflare-CDN-Cache-Control") != "no-store" {
					t.Fatal("the application replaced the sensitive-response cache policy")
				}
				reader := bufio.NewReader(response.Body)
				line, readErr := reader.ReadString('\n')
				if readErr != nil || line != "data: attached\n" || !time.Now().Before(end) {
					t.Fatalf("SSE was buffered until expiry: %q, %v", line, readErr)
				}
				_, err = io.ReadAll(reader)
			}
			if err == nil || ctx.Err() != nil {
				t.Fatalf("the original lease did not close %s without client timeout: %v", name, err)
			}
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("expired request handler retained authority")
			}
			request, _ = http.NewRequest(http.MethodGet, "http://"+service.Status().Listen+"/stream", nil)
			request.Host = "axv.example.com"
			request.Header.Set("Cf-Access-Jwt-Assertion", raw)
			response, err = client.Do(request.WithContext(ctx))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatal("an expired lease was renewed by HTTP reconnect")
			}
		})
	}
}

func TestOwnerWebSocketReconnectCannotRenewItsSignedLease(t *testing.T) {
	fixture := accessfixture.New(t)
	options := testOptions(t)
	options.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"axv.example.com"}})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		_, _, _ = connection.Read(r.Context())
	})
	service := startFixtureService(t, fixture, options)
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, service.Status().Listen)
	}}
	defer transport.CloseIdleConnections()
	end := time.Now().Add(2 * time.Second).Truncate(time.Second)
	raw := fixture.Token(t, time.Now(), map[string]any{"iat": end.Add(-access.LeaseDuration).Unix(), "nbf": end.Add(-access.LeaseDuration).Unix(), "exp": end.Unix()})
	header := http.Header{"Origin": {testOrigin}, "Cf-Access-Jwt-Assertion": {raw}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, response, err := websocket.Dial(ctx, "ws://axv.example.com/ws", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("the upgraded response lost no-store")
	}
	if _, _, err = connection.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("the original lease did not close the WebSocket: %v", err)
	}
	connection, response, err = websocket.Dial(ctx, "ws://axv.example.com/ws", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: header})
	if err == nil {
		connection.CloseNow()
		t.Fatal("WebSocket reconnect renewed an expired token")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired WebSocket reconnect did not fail closed: %v", err)
	}
	if strings.Contains(response.Header.Get("Cache-Control"), "public") {
		t.Fatal("an authorization failure became cacheable")
	}
}
