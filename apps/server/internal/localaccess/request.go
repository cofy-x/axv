// Package localaccess validates access to host capabilities without trusting forwarded headers.
package localaccess

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Request requires a loopback peer and host, and an exact origin when supplied.
func Request(r *http.Request, requireOrigin bool) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !loopbackIP(peer) || !localHost(r.Host) {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return !requireOrigin
	}
	if len(origins) != 1 || origins[0] == "" {
		return false
	}
	u, err := url.Parse(origins[0])
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.String() == origins[0] && u.Scheme == scheme &&
		u.Host == r.Host && u.User == nil && u.Opaque == "" && u.Path == "" &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == ""
}

func loopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func localHost(host string) bool {
	if host == "" || strings.ContainsAny(host, " /\\\t\r\n?#@") {
		return false
	}
	u, err := url.Parse("http://" + host)
	if err != nil || u.Host != host || u.User != nil {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	} else if strings.HasSuffix(host, ":") {
		return false
	}
	return u.Hostname() == "localhost" || loopbackIP(u.Hostname())
}
