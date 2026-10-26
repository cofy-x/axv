package access

import (
	"net/http"
	"strings"
)

// StripCredentials confines Cloudflare credentials to this authentication
// layer. Shared features may proxy ordinary application headers to compute or
// guest services, which must never receive the workstation's Access credentials.
// Clone first so authentication does not mutate the original inbound request.
func StripCredentials(r *http.Request) *http.Request {
	clean := r.Clone(r.Context())
	for name, values := range clean.Header {
		if strings.HasPrefix(strings.ToLower(name), "cf-access-") {
			delete(clean.Header, name)
			continue
		}
		if !strings.EqualFold(name, "Cookie") {
			continue
		}
		filtered := make([]string, 0, len(values))
		for _, value := range values {
			var kept []string
			for _, pair := range strings.Split(value, ";") {
				pair = strings.TrimSpace(pair)
				cookieName, _, _ := strings.Cut(pair, "=")
				switch strings.ToLower(strings.TrimSpace(cookieName)) {
				case "cf_authorization", "cf_binding", "cf_session", "cf_appsession", "cf_device":
					continue
				}
				if pair != "" {
					kept = append(kept, pair)
				}
			}
			if len(kept) != 0 {
				filtered = append(filtered, strings.Join(kept, "; "))
			}
		}
		if len(filtered) == 0 {
			delete(clean.Header, name)
		} else {
			clean.Header[name] = filtered
		}
	}
	return clean
}
