package httpaccess

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

type authenticatedRemoteKey struct{}

type authenticatedRemote struct {
	peer   string
	host   string
	origin string
	tls    *tls.ConnectionState
	tunnel bool
}

// WithAuthenticatedRemote records an already authorized remote transport. Only
// trusted authentication middleware may call this after verifying the actual
// peer. HTTP headers cannot create this private context value. The authorization
// remains bound to this peer, TLS connection, Host and exact HTTPS origin.
func WithAuthenticatedRemote(r *http.Request, origin string) (*http.Request, error) {
	return withAuthenticatedTransport(r, origin, false)
}

// WithAuthenticatedTunnel records authority established by a trusted tunnel
// verifier on its dedicated loopback HTTP listener. The public HTTPS
// origin stays intact; neither forwarded headers nor a rewritten local Host
// can create this authority. This does not authorize machine-local operations.
func WithAuthenticatedTunnel(r *http.Request, origin string) (*http.Request, error) {
	return withAuthenticatedTransport(r, origin, true)
}

func withAuthenticatedTransport(r *http.Request, origin string, tunnel bool) (*http.Request, error) {
	u, err := url.Parse(origin)
	if err != nil || u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.RawFragment != "" || u.String() != origin || strings.HasSuffix(u.Host, ":") {
		return nil, fmt.Errorf("authenticated remote access requires an exact HTTPS origin")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("authenticated remote access requires a valid HTTPS port")
		}
	}
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || r.Host != u.Host {
		return nil, fmt.Errorf("authenticated remote access does not match this request")
	}
	if tunnel {
		// The connector runs on the deployment machine. Public Host validation
		// also ensures LocalRequest continues to reject this as local authority.
		if !peer.Addr().IsLoopback() || r.TLS != nil || localHost(r.Host) ||
			strings.HasSuffix(u.Hostname(), ".localhost") {
			return nil, fmt.Errorf("authenticated tunnel access requires its loopback HTTP connection and public Host")
		}
	} else if r.TLS == nil {
		return nil, fmt.Errorf("authenticated remote access requires TLS")
	}
	grant := authenticatedRemote{peer: r.RemoteAddr, host: r.Host, origin: origin, tls: r.TLS, tunnel: tunnel}
	return r.WithContext(context.WithValue(r.Context(), authenticatedRemoteKey{}, grant)), nil
}

// AllowRequest preserves LocalRequest's local checks while allowing a remote request
// that trusted middleware explicitly authenticated. Preparation and other
// machine-local controls continue to use LocalRequest directly.
func AllowRequest(r *http.Request, requireOrigin bool) bool {
	grant, ok := r.Context().Value(authenticatedRemoteKey{}).(authenticatedRemote)
	if !ok {
		return LocalRequest(r, requireOrigin)
	}
	if r.Context().Err() != nil || (!grant.tunnel && r.TLS == nil) || r.TLS != grant.tls || r.RemoteAddr != grant.peer || r.Host != grant.host {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return !requireOrigin
	}
	return len(origins) == 1 && origins[0] == grant.origin
}
