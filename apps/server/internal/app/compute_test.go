package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func runtimeFixture(t *testing.T) (Settings, compute.Config) {
	t.Helper()
	home, err := os.MkdirTemp("", "axv-rt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	s := Settings{Inputs: launch.Inputs{Home: home, Desktop: config.Config{Addr: "127.0.0.1:0", DataDir: filepath.Join(home, "desktop"), Home: home, ConfigPath: filepath.Join(home, "config.json"), WorkspaceDir: filepath.Join(home, "workspace")}}, ComputeSettings: ComputeSettings{Compute: profile.Compute{Mode: "disabled"}}}
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	c := compute.Config{DataDir: filepath.Join(home, "compute"), Socket: filepath.Join(home, "host.sock"), Capacity: compute.Capacity{Resources: compute.Resources{CPUs: 4, MemoryMiB: 4096, DiskGiB: 40}, Machines: 4, Workers: 1}, Lima: &compute.LimaConfig{Binary: "/test/limactl", Version: compute.SupportedLimaVersion}}
	return s, c
}

type fakeComputeHost struct {
	server        *http.Server
	identity      atomic.Pointer[compute.Runtime]
	available     atomic.Bool
	images        atomic.Bool
	infoCalls     atomic.Int32
	machinesCalls atomic.Int32
}

func fakeHost(t *testing.T, socket string, identity compute.Runtime) *fakeComputeHost {
	t.Helper()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	h := &fakeComputeHost{}
	h.identity.Store(&identity)
	h.available.Store(true)
	h.images.Store(true)
	h.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := *h.identity.Load()
		if r.URL.Path == "/v1/runtime" {
			json.NewEncoder(w).Encode(info)
			return
		}
		if r.URL.Path == "/v1/info" {
			h.infoCalls.Add(1)
			images := []compute.ImageInfo{}
			if h.images.Load() {
				images = append(images, compute.ImageInfo{ID: "native", Provider: "lima", Arch: runtime.GOARCH, Status: "ready"})
			}
			json.NewEncoder(w).Encode(map[string]any{"hostId": info.HostID, "protocolVersion": info.ProtocolVersion, "configFingerprint": info.ConfigFingerprint, "hostArch": runtime.GOARCH, "providers": []compute.Capability{{Provider: "lima", Available: h.available.Load(), Reason: "missing runtime"}}, "images": images})
			return
		}
		h.machinesCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"machines": []any{}})
	})}
	go h.server.Serve(listener)
	t.Cleanup(func() { h.server.Close() })
	return h
}

func TestHostQueriesUseConfiguredIdentityWithoutProviderProbes(t *testing.T) {
	s, cfg := runtimeFixture(t)
	s.Compute = profile.Compute{Mode: "managed", Socket: cfg.Socket}
	s.Host = &cfg
	identity := compute.Runtime{Ready: true, HostID: "host", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: cfg.Fingerprint()}
	host := fakeHost(t, cfg.Socket, identity)
	if actual, err := HostStatus(t.Context(), s); err != nil || actual != identity {
		t.Fatalf("runtime identity: %+v %v", actual, err)
	}
	if _, err := Machines(t.Context(), s); err != nil || host.machinesCalls.Load() != 1 {
		t.Fatalf("verified machine query failed: %v", err)
	}
	identity.ConfigFingerprint = "unrelated-config"
	host.identity.Store(&identity)
	if _, err := HostStatus(t.Context(), s); err == nil {
		t.Fatal("runtime accepted unrelated configuration")
	}
	if _, err := Machines(t.Context(), s); err == nil || host.machinesCalls.Load() != 1 {
		t.Fatalf("unrelated host received machine query: %v", err)
	}
	if host.infoCalls.Load() != 0 {
		t.Fatal("runtime or machine query probed providers")
	}
}

func TestLiveComputeActivationDoesNotChangeDesktopIdentity(t *testing.T) {
	s, cfg := runtimeFixture(t)
	gateway := computegateway.NewGateway("", computegateway.GatewayOptions{})
	defer gateway.Close()
	coordinator := newComputeCoordinator(context.Background(), s, io.Discard, gateway, nil)
	defer coordinator.close()
	coordinator.bootstrap()
	if coordinator.runtimeStatus().State != "not_configured" {
		t.Fatal(coordinator.runtimeStatus())
	}
	identity := compute.Runtime{Ready: true, HostID: "same-host", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: cfg.Fingerprint()}
	host := fakeHost(t, cfg.Socket, identity)
	connection := profile.Compute{Mode: "external", Socket: cfg.Socket}
	if err := profile.SaveCompute(s.Home, connection); err != nil {
		t.Fatal(err)
	}
	binding, err := coordinator.activatePrepared(context.Background(), prepare.Activation{Compute: connection, ExpectedHostID: identity.HostID})
	if err != nil || binding.HostID != identity.HostID || coordinator.runtimeStatus().State != "ready" {
		t.Fatalf("activation: %#v %#v %v", binding, coordinator.runtimeStatus(), err)
	}
	live, err := s.LiveCompute()
	if err != nil || live.Fingerprint() != s.Fingerprint() {
		t.Fatalf("live compute invalidated desktop: %v", err)
	}
	coordinator.checkConnection()
	if host.infoCalls.Load() != 1 {
		t.Fatal("heartbeat repeated provider probe")
	}
	host.identity.Store(&compute.Runtime{Ready: true, HostID: "replacement", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: cfg.Fingerprint()})
	coordinator.checkConnection()
	if coordinator.runtimeStatus().State != "failed" {
		t.Fatal("replacement host reported ready")
	}
	host.identity.Store(&identity)
	coordinator.checkConnection()
	if coordinator.runtimeStatus().State != "ready" || host.infoCalls.Load() != 2 {
		t.Fatalf("same-host reconnect: %#v probes %d", coordinator.runtimeStatus(), host.infoCalls.Load())
	}
	coordinator.close()
	if _, err := HostStatus(context.Background(), live); err != nil {
		t.Fatalf("coordinator stopped independently owned host: %v", err)
	}
}

func TestActivationRequiresUsableProviderAndNativeImage(t *testing.T) {
	for _, kind := range []string{"provider", "image", "expected identity", "configuration changed"} {
		t.Run(kind, func(t *testing.T) {
			s, cfg := runtimeFixture(t)
			host := fakeHost(t, cfg.Socket, compute.Runtime{Ready: true, HostID: "host", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: cfg.Fingerprint()})
			connection := profile.Compute{Mode: "external", Socket: cfg.Socket}
			if err := profile.SaveCompute(s.Home, connection); err != nil {
				t.Fatal(err)
			}
			gateway := computegateway.NewGateway("", computegateway.GatewayOptions{})
			defer gateway.Close()
			coordinator := newComputeCoordinator(context.Background(), s, io.Discard, gateway, nil)
			defer coordinator.close()
			request := prepare.Activation{Compute: connection}
			switch kind {
			case "provider":
				host.available.Store(false)
			case "image":
				host.images.Store(false)
			case "expected identity":
				request.ExpectedHostID = "other"
			case "configuration changed":
				request.Compute.Socket = "/different.sock"
			}
			if _, err := coordinator.activatePrepared(context.Background(), request); err == nil {
				t.Fatal("invalid activation accepted")
			}
			if coordinator.runtimeStatus().State != "failed" {
				t.Fatal(coordinator.runtimeStatus())
			}
			if kind == "provider" || kind == "image" {
				if coordinator.runtimeStatus().HostID != "host" {
					t.Fatal("verified host identity was lost on readiness failure")
				}
			}
		})
	}
}

func TestDesktopReadyPrecedesSlowHostAndShutdownCancelsIt(t *testing.T) {
	s, cfg := runtimeFixture(t)
	path := filepath.Join(s.Home, "host.json")
	if err := jsonfile.Write(path, cfg, 0600); err != nil {
		t.Fatal(err)
	}
	if err := profile.SaveCompute(s.Home, profile.Compute{Mode: "managed", HostConfig: path}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	runner := func(ctx context.Context, _ Settings, _ io.Writer) (func(), error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveContext(ctx, s, io.Discard, runner) }()
	state := waitRuntime(t, s.Home)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("host bootstrap did not start")
	}
	response, err := http.Get(state.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("desktop waited for host readiness")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown waited for a host activation lock")
	}
}

func TestComputeAndPreparationFailuresDoNotBlockDesktop(t *testing.T) {
	for _, kind := range []string{"compute", "preparation"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := runtimeFixture(t)
			if kind == "compute" {
				os.WriteFile(filepath.Join(s.Home, "config.json"), []byte(`{"compute":{"mode":"managed","host_config":"relative"}}`), 0600)
			} else {
				os.WriteFile(filepath.Join(s.Home, "preparation"), []byte("preserve"), 0600)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- serveContext(ctx, s, io.Discard, nil) }()
			state := waitRuntime(t, s.Home)
			response, err := http.Get(state.URL + "/api/compute/preparation/check")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if kind == "preparation" && (response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "preparation_unavailable")) {
				t.Fatalf("unexpected unavailable response: %d %s", response.StatusCode, body)
			}
			response, err = http.Get(state.URL + "/api/compute/environment")
			if err != nil {
				t.Fatal(err)
			}
			var environment computegateway.Environment
			err = json.NewDecoder(response.Body).Decode(&environment)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || environment.Preparation == nil || environment.ConfigurationSource != "profile" || len(environment.Preparation.Checks) == 0 {
				t.Fatalf("environment missing degraded preparation snapshot: %#v, %v, HTTP %d", environment, err, response.StatusCode)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func waitRuntime(t *testing.T, home string) launch.State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := launch.ReadState(home)
		if err == nil && launch.Verify(context.Background(), state) == nil {
			return state
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("desktop did not become ready")
	return launch.State{}
}

func TestOverrideIgnoresBrokenComputeAndRemainsReadOnly(t *testing.T) {
	s, cfg := runtimeFixture(t)
	os.WriteFile(filepath.Join(s.Home, "config.json"), []byte(`{"compute":{"mode":"managed","host_config":"relative"}}`), 0600)
	s.ComputeOverride = cfg.Socket
	fakeHost(t, cfg.Socket, compute.Runtime{Ready: true, HostID: "override", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: cfg.Fingerprint()})
	gateway := computegateway.NewGateway("", computegateway.GatewayOptions{})
	defer gateway.Close()
	coordinator := newComputeCoordinator(context.Background(), s, io.Discard, gateway, nil)
	defer coordinator.close()
	coordinator.bootstrap()
	if status := coordinator.runtimeStatus(); status.State != "ready" || !status.ReadOnly || status.Mode != "external" {
		t.Fatal(status)
	}
	request := httptest.NewRequest("GET", "/api/compute/environment", nil)
	request.Host, request.RemoteAddr = "localhost:3001", "127.0.0.1:1234"
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), `"readOnly":true`) {
		t.Fatal(response.Body.String())
	}
}
