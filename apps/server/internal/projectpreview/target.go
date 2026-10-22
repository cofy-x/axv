package projectpreview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func GatewayResolver(g *gateway.Gateway) Resolver {
	return func(ctx context.Context, binding compute.ProjectBinding, port int) (Target, error) {
		captured, err := g.CaptureProject(ctx, binding)
		if err != nil {
			return Target{}, err
		}
		client, err := computeclient.Connect(ctx, captured.Socket, computeclient.Binding{HostID: binding.HostID, ConfigFingerprint: captured.ConfigFingerprint})
		if err != nil {
			return Target{}, err
		}
		transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return client.Dial(ctx) }, ResponseHeaderTimeout: 15 * time.Second}
		encoded, _ := json.Marshal(binding)
		proxy := &httputil.ReverseProxy{Transport: transport, ModifyResponse: applicationResponse, Rewrite: func(p *httputil.ProxyRequest) {
			path := p.In.URL.EscapedPath()
			p.Out.URL.Scheme, p.Out.URL.Host, p.Out.Host = "http", "host", "host"
			p.Out.URL.Path, p.Out.URL.RawPath = "/v1/project-preview", ""
			for key := range p.Out.Header {
				if strings.HasPrefix(http.CanonicalHeaderKey(key), "X-Axv-") {
					p.Out.Header.Del(key)
				}
			}
			p.Out.Header.Del("Authorization")
			if value := p.In.Header.Get("Authorization"); value != "" {
				p.Out.Header.Set(protocol.PreviewAuthorizationHeader, value)
			}
			p.Out.Header.Set("X-AXV-Host-ID", binding.HostID)
			p.Out.Header.Set(protocol.PreviewBindingHeader, base64.RawURLEncoding.EncodeToString(encoded))
			p.Out.Header.Set(protocol.PreviewPortHeader, strconv.Itoa(port))
			p.Out.Header.Set(protocol.PreviewPathHeader, path)
		}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "The original VM preview is unavailable.", 503)
		}}
		return Target{Handler: proxy, Validate: func(ctx context.Context) error {
			current, err := client.ProjectBinding(ctx, binding.ProjectID)
			if err == nil && current.Binding != binding {
				err = errors.New("The preview environment changed. Register a new preview explicitly.")
			}
			return err
		}, Close: func() { transport.CloseIdleConnections(); client.Close() }}, nil
	}
}

func applicationResponse(response *http.Response) error {
	cookies := response.Cookies()
	response.Header.Del("Set-Cookie")
	for _, cookie := range cookies {
		if strings.HasPrefix(cookie.Name, "axv_preview_") {
			continue
		}
		cookie.Domain = ""
		response.Header.Add("Set-Cookie", cookie.String())
	}
	response.Header.Del("Clear-Site-Data")
	return nil
}
