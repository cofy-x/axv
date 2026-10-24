package cloudflarepublish

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
	"github.com/cofy-x/axv/apps/server/internal/projectpreview"
)

func gatewayCapture(g *gateway.Gateway) func(context.Context, compute.ProjectBinding) (CapturedRoute, error) {
	return func(ctx context.Context, binding compute.ProjectBinding) (CapturedRoute, error) {
		if g == nil {
			return CapturedRoute{}, errors.New("the VM gateway is unavailable")
		}
		captured, err := g.CaptureProject(ctx, binding)
		return CapturedRoute{Socket: captured.Socket, ConfigFingerprint: captured.ConfigFingerprint}, err
	}
}

// resolveCaptured never consults the current gateway. The exact private socket
// and host fingerprint were committed with the original publication request.
func resolveCaptured(ctx context.Context, route CapturedRoute, binding compute.ProjectBinding, port int) (projectpreview.Target, error) {
	client, err := computeclient.Connect(ctx, route.Socket, computeclient.Binding{HostID: binding.HostID, ConfigFingerprint: route.ConfigFingerprint})
	if err != nil {
		return projectpreview.Target{}, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return client.Dial(ctx) }, ResponseHeaderTimeout: 15 * time.Second}
	encoded, _ := json.Marshal(binding)
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
		path := p.In.URL.EscapedPath()
		p.Out.URL.Scheme, p.Out.URL.Host, p.Out.Host = "http", "host", "host"
		p.Out.URL.Path, p.Out.URL.RawPath = "/v1/project-preview", ""
		for key := range p.Out.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-axv-") || strings.HasPrefix(strings.ToLower(key), "cf-access-") {
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
	}, ModifyResponse: applicationResponse, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		controlError(w, 503, "publication_unavailable", "The original published guest service is unavailable.")
	}}
	return projectpreview.Target{Handler: proxy, Validate: func(ctx context.Context) error {
		current, err := client.ProjectBinding(ctx, binding.ProjectID)
		if err == nil && current.Binding != binding {
			err = errors.New("the original publication binding changed")
		}
		return err
	}, Close: func() { transport.CloseIdleConnections(); client.Close() }}, nil
}

func applicationResponse(response *http.Response) error {
	cookies := response.Cookies()
	response.Header.Del("Set-Cookie")
	for _, cookie := range cookies {
		if strings.HasPrefix(strings.ToLower(cookie.Name), "axv_") || strings.HasPrefix(strings.ToLower(cookie.Name), "__host-axv-") || accessCookie(cookie.Name) {
			continue
		}
		cookie.Domain = ""
		response.Header.Add("Set-Cookie", cookie.String())
	}
	response.Header.Del("Clear-Site-Data")
	securityHeaders(response.Header)
	return nil
}

func accessCookie(name string) bool {
	switch strings.ToLower(name) {
	case "cf_authorization", "cf_binding", "cf_session", "cf_appsession", "cf_device":
		return true
	}
	return false
}
