package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/localaccess"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/tailnet"
)

type remoteConnectionStatus struct {
	Enabled   bool          `json:"enabled"`
	Supported bool          `json:"supported"`
	State     tailnet.State `json:"state"`
	URL       string        `json:"url,omitempty"`
	Message   string        `json:"message,omitempty"`
}

func remoteConnection(status remoteSnapshot) remoteConnectionStatus {
	result := remoteConnectionStatus{Enabled: status.Enabled, Supported: status.Supported, State: status.State}
	switch status.State {
	case tailnet.StateDisabled:
		result.Message = "AXV remote access is disabled in this profile."
	case tailnet.StateStarting:
		result.Message = "AXV remote access is starting."
	case tailnet.StateNeedsLogin:
		result.Message = "Complete Tailscale enrollment on the computer running AXV."
	case tailnet.StateNeedsHTTPS:
		result.Message = "AXV is waiting for tailnet HTTPS availability. Check Tailscale HTTPS settings."
	case tailnet.StateReady:
		if parsed, err := url.Parse(status.URL); err == nil && parsed.Scheme == "https" && parsed.User == nil && parsed.Port() == "" && strings.HasSuffix(parsed.Hostname(), ".ts.net") && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == "" && parsed.RawFragment == "" && parsed.Opaque == "" && parsed.Path == "" && parsed.RawPath == "" && parsed.Host == parsed.Hostname() && parsed.String() == status.URL {
			result.URL = parsed.String()
			if status.Error != "" {
				result.Message = "AXV remote access is available but needs attention. Check remote status locally."
			}
		} else {
			result.State = tailnet.StateError
			result.Message = "The AXV remote address could not be verified. Check remote status locally."
		}
	case tailnet.StateStopped:
		result.Message = "AXV remote access is stopped."
	case tailnet.StateError:
		result.Message = "AXV remote access needs attention. Check remote status locally."
	default:
		result.State = tailnet.StateError
		result.Message = "AXV remote status is unavailable."
	}
	return result
}

type cloudflareConnectionStatus struct {
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	State      string `json:"state"`
	URL        string `json:"url,omitempty"`
	Message    string `json:"message,omitempty"`
}

func cloudflareConnection(status cloudflareSnapshot) cloudflareConnectionStatus {
	result := cloudflareConnectionStatus{Enabled: status.Enabled, Configured: status.Configured, State: status.State}
	switch status.State {
	case "disabled":
		result.Message = "Cloudflare access is disabled in this profile."
	case "starting":
		result.Message = "The authenticated Cloudflare ingress is starting."
	case "listening":
		if profile.CloudflareOrigin(status.URL) {
			result.URL = status.URL
			result.Message = "The authenticated local ingress is listening. Configure and verify the external Cloudflare Tunnel and Access policy."
		} else {
			result.State = "error"
			result.Message = "The Cloudflare address could not be verified. Check its status locally."
		}
	case "stopped":
		result.Message = "Cloudflare access is stopped."
	case "error":
		result.Message = "Cloudflare access needs attention. Check its status locally."
	default:
		result.State = "error"
		result.Message = "Cloudflare access status is unavailable."
	}
	return result
}

func connectionHandler(remoteStatus func() remoteSnapshot, cloudflareStatus func() cloudflareSnapshot) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !localaccess.AllowRequest(r, false) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "forbidden", "error": "Connection status requires local or authorized remote access."})
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Local      bool                       `json:"local"`
			Remote     remoteConnectionStatus     `json:"remote"`
			Cloudflare cloudflareConnectionStatus `json:"cloudflare"`
		}{Local: localaccess.Request(r, false), Remote: remoteConnection(remoteStatus()), Cloudflare: cloudflareConnection(cloudflareStatus())})
	})
}
