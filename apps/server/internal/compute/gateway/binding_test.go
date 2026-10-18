package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func bindingHost(t *testing.T, socket, id string, handle http.HandlerFunc) *http.Server {
	t.Helper()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ready": true, "hostId": id, "protocolVersion": compute.ProtocolVersion, "configFingerprint": id + "-config"})
			return
		}
		handle(w, r)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return server
}

func gatewayRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Host, r.RemoteAddr = "localhost:3001", "127.0.0.1:12345"
	r.Header.Set("Origin", "http://localhost:3001")
	return r
}

func shortSocketDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "axv-gw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestBindingKeepsInflightRequestOnItsOriginalHost(t *testing.T) {
	dir := shortSocketDirectory(t)
	firstSocket, nextSocket := filepath.Join(dir, "a.sock"), filepath.Join(dir, "b.sock")
	entered, release := make(chan struct{}), make(chan struct{})
	bindingHost(t, firstSocket, "first", func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; fmt.Fprint(w, "first") })
	bindingHost(t, nextSocket, "next", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "next") })
	gateway := NewGateway("", GatewayOptions{})
	defer gateway.Close()
	generation := gateway.Bind(firstSocket, "first", "first-config")
	firstResponse, done := httptest.NewRecorder(), make(chan struct{})
	go func() { gateway.ServeHTTP(firstResponse, gatewayRequest("POST", "/api/compute/machines")); close(done) }()
	<-entered
	nextGeneration := gateway.Bind(nextSocket, "next", "next-config")
	if nextGeneration == generation {
		t.Fatal("binding did not advance")
	}
	close(release)
	<-done
	if firstResponse.Body.String() != "first" {
		t.Fatalf("inflight request moved hosts: %s", firstResponse.Body)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, gatewayRequest("GET", "/api/compute/machines"))
	if response.Body.String() != "next" {
		t.Fatalf("new request did not use published host: %s", response.Body)
	}
	stale := gatewayRequest("POST", "/api/compute/machines")
	stale.Header.Set("X-AXV-Compute-Generation", strconv.FormatUint(generation, 10))
	response = httptest.NewRecorder()
	gateway.ServeHTTP(response, stale)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale mutation accepted: %d", response.Code)
	}
	stale = gatewayRequest("POST", "/api/compute/machines")
	stale.Header.Set("X-AXV-Host-ID", "first")
	response = httptest.NewRecorder()
	gateway.ServeHTTP(response, stale)
	if response.Code != http.StatusConflict {
		t.Fatalf("old host mutation accepted: %d", response.Code)
	}
}

func TestReplacingSocketCannotRetargetAnExistingBinding(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(strconv.FormatBool(upgrade), func(t *testing.T) {
			socket := filepath.Join(shortSocketDirectory(t), "control.sock")
			first := bindingHost(t, socket, "first", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "first") })
			gateway := NewGateway("", GatewayOptions{})
			defer gateway.Close()
			gateway.Bind(socket, "first", "first-config")
			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, gatewayRequest("GET", "/api/compute/machines"))
			if response.Code != http.StatusOK {
				t.Fatal(response.Body.String())
			}
			first.Close()
			var mutations atomic.Int32
			bindingHost(t, socket, "replacement", func(w http.ResponseWriter, r *http.Request) { mutations.Add(1); io.WriteString(w, "wrong host") })
			request := gatewayRequest("POST", "/api/compute/machines")
			if upgrade {
				request = gatewayRequest("GET", "/api/compute/machines/test/guest/terminals/test/attach")
				request.Header.Set("Connection", "Upgrade")
				request.Header.Set("Upgrade", "websocket")
			}
			response = httptest.NewRecorder()
			gateway.ServeHTTP(response, request)
			if response.Code != http.StatusServiceUnavailable || mutations.Load() != 0 || strings.Contains(response.Body.String(), "wrong host") {
				t.Fatalf("request reached replacement: %d %d %s", response.Code, mutations.Load(), response.Body)
			}
		})
	}
}

func TestBindingWithoutHostIdentityRetainsDirectProxyBehavior(t *testing.T) {
	socket := filepath.Join(shortSocketDirectory(t), "direct.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/machines" {
			t.Errorf("unverified binding unexpectedly requested %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		io.WriteString(w, "direct")
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	gateway := NewGateway(socket, GatewayOptions{})
	defer gateway.Close()
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, gatewayRequest(http.MethodGet, "/api/compute/machines"))
	if response.Code != http.StatusOK || response.Body.String() != "direct" {
		t.Fatalf("direct binding changed: %d %s", response.Code, response.Body)
	}
}

func TestBrowserGatewayNeverForwardsPrivateSSHPaths(t *testing.T) {
	socket := filepath.Join(shortSocketDirectory(t), "private.sock")
	var reached atomic.Int32
	bindingHost(t, socket, "test-host", func(w http.ResponseWriter, r *http.Request) { reached.Add(1); w.WriteHeader(200) })
	gateway := NewGateway("", GatewayOptions{})
	defer gateway.Close()
	gateway.Bind(socket, "test-host", "test-host-config")
	for _, path := range []string{
		"/api/compute/machines/vm-test/ssh",
		"/api/compute/machines/vm-test/ssh/clients",
		"/api/compute/machines/vm-test/ssh/attach?bootId=boot",
		"/api/compute//machines//vm-test//ssh//attach",
		"/api/compute/machines/vm-test/guest/../ssh",
		"/api/compute/machines/vm-test/guest/%2e%2e/ssh/clients",
		"/api/compute/machines%2fvm-test%2fssh%2fattach",
		"/api/compute/machines/vm-test/%73sh",
	} {
		for _, method := range []string{"GET", "POST", "HEAD"} {
			r := gatewayRequest(method, path)
			r.Header.Set("X-AXV-Host-ID", "test-host")
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "axv-ssh-v1")
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, r)
			if w.Code != 404 {
				t.Fatalf("%s %s: status %d", method, path, w.Code)
			}
		}
	}
	if reached.Load() != 0 {
		t.Fatal("browser reached a private SSH route")
	}
}
