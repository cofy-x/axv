package codex

import (
	"reflect"
	"strings"
	"testing"
)

func TestProxyPrecedenceAndRedaction(t *testing.T) {
	system := []string{"HTTP_PROXY=http://system:9000", "http_proxy=http://system:9000", "HTTPS_PROXY=http://system:9001", "https_proxy=http://system:9001"}
	for _, tc := range []struct {
		name, configured, source, proxy string
		env, want                       []string
	}{
		{name: "explicit environment", env: []string{"HTTPS_PROXY=http://user:secret@environment:8000/private?token=hidden", "NO_PROXY=localhost"}, configured: "http://configured:8080", source: "environment", proxy: "http://environment:8000"},
		{name: "lowercase environment", env: []string{"https_proxy=http://environment:8000"}, configured: "http://configured:8080", source: "environment", proxy: "http://environment:8000"},
		{name: "configuration", env: []string{"NO_PROXY=localhost", "HTTPS_PROXY="}, configured: "http://user:secret@configured:8080", source: "configuration", proxy: "http://configured:8080", want: []string{"NO_PROXY=localhost", "HTTP_PROXY=http://user:secret@configured:8080", "http_proxy=http://user:secret@configured:8080", "HTTPS_PROXY=http://user:secret@configured:8080", "https_proxy=http://user:secret@configured:8080"}},
		{name: "system", env: []string{"NO_PROXY=localhost"}, source: "system", proxy: "http://system:9001", want: append([]string{"NO_PROXY=localhost"}, system...)},
		{name: "socks configuration", configured: "socks5h://proxy:1080", source: "configuration", proxy: "socks5h://proxy:1080", want: []string{"ALL_PROXY=socks5h://proxy:1080", "all_proxy=socks5h://proxy:1080"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			got, network := networkEnvironment(tc.env, tc.configured, func() []string { called = true; return system })
			want := tc.want
			if want == nil {
				want = tc.env
			}
			if !reflect.DeepEqual(got, want) || network.ProxySource != tc.source || network.Proxy != tc.proxy {
				t.Fatal("incorrect proxy precedence or redaction")
			}
			if called != (tc.source == "system") {
				t.Fatal("system inspection bypassed explicit proxy configuration")
			}
		})
	}
	if _, network := networkEnvironment(nil, "", func() []string { return nil }); network.ProxySource != "direct" || network.Proxy != "" {
		t.Fatalf("unexpected direct routing %+v", network)
	}
	if redactedProxy("http://user:secret@bad:port/path") != "" {
		t.Fatal("invalid proxy was exposed")
	}
}

func TestSystemProxyUsesOnlyEnabledValidSettings(t *testing.T) {
	output := `<dictionary> {
  HTTPEnable : 1
  HTTPProxy : 127.0.0.1
  HTTPPort : 7890
  HTTPSEnable : 1
  HTTPSProxy : ::1
  HTTPSPort : 7891
  SOCKSEnable : 1
  SOCKSProxy : ignored
  SOCKSPort : 1080
}`
	want := []string{"HTTP_PROXY=http://127.0.0.1:7890", "http_proxy=http://127.0.0.1:7890", "HTTPS_PROXY=http://[::1]:7891", "https_proxy=http://[::1]:7891"}
	if got := parseSystemProxy(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("system proxies = %v", got)
	}
	if got := parseSystemProxy(strings.ReplaceAll(output, "Enable : 1", "Enable : 0")); len(got) != 0 {
		t.Fatal("disabled system proxy applied")
	}
	if got := parseSystemProxy("HTTPEnable : 1\nHTTPProxy : host/secret\nHTTPPort : 70000"); len(got) != 0 {
		t.Fatal("invalid system proxy applied")
	}
}
