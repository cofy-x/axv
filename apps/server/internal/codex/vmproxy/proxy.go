// Package vmproxy implements a zero-credential host-side LLM API reverse proxy
// for isolated guest VMs. Real API keys remain on the host and are never
// written into VM disks, environment variables, or logs.
package vmproxy

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// ProxyToken is the placeholder bearer credential used inside the guest VM.
	ProxyToken     = "axv-vm-proxy"
	DefaultBaseURL = "https://api.openai.com/v1"
)

// Config controls the host-side upstream LLM endpoint and credential injection.
type Config struct {
	APIKey                string
	BaseURL               string
	Source                string
	AllowLoopbackUpstream bool
}

// Status reports non-sensitive proxy configuration for UI and diagnostics.
type Status struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl,omitempty"`
	Source  string `json:"source"`
}

// FromEnvironment resolves optional host API proxy settings without exposing secrets.
func FromEnvironment() Config {
	apiKey := strings.TrimSpace(os.Getenv("AXV_VM_CODEX_API_KEY"))
	source := "axv_env"
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		source = "openai_env"
	}
	if apiKey == "" {
		return Config{Source: "none"}
	}
	baseURL := strings.TrimSpace(os.Getenv("AXV_VM_CODEX_BASE_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return Config{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Source:  source,
	}
}

func (c Config) Enabled() bool {
	return strings.TrimSpace(c.APIKey) != ""
}

func (c Config) Status() Status {
	if !c.Enabled() {
		return Status{Enabled: false, Source: "none"}
	}
	base := strings.TrimSpace(c.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	parsed, err := url.Parse(base)
	redacted := ""
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		redacted = (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: strings.TrimRight(parsed.Path, "/")}).String()
	}
	source := c.Source
	if source == "" {
		source = "environment"
	}
	return Status{Enabled: true, BaseURL: redacted, Source: source}
}

func validateUpstream(raw string, allowLoopback bool) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		raw = DefaultBaseURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid VM Codex upstream base URL")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("VM Codex upstream base URL must not contain embedded credentials")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if parsed.Scheme != "https" && !(allowLoopback && parsed.Scheme == "http" && loopback) {
		return nil, fmt.Errorf("VM Codex upstream base URL must use https")
	}
	if !allowLoopback && (loopback || (ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()))) {
		return nil, fmt.Errorf("VM Codex upstream base URL must target a public endpoint")
	}
	return parsed, nil
}

// NewHandler creates an HTTP handler that verifies the guest proxy token,
// strips guest auth/cookies, injects the host API key, and streams responses.
func NewHandler(cfg Config) (http.Handler, error) {
	if !cfg.Enabled() {
		return nil, errors.New("VM Codex API proxy is not configured")
	}
	upstream, err := validateUpstream(cfg.BaseURL, cfg.AllowLoopbackUpstream)
	if err != nil {
		return nil, err
	}
	basePath := strings.TrimRight(upstream.Path, "/")
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = upstream.Scheme
			pr.Out.URL.Host = upstream.Host
			pr.Out.Host = upstream.Host
			inPath := pr.In.URL.Path
			if basePath != "" && strings.HasPrefix(inPath, "/v1/") && strings.HasSuffix(basePath, "/v1") {
				inPath = strings.TrimPrefix(inPath, "/v1")
			}
			pr.Out.URL.Path = basePath + inPath
			pr.Out.URL.RawPath = ""
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, `{"error":{"message":"Host API proxy could not reach the configured model upstream."}}`, http.StatusBadGateway)
		},
	}
	expectedAuth := []byte("Bearer " + ProxyToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), expectedAuth) != 1 {
			http.Error(w, `{"error":{"message":"Guest API proxy authentication failed."}}`, http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	}), nil
}

// ServeUnix listens on a private Unix socket (mode 0600) until ctx is cancelled.
func ServeUnix(ctx context.Context, socketPath string, handler http.Handler) (func(), error) {
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		_ = server.Serve(listener)
	}()
	stop := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}
	go func() {
		<-ctx.Done()
		stop()
	}()
	return stop, nil
}
