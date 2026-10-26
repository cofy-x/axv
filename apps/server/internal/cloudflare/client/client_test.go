package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func rawResponse(status int, value string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(value))}
}

func resultResponse(value any) *http.Response {
	encoded, _ := json.Marshal(map[string]any{"success": true, "result": value})
	return rawResponse(http.StatusOK, string(encoded))
}

func testClient(t *testing.T, handler roundTripFunc) *Client {
	t.Helper()
	client, err := New(Options{Token: "test-secret-token", HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "api.cloudflare.com" || request.Header.Get("Authorization") != "Bearer test-secret-token" || request.Header.Get("Cookie") != "" {
			t.Fatalf("unexpected management request authority or headers")
		}
		return handler(request)
	})}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func failureKind(t *testing.T, err error, kind string, uncertain bool) {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != kind || failure.Uncertain != uncertain {
		t.Fatalf("error=%v, want kind=%s uncertain=%v", err, kind, uncertain)
	}
}

func TestClientTokenAndTransportBoundaries(t *testing.T) {
	for _, token := range []string{"", " token", "token\nheader", "☁", strings.Repeat("t", 4097)} {
		if _, err := New(Options{Token: token}); err == nil {
			t.Fatal("accepted invalid token")
		}
	}
	jar, _ := cookiejar.New(nil)
	injected := http.DefaultTransport.(*http.Transport).Clone()
	injected.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS10}
	client, err := New(Options{Token: "private", HTTPClient: &http.Client{Transport: injected, Jar: jar, Timeout: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	transport := client.http.Transport.(*http.Transport)
	if transport == injected || transport.Proxy != nil || client.http.Jar != nil || client.http.Timeout > requestTimeout || transport.TLSClientConfig.MinVersion < tls.VersionTLS12 || injected.TLSClientConfig.MinVersion != tls.VersionTLS10 {
		t.Fatal("caller transport was mutated or unsafe defaults inherited")
	}
	if strings.Contains(fmt.Sprintf("%v %#v", client, client), "private") {
		t.Fatal("client formatting leaked credentials")
	}
}

func TestRedirectAndCancellationNeverLeakCredentials(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		response := rawResponse(http.StatusFound, "")
		response.Header.Set("Location", "https://evil.example/steal?test-secret-token")
		return response, nil
	})
	_, err := client.VerifyToken(context.Background())
	failureKind(t, err, "unavailable", false)
	if calls.Load() != 1 || strings.Contains(err.Error(), "test-secret-token") || strings.Contains(err.Error(), "evil") {
		t.Fatal("redirect followed or sensitive request URL disclosed")
	}
	client = testClient(t, func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, fmt.Errorf("transport exposed test-secret-token: %w", request.Context().Err())
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.VerifyToken(ctx)
	failureKind(t, err, "unavailable", false)
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "test-secret-token") {
		t.Fatal("cancellation lost or transport message exposed")
	}
}

func TestResponseAndWriteUncertainty(t *testing.T) {
	for _, test := range []struct {
		name, body, kind string
		status           int
		write, uncertain bool
	}{
		{"permission", `{"success":false,"errors":[{"code":9109,"message":"test-secret-token"}]}`, "forbidden", 403, false, false},
		{"malformed", `{"success":true,"result":`, "invalid_response", 200, true, true},
		{"too-large", strings.Repeat("x", maxResponseBytes+1), "invalid_response", 200, false, false},
		{"server-error", `{"success":false}`, "provider_error", 503, true, true},
		{"definite-denial", `{"success":false}`, "forbidden", 403, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := testClient(t, func(*http.Request) (*http.Response, error) { return rawResponse(test.status, test.body), nil })
			method := http.MethodGet
			if test.write {
				method = http.MethodPost
			}
			_, err := client.do(context.Background(), method, "/test", nil, nil)
			failureKind(t, err, test.kind, test.uncertain)
			if strings.Contains(fmt.Sprintf("%v %+v", err, err), "test-secret-token") {
				t.Fatal("provider error leaked credentials")
			}
		})
	}
}

func TestTokenAndPagination(t *testing.T) {
	client := testClient(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/client/v4/user/tokens/verify" {
			return resultResponse(map[string]any{"id": "token", "status": "active", "expires_on": time.Now().Add(time.Hour)}), nil
		}
		if request.URL.Query().Get("per_page") != "50" {
			t.Fatal("unbounded pagination")
		}
		page := request.URL.Query().Get("page")
		if page != "1" && page != "2" {
			t.Fatalf("unexpected page %s", page)
		}
		return rawResponse(200, fmt.Sprintf(`{"success":true,"result":[{"id":"account%s","name":"Account"}],"result_info":{"page":%s,"total_pages":2,"total_count":2}}`, page, page)), nil
	})
	if _, err := client.VerifyToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	accounts, err := client.ListAccounts(context.Background())
	if err != nil || len(accounts) != 2 || accounts[1].ID != "account2" {
		t.Fatalf("accounts=%v err=%v", accounts, err)
	}
	for _, body := range []string{
		`{"success":true,"result":[],"result_info":{"page":1,"total_pages":21}}`,
		`{"success":true,"result":[{"id":"one"}],"result_info":{"total_count":2}}`,
	} {
		bounded := testClient(t, func(*http.Request) (*http.Response, error) { return rawResponse(200, body), nil })
		if items, err := bounded.ListAccounts(context.Background()); err == nil || items != nil {
			t.Fatal("silently truncated account list")
		}
	}
	expired := testClient(t, func(*http.Request) (*http.Response, error) {
		return resultResponse(map[string]any{"status": "active", "expires_on": time.Now().Add(-time.Hour)}), nil
	})
	_, err = expired.VerifyToken(context.Background())
	failureKind(t, err, "inactive_token", false)
}

func TestTunnelUsesCurrentConfigSource(t *testing.T) {
	for _, test := range []struct {
		body   string
		remote bool
	}{
		{`{"id":"tunnel","config_src":"cloudflare"}`, true},
		{`{"id":"tunnel","config_src":"local","remote_config":true}`, false},
		{`{"id":"tunnel","remote_config":true}`, true},
	} {
		client := testClient(t, func(*http.Request) (*http.Response, error) {
			return rawResponse(200, `{"success":true,"result":`+test.body+`}`), nil
		})
		result, err := client.GetTunnel(context.Background(), "account", "tunnel")
		if err != nil || result.RemoteConfig != test.remote {
			t.Fatalf("current tunnel source ignored: %v err=%v", result, err)
		}
	}
}
