package webpush

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const requestTimeout = 10 * time.Second

// Sender owns a fixed policy rather than accepting an arbitrary HTTP client.
// Its unexported dependencies allow transport tests without exposing an unsafe
// destination override to application configuration.
type Sender struct {
	lookupIP       func(context.Context, string, string) ([]netip.Addr, error)
	dialContext    func(context.Context, string, string) (net.Conn, error)
	tlsConfig      *tls.Config
	authMu         sync.Mutex
	authorizations map[authorizationScope]authorization
}

func NewSender() *Sender {
	dialer := &net.Dialer{Timeout: requestTimeout}
	return &Sender{lookupIP: net.DefaultResolver.LookupNetIP, dialContext: dialer.DialContext}
}

var errUnsafeAddress = errors.New("unsafe Web Push service address")

func (s *Sender) client(ctx context.Context, hostname string) (*http.Client, func(), error) {
	addresses, err := s.lookupIP(ctx, "ip", hostname)
	if err != nil {
		return nil, nil, err
	}
	if len(addresses) == 0 || len(addresses) > 16 {
		return nil, nil, errUnsafeAddress
	}
	// Validate every answer before dialing any. The connection uses only this
	// checked snapshot, preventing a second resolution or DNS rebinding.
	for _, address := range addresses {
		if !publicAddress(address) {
			return nil, nil, errUnsafeAddress
		}
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.tlsConfig != nil {
		config = s.tlsConfig.Clone()
	}
	config.ServerName = hostname
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || !strings.EqualFold(host, hostname) || port != "443" {
				return nil, errUnsafeAddress
			}
			var dialErr error
			for _, ip := range addresses {
				connection, err := s.dialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return connection, nil
				}
				dialErr = err
				if ctx.Err() != nil {
					break
				}
			}
			return nil, dialErr
		},
		TLSClientConfig:        config,
		TLSHandshakeTimeout:    requestTimeout,
		ResponseHeaderTimeout:  requestTimeout,
		MaxResponseHeaderBytes: 16 << 10,
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      true,
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return client, transport.CloseIdleConnections, nil
}

var deniedIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

var globalIPv6 = netip.MustParsePrefix("2000::/3")
var deniedIPv6 = []netip.Prefix{
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicAddress(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" || address.Is4In6() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() {
		return false
	}
	denied := deniedIPv4
	if address.Is6() {
		if !globalIPv6.Contains(address) {
			return false
		}
		denied = deniedIPv6
	}
	for _, prefix := range denied {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
