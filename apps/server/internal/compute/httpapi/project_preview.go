package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"net/http"
	"net/http/httputil"
)

func registerProjectPreview(mux *http.ServeMux, service *control.Service) {
	mux.HandleFunc("/v1/project-preview", func(w http.ResponseWriter, r *http.Request) {
		var binding compute.ProjectBinding
		data, err := base64.RawURLEncoding.DecodeString(r.Header.Get(protocol.PreviewBindingHeader))
		if err != nil || len(data) > 4096 || json.Unmarshal(data, &binding) != nil || binding.HostID != r.Header.Get("X-AXV-Host-ID") {
			failure(w, compute.Problem("invalid_request", "The original preview binding is required."))
			return
		}
		access, scope, err := service.ProjectPreviewAccess(r.Context(), binding)
		if err != nil {
			failure(w, err)
			return
		}
		token, err := access.Driver.GuestToken(access.Machine)
		if err != nil {
			failure(w, err)
			return
		}
		encoded, _ := json.Marshal(scope)
		transport := compute.GuestTransport(access.Machine, access.Driver)
		defer transport.CloseIdleConnections()
		proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL.Scheme, p.Out.URL.Host, p.Out.Host = "http", "guest", "guest"
			p.Out.URL.Path, p.Out.URL.RawPath = "/v1/project-preview", ""
			p.Out.Header.Del(protocol.PreviewBindingHeader)
			p.Out.Header.Set(protocol.PreviewScopeHeader, base64.RawURLEncoding.EncodeToString(encoded))
			p.Out.Header.Set("Authorization", "Bearer "+token)
		}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			accessProblem(w, 503, "preview_unavailable", "The original guest preview is unavailable.")
		}}
		proxy.ServeHTTP(w, r)
	})
}
