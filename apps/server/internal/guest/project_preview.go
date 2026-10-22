package guest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *service) projectPreview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	r = r.WithContext(ctx)
	var scope protocol.ProjectFileScope
	data, err := base64.RawURLEncoding.DecodeString(r.Header.Get(protocol.PreviewScopeHeader))
	port, portErr := strconv.Atoi(r.Header.Get(protocol.PreviewPortHeader))
	path := r.Header.Get(protocol.PreviewPathHeader)
	decoded, pathErr := url.PathUnescape(path)
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &scope) != nil || portErr != nil || port < 1024 || port > 65535 || pathErr != nil || !strings.HasPrefix(decoded, "/") || strings.ContainsAny(decoded, "\r\n\x00") {
		fail(w, 400, "invalid_request", fmt.Errorf("A valid original preview scope, path and unprivileged TCP port are required"))
		return
	}
	root, _, _, err := s.openProjectFiles(scope, false)
	if err != nil {
		projectFailure(w, err)
		return
	}
	root.Close()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 15 * time.Second}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
		applicationAuthorization := p.In.Header.Get(protocol.PreviewAuthorizationHeader)
		p.Out.URL.Scheme, p.Out.URL.Host = "http", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		p.Out.Host = p.Out.URL.Host
		p.Out.URL.Path, p.Out.URL.RawPath = decoded, path
		for key := range p.Out.Header {
			if strings.HasPrefix(http.CanonicalHeaderKey(key), "X-Axv-") {
				p.Out.Header.Del(key)
			}
		}
		p.Out.Header.Del("Authorization")
		if applicationAuthorization != "" {
			p.Out.Header.Set("Authorization", applicationAuthorization)
		}
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		fail(w, 503, "preview_unavailable", fmt.Errorf("The registered development service is not listening on its guest loopback port"))
	}}
	proxy.ServeHTTP(w, r)
}
