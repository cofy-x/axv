// Package accessfixture supplies controlled signing keys and JWTs for Cloudflare tests.
package accessfixture

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

const (
	Origin   = "https://axv.example.com"
	Issuer   = "https://owner.cloudflareaccess.com"
	Owner    = "7335d417-61da-459d-899c-0a01c76a2f94"
	Audience = "4714c1358e65fe4b408ad6d432a5f878f08194bdb4752441fd56faefa9b2b6f2"
)

type Fixture struct {
	server *httptest.Server
	Client *http.Client
	First  *rsa.PrivateKey
	Second *rsa.PrivateKey
	mu     sync.Mutex
	keys   []jose.JSONWebKey
	mode   string
	Calls  atomic.Int64
	Start  chan struct{}
}

func New(t *testing.T) *Fixture {
	t.Helper()
	first, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &Fixture{First: first, Second: second, keys: []jose.JSONWebKey{{Key: &first.PublicKey, KeyID: "first", Algorithm: "RS256", Use: "sig"}}, Start: make(chan struct{}, 20)}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.Calls.Add(1)
		f.Start <- struct{}{}
		if r.URL.Path != "/cdn-cgi/access/certs" || r.Host != "owner.cloudflareaccess.com" {
			t.Errorf("unexpected JWKS request: %s %s", r.Host, r.URL.Path)
		}
		f.mu.Lock()
		mode, keys := f.mode, append([]jose.JSONWebKey(nil), f.keys...)
		f.mu.Unlock()
		switch mode {
		case "redirect":
			http.Redirect(w, r, "https://attacker.example.com/keys", http.StatusFound)
		case "error":
			http.Error(w, "fixture failure", http.StatusInternalServerError)
		case "invalid":
			_, _ = io.WriteString(w, "not a key set")
		case "large":
			_, _ = io.WriteString(w, strings.Repeat(" ", (1<<20)+1))
		case "slow":
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: keys})
		}
	}))
	transport := f.server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "owner.cloudflareaccess.com:443" {
			t.Errorf("unexpected JWKS destination: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, f.server.Listener.Addr().String())
	}
	f.Client = &http.Client{Transport: transport}
	t.Cleanup(func() { transport.CloseIdleConnections(); f.server.Close() })
	return f
}

func (f *Fixture) SetMode(mode string) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

func (f *Fixture) Token(t *testing.T, now time.Time, updates map[string]any) string {
	t.Helper()
	claims := map[string]any{"iss": Issuer, "sub": Owner, "aud": []string{Audience}, "type": "app",
		"exp": now.Add(14 * time.Minute).Unix(), "iat": now.Add(-time.Minute).Unix(), "nbf": now.Add(-time.Minute).Unix(), "email": "ignored@example.com"}
	for key, value := range updates {
		if value == nil {
			delete(claims, key)
		} else {
			claims[key] = value
		}
	}
	return SignedToken(t, f.First, "first", claims)
}

func SignedToken(t *testing.T, key *rsa.PrivateKey, kid string, claims any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	object, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := object.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (f *Fixture) Rotate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = []jose.JSONWebKey{{Key: &f.Second.PublicKey, KeyID: "second", Algorithm: "RS256", Use: "sig"}}
}
