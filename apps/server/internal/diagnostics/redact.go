package diagnostics

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var urls = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s<>"']+`)
var authorization = regexp.MustCompile(`(?i)(authorization[\s:=]+)(?:bearer|basic)[\s]+[^\s,;]+`)
var credentials = regexp.MustCompile(`(?i)(authorization|bearer|token|password|secret|api[_-]?key)([\s:=]+)[^\s,;]+`)

func environmentSecrets() []string {
	var out []string
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		k = strings.ToUpper(k)
		if !ok {
			continue
		}
		if len(v) >= 4 && (strings.Contains(k, "TOKEN") || strings.Contains(k, "SECRET") || strings.Contains(k, "PASSWORD") || strings.HasSuffix(k, "KEY")) {
			out = append(out, v)
		}
		if strings.HasSuffix(k, "_PROXY") {
			if u, err := url.Parse(v); err == nil && u.User != nil {
				if password, ok := u.User.Password(); ok && len(password) >= 4 {
					out = append(out, password)
				}
				if user := u.User.String(); len(user) >= 4 {
					out = append(out, user)
				}
			}
		}
	}
	return out
}
func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	value = urls.ReplaceAllStringFunc(value, func(raw string) string {
		u, e := url.Parse(raw)
		if e != nil {
			return "[REDACTED URL]"
		}
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		return u.String()
	})
	value = authorization.ReplaceAllString(value, "$1[REDACTED]")
	value = credentials.ReplaceAllString(value, "$1$2[REDACTED]")
	if len(value) > 8192 {
		value = value[:8192] + "…[truncated]"
	}
	return value
}
func sanitize(v any, secrets []string) any {
	switch v := v.(type) {
	case string:
		return redact(v, secrets)
	case error:
		return redact(v.Error(), secrets)
	case bool, int, int64, uint64, float64, float32, time.Duration:
		return v
	case nil:
		return nil
	default:
		return fmt.Sprintf("[%T omitted]", v)
	}
}

func sensitiveKey(k string) bool {
	k = strings.ToLower(k)
	return k == "authorization" || k == "cookie" || strings.Contains(k, "password") || strings.Contains(k, "secret") || strings.Contains(k, "token") || k == "apikey" || k == "api_key"
}
