// Package gateway exposes the verified local compute host to the desktop.
package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/hostconn"
	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

type GatewayOptions struct {
	Logger      *diagnostics.Logger
	LogPath     string
	ConfigPath  string
	ProfileHome string
}

type RuntimeStatus struct {
	Code       string `json:"code,omitempty"`
	State      string `json:"state"`
	Mode       string `json:"mode"`
	Error      string `json:"error,omitempty"`
	HostID     string `json:"hostId,omitempty"`
	Generation uint64 `json:"generation"`
	ReadOnly   bool   `json:"readOnly"`
}

type Environment struct {
	LogPath                 string         `json:"logPath,omitempty"`
	LoggingError            string         `json:"loggingError,omitempty"`
	Preparation             *prepare.Check `json:"preparation,omitempty"`
	ConfigurationSource     string         `json:"configurationSource,omitempty"`
	AvailableActions        []string       `json:"availableActions,omitempty"`
	Runtime                 *RuntimeStatus `json:"runtime,omitempty"`
	Platform                string         `json:"platform"`
	Configured              bool           `json:"configured"`
	ConfigPath              string         `json:"configPath"`
	ProfileHome             string         `json:"profileHome"`
	ExecutablePath          string         `json:"executablePath"`
	RequiredLimaVersion     string         `json:"requiredLimaVersion"`
	AutomaticSetupSupported bool           `json:"automaticSetupSupported"`
}

type Gateway struct {
	logger      *diagnostics.Logger
	preparation func() prepare.Check
	binding     *gatewayBinding
	bindings    []*gatewayBinding
	generation  uint64
	environment Environment
	mu          sync.Mutex
	connections map[*trackedConn]bool
	closed      bool
}

type gatewayBinding struct {
	errorMu            sync.Mutex
	lastTransportError string
	socket             string
	hostID             string
	fingerprint        string
	generation         uint64
	proxy              *httputil.ReverseProxy
	transport          *http.Transport
}

type trackedConn struct {
	net.Conn
	owner *Gateway
}

func (c *trackedConn) Close() error {
	c.owner.mu.Lock()
	delete(c.owner.connections, c)
	c.owner.mu.Unlock()
	return c.Conn.Close()
}

func NewGateway(socket string, options GatewayOptions) *Gateway {
	executablePath, _ := os.Executable()
	g := &Gateway{
		logger: options.Logger,
		environment: Environment{
			Platform:                runtime.GOOS,
			LogPath:                 options.LogPath,
			Configured:              socket != "",
			ConfigPath:              options.ConfigPath,
			ProfileHome:             options.ProfileHome,
			ExecutablePath:          executablePath,
			RequiredLimaVersion:     compute.SupportedLimaVersion,
			AutomaticSetupSupported: runtime.GOOS == "darwin",
		},
		connections: map[*trackedConn]bool{},
	}
	if socket != "" {
		g.Bind(socket, "", "")
	}
	return g
}

// Bind publishes an immutable routing generation. In-flight requests and guest
// attachments retain the transport that accepted them.
func (g *Gateway) Bind(socket, hostID, fingerprint string) uint64 {
	g.mu.Lock()
	if g.closed {
		generation := g.generation
		g.mu.Unlock()
		return generation
	}
	if b := g.binding; b != nil && b.socket == socket && b.hostID == hostID && b.fingerprint == fingerprint {
		g.mu.Unlock()
		return b.generation
	}
	g.generation++
	b := &gatewayBinding{socket: socket, hostID: hostID, fingerprint: fingerprint, generation: g.generation}
	b.transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
		if err != nil {
			return nil, err
		}
		if hostID != "" {
			verified, err := hostconn.Verify(ctx, conn, hostconn.Binding{HostID: hostID, ConfigFingerprint: fingerprint})
			if err != nil {
				conn.Close()
				return nil, err
			}
			conn = verified
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.closed {
			conn.Close()
			return nil, net.ErrClosed
		}
		tracked := &trackedConn{Conn: conn, owner: g}
		g.connections[tracked] = true
		return tracked, nil
	}}
	b.proxy = &httputil.ReverseProxy{Transport: b.transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.Out.URL.Scheme, p.Out.URL.Host, p.Out.Host = "http", "host", "host"
		p.Out.URL.Path = "/v1/" + strings.TrimPrefix(p.In.URL.Path, "/api/compute/")
		p.Out.URL.RawPath = "/v1/" + strings.TrimPrefix(p.In.URL.EscapedPath(), "/api/compute/")
		p.Out.Header.Del("Authorization")
		p.Out.Header.Del("Cookie")
	}, ModifyResponse: func(_ *http.Response) error {
		b.errorMu.Lock()
		b.lastTransportError = ""
		b.errorMu.Unlock()
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		b.errorMu.Lock()
		changed := err.Error() != b.lastTransportError
		b.lastTransportError = err.Error()
		b.errorMu.Unlock()
		if changed || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			g.logger.Error("compute.transport_failed", err, "hostId", hostID, "generation", b.generation, "method", r.Method)
		}
		write(w, 503, map[string]string{"code": "host_unavailable", "error": "The local VM service is unavailable. Check its status on the computer running AXV; existing VMs have not been stopped."})
	}}
	previous := g.binding
	g.binding = b
	g.bindings = append(g.bindings, b)
	g.environment.Configured = true
	g.mu.Unlock()
	if previous != nil {
		previous.transport.CloseIdleConnections()
	}
	return b.generation
}

func (g *Gateway) SetRuntime(status RuntimeStatus) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.environment.Runtime = &status
	g.environment.Configured = status.Mode != "" && status.Mode != "disabled"
}

// SetPreparationSnapshot attaches the desktop-owned engine's read-only snapshot.
func (g *Gateway) SetPreparationSnapshot(snapshot func() prepare.Check) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.preparation = snapshot
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// SSH authentication material and raw streams belong exclusively to the
	// private host socket. They are never part of the browser compute gateway.
	if privateGuestAccess(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	if !local(r) {
		write(w, 403, map[string]string{"code": "compute_forbidden", "error": "Machine control requires a same-origin connection from this computer."})
		return
	}
	g.mu.Lock()
	binding, environment, preparation := g.binding, g.environment, g.preparation
	g.mu.Unlock()
	if r.URL.Path == "/api/compute/environment" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			write(w, http.StatusMethodNotAllowed, map[string]string{"code": "method_not_allowed", "error": "Compute environment only supports GET."})
			return
		}
		if preparation != nil {
			check := preparation()
			environment.Preparation = &check
			environment.ConfigurationSource = check.ConfigurationSource
			if check.ActiveJob != nil {
				if check.ActiveJob.CanCancel {
					environment.AvailableActions = append(environment.AvailableActions, "cancel")
				}
			} else {
				if check.LockedReason == "" {
					environment.AvailableActions = append(environment.AvailableActions, "review")
				}
				if check.LockedReason == "" && check.LatestJob != nil && check.LatestJob.CanRetry {
					environment.AvailableActions = append(environment.AvailableActions, "retry")
				}
			}
		}
		environment.LoggingError = g.logger.Status()
		write(w, http.StatusOK, environment)
		return
	}
	if binding == nil {
		if r.Method == "GET" && r.URL.Path == "/api/compute/info" {
			write(w, 200, map[string]any{"configured": false, "reason": "VM hosting is not configured on the computer running AXV. Follow the setup instructions for this platform."})
			return
		}
		write(w, 503, map[string]string{"code": "host_unavailable", "error": "No AXV host service is configured."})
		return
	}
	generation := strconv.FormatUint(binding.generation, 10)
	w.Header().Set("X-AXV-Compute-Generation", generation)
	w.Header().Set("X-AXV-Host-ID", binding.hostID)
	expectedHost := r.Header.Get("X-AXV-Host-ID")
	if expectedHost == "" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		expectedHost = r.URL.Query().Get("hostId")
	}
	if expectedHost != "" && expectedHost != binding.hostID {
		write(w, http.StatusConflict, map[string]string{"code": "binding_changed", "error": "This view belongs to a different VM host. Its commands were not sent."})
		return
	}
	expected := r.Header.Get("X-AXV-Compute-Generation")
	if expected == "" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		expected = r.URL.Query().Get("generation")
	}
	if expected != "" && expected != generation {
		write(w, http.StatusConflict, map[string]string{"code": "binding_changed", "error": "The VM service connection changed. Refresh this view before sending another command."})
		return
	}
	binding.proxy.ServeHTTP(w, r)
}

func privateGuestAccess(value string) bool {
	value = strings.TrimPrefix(value, "/api/compute/")
	parts := strings.Split(strings.TrimPrefix(path.Clean("/"+value), "/"), "/")
	return len(parts) >= 3 && parts[0] == "machines" && parts[2] == "ssh"
}

func (g *Gateway) Close() {
	g.mu.Lock()
	g.closed = true
	connections := make([]*trackedConn, 0, len(g.connections))
	for c := range g.connections {
		connections = append(connections, c)
	}
	bindings := append([]*gatewayBinding{}, g.bindings...)
	g.mu.Unlock()
	for _, c := range connections {
		c.Close()
	}
	for _, b := range bindings {
		b.transport.CloseIdleConnections()
	}
}

func local(r *http.Request) bool {
	requiresOrigin := r.Method != "GET" && r.Method != "HEAD" || strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	return localaccess.Request(r, requiresOrigin)
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
