package codex

import (
	"context"
	"net"
	"net/url"
	"runtime"
	"strconv"
	"strings"
)

// Network describes routing without exposing credentials or proxy URL parameters.
type Network struct {
	ProxySource string `json:"proxySource"`
	Proxy       string `json:"proxy,omitempty"`
}

var proxyKeys = []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"}

func (m *Manager) environment(ctx context.Context, bin ...string) ([]string, Network) {
	return networkEnvironment(hostEnvironment(bin...), m.settings.ProxyURL, func() []string {
		if runtime.GOOS != "darwin" {
			return nil
		}
		output, err := command(ctx, "/usr/sbin/scutil", "--proxy")
		if err != nil {
			return nil
		}
		return parseSystemProxy(output)
	})
}

func networkEnvironment(env []string, configured string, system func() []string) ([]string, Network) {
	values := make(map[string]string)
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	for _, key := range proxyKeys {
		if values[key] != "" {
			return env, Network{ProxySource: "environment", Proxy: redactedProxy(values[key])}
		}
	}
	additional, source := []string{}, "configuration"
	if configured != "" {
		parsed, _ := url.Parse(configured)
		if parsed != nil && (parsed.Scheme == "socks5" || parsed.Scheme == "socks5h") {
			additional = []string{"ALL_PROXY=" + configured, "all_proxy=" + configured}
		} else {
			additional = []string{"HTTP_PROXY=" + configured, "http_proxy=" + configured, "HTTPS_PROXY=" + configured, "https_proxy=" + configured}
		}
	} else {
		additional, source = system(), "system"
	}
	if len(additional) == 0 {
		return env, Network{ProxySource: "direct"}
	}
	// Replace empty explicit entries without changing any nonempty proxy setting.
	for _, entry := range additional {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	result := make([]string, 0, len(env)+len(additional))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		replaced := false
		for _, extra := range additional {
			extraKey, _, _ := strings.Cut(extra, "=")
			if extraKey == key {
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, entry)
		}
	}
	result = append(result, additional...)
	for _, key := range proxyKeys {
		if values[key] != "" {
			return result, Network{ProxySource: source, Proxy: redactedProxy(values[key])}
		}
	}
	return result, Network{ProxySource: "direct"}
}

func redactedProxy(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
}

// scutil reports enabled HTTP/HTTPS proxy hosts separately. PAC and disabled
// settings are not guessed or executed; users can configure proxy_url explicitly.
func parseSystemProxy(output string) []string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, " : ")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	result := []string{}
	for _, kind := range []string{"HTTP", "HTTPS"} {
		port, err := strconv.Atoi(values[kind+"Port"])
		host := values[kind+"Proxy"]
		if values[kind+"Enable"] != "1" || err != nil || port < 1 || port > 65535 || host == "" || strings.ContainsAny(host, "/@?# \t\r\n") {
			continue
		}
		proxy := (&url.URL{Scheme: "http", Host: net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))}).String()
		result = append(result, kind+"_PROXY="+proxy, strings.ToLower(kind)+"_proxy="+proxy)
	}
	return result
}
