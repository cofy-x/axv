package localaccess

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticatedRemoteGrantCannotMoveToAnotherTransport(t *testing.T) {
	const origin = "https://axv.example.ts.net"
	r := httptest.NewRequest(http.MethodPost, origin+"/api/projects", nil)
	r.RemoteAddr = "100.101.102.103:45678"
	r.Header.Set("Origin", origin)
	if Request(r, true) || AllowRequest(r, true) {
		t.Fatal("an unauthenticated remote request passed local access checks")
	}
	authenticated, err := WithAuthenticatedRemote(r, origin)
	if err != nil {
		t.Fatal(err)
	}
	if !AllowRequest(authenticated, true) || Request(authenticated, true) {
		t.Fatal("explicit remote authorization changed the local-only primitive")
	}
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"another peer", func(r *http.Request) { r.RemoteAddr = "100.101.102.104:45678" }},
		{"another port", func(r *http.Request) { r.RemoteAddr = "100.101.102.103:45679" }},
		{"another host", func(r *http.Request) { r.Host = "other.example.ts.net" }},
		{"another TLS connection", func(r *http.Request) { r.TLS = &tls.ConnectionState{} }},
		{"plain HTTP", func(r *http.Request) { r.TLS = nil }},
		{"another origin", func(r *http.Request) { r.Header.Set("Origin", "https://other.example.ts.net") }},
		{"missing required origin", func(r *http.Request) { r.Header.Del("Origin") }},
		{"duplicate origin", func(r *http.Request) { r.Header.Add("Origin", origin) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := authenticated.Clone(authenticated.Context())
			tc.change(copy)
			if AllowRequest(copy, true) {
				t.Fatal("authenticated context authorized another transport or origin")
			}
		})
	}
	read := authenticated.Clone(authenticated.Context())
	read.Header.Del("Origin")
	if !AllowRequest(read, false) {
		t.Fatal("authorized read without a supplied Origin rejected")
	}
	ctx, cancel := context.WithCancel(authenticated.Context())
	cancel()
	if AllowRequest(authenticated.WithContext(ctx), false) {
		t.Fatal("cancelled remote authority remained usable")
	}
}

func TestForwardedHeadersCannotMintRemoteAuthority(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://axv.example.ts.net/api/projects", nil)
	r.RemoteAddr = "100.101.102.103:45678"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Forwarded-Host", "localhost")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Tailscale-User-ID", "123")
	if AllowRequest(r, false) {
		t.Fatal("forwarded headers authorized a remote request")
	}
	if _, err := WithAuthenticatedRemote(r, "https://axv.example.ts.net"); err == nil {
		t.Fatal("plain HTTP acquired HTTPS authority")
	}
	local := httptest.NewRequest(http.MethodGet, "http://localhost:3001/api/projects", nil)
	local.RemoteAddr = "127.0.0.1:45678"
	if !Request(local, false) || !AllowRequest(local, false) {
		t.Fatal("local policy changed without a remote context")
	}
}

func TestAuthenticatedTunnelPreservesPublicAndLocalBoundaries(t *testing.T) {
	const origin = "https://axv.example.com"
	r := httptest.NewRequest(http.MethodPost, origin+"/api/projects", nil)
	r.TLS = nil // cloudflared's actual loopback HTTP hop, not synthetic TLS.
	r.RemoteAddr = "127.0.0.1:45678"
	r.Header.Set("Origin", origin)
	if Request(r, true) || AllowRequest(r, true) {
		t.Fatal("loopback connector alone established remote authority")
	}
	authorized, err := WithAuthenticatedTunnel(r, origin)
	if err != nil {
		t.Fatal(err)
	}
	if !AllowRequest(authorized, true) || Request(authorized, true) || authorized.TLS != nil {
		t.Fatal("tunnel authority changed local checks or invented TLS")
	}
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"remote peer", func(r *http.Request) { r.RemoteAddr = "192.0.2.1:45678" }},
		{"different connection", func(r *http.Request) { r.RemoteAddr = "127.0.0.1:45679" }},
		{"local host", func(r *http.Request) { r.Host = "localhost:3001" }},
		{"another host", func(r *http.Request) { r.Host = "preview.example.com" }},
		{"changed transport", func(r *http.Request) { r.TLS = &tls.ConnectionState{} }},
		{"wrong origin", func(r *http.Request) { r.Header.Set("Origin", "https://preview.example.com") }},
		{"missing required origin", func(r *http.Request) { r.Header.Del("Origin") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := authorized.Clone(authorized.Context())
			tc.change(copy)
			if AllowRequest(copy, true) {
				t.Fatal("tunnel authority moved to another request boundary")
			}
		})
	}
	read := authorized.Clone(authorized.Context())
	read.Header.Del("Origin")
	if !AllowRequest(read, false) || AllowRequest(read, true) {
		t.Fatal("tunnel read/mutation Origin policy changed")
	}
	ctx, cancel := context.WithCancel(authorized.Context())
	cancel()
	if AllowRequest(authorized.WithContext(ctx), false) {
		t.Fatal("cancelled tunnel authority remained usable")
	}
	for _, tc := range []struct {
		origin, peer string
		tls          bool
	}{
		{"https://localhost", "127.0.0.1:45678", false},
		{"https://127.0.0.1", "127.0.0.1:45678", false},
		{"https://preview.localhost", "127.0.0.1:45678", false},
		{origin, "192.0.2.1:45678", false},
		{origin, "127.0.0.1:45678", true},
	} {
		candidate := httptest.NewRequest(http.MethodGet, tc.origin+"/api/projects", nil)
		candidate.RemoteAddr = tc.peer
		if !tc.tls {
			candidate.TLS = nil
		}
		if _, err := WithAuthenticatedTunnel(candidate, tc.origin); err == nil {
			t.Fatalf("invalid tunnel boundary accepted: %+v", tc)
		}
	}
}
