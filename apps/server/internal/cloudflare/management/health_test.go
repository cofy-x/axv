package management

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type healthTransport func(*http.Request) (*http.Response, error)

func (f healthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInterruptedHealthNeverCachesAnObsoleteObservation(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		name := "configuration changed"
		if cancelled {
			name = "request cancelled"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			settings, revision, err := profile.ReadCloudflareConfiguration(home)
			if err != nil {
				t.Fatal(err)
			}
			settings.Management = policy.Account{APIToken: "private-api-token", AccountID: "account", ZoneID: "zone", TunnelID: "tunnel", Domain: "example.net", Listen: policy.DefaultPublishListen}
			lock, err := profile.Lock(home)
			if err != nil {
				t.Fatal(err)
			}
			_, revision, err = profile.SaveCloudflareConfiguration(home, revision, settings)
			_ = lock.Close()
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			finish := sync.OnceFunc(func() { close(release) })
			defer finish()
			service := New(context.Background(), Options{Home: home, Client: func(token string) (*cfclient.Client, error) {
				return cfclient.New(cfclient.Options{Token: token, HTTPClient: &http.Client{Transport: healthTransport(func(r *http.Request) (*http.Response, error) {
					body := `{"success":true,"result":[]}`
					switch r.URL.Path {
					case "/client/v4/user/tokens/verify":
						close(entered)
						select {
						case <-release:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
						body = `{"success":true,"result":{"id":"token","status":"active"}}`
					case "/client/v4/accounts/account/cfd_tunnel/tunnel":
						body = `{"success":true,"result":{"id":"tunnel","status":"healthy","remote_config":true}}`
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}})
			}})
			defer service.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := service.Health(ctx, false); done <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("health check did not reach the controlled provider")
			}
			if cancelled {
				cancel()
			} else {
				lock, err := profile.Lock(home)
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = profile.SaveCloudflareAPIToken(home, revision, "rotated-api-token")
				_ = lock.Close()
				if err != nil {
					t.Fatal(err)
				}
				finish()
			}
			select {
			case err := <-done:
				var failure *Problem
				if cancelled && !errors.Is(err, context.Canceled) || !cancelled && (!errors.As(err, &failure) || failure.Code != "cloudflare_config_conflict") {
					t.Fatalf("interrupted health returned the wrong result: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("health check did not finish")
			}
			if !service.health.CheckedAt.IsZero() || service.healthRevision != "" {
				t.Fatal("interrupted health changed the current cache")
			}
		})
	}
}
