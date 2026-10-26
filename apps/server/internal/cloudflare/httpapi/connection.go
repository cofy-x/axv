package httpapi

import (
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/desktop"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
)

type ConnectionStatus struct {
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	State      string `json:"state"`
	URL        string `json:"url,omitempty"`
	Message    string `json:"message,omitempty"`
}

func Connection(status desktop.Snapshot) ConnectionStatus {
	result := ConnectionStatus{Enabled: status.Enabled, Configured: status.Configured, State: status.State}
	switch status.State {
	case "disabled":
		result.Message = "Cloudflare access is disabled in this profile."
	case "starting":
		result.Message = "The authenticated Cloudflare ingress is starting."
	case "listening":
		if policy.ValidOrigin(status.URL) {
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
