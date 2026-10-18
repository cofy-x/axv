package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"golang.org/x/crypto/ssh"
)

type accessDriver struct {
	compute.Driver
	address string
}

func (d accessDriver) GuestToken(compute.Machine) (string, error) { return "test-guest-token", nil }
func (d accessDriver) DialGuest(ctx context.Context, _ compute.Machine) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "tcp", d.address)
}
func (accessDriver) GuestReachability(compute.Machine, netip.Addr) guestproto.Reachability {
	return guestproto.Reachability{From: "deployment-host", State: "unreachable", Reason: "provider_test"}
}

func runningAccessService(t *testing.T, handler http.Handler) (*serviceFixture, compute.Machine) {
	t.Helper()
	service, spec := operationService(t)
	created, err := service.Create(context.Background(), compute.CreateRequest{RequestID: "test-ssh-create", Spec: spec, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	op, found, err := service.Store.Claim(context.Background())
	if err != nil || !found {
		t.Fatalf("claim: %v, %v", found, err)
	}
	if err = service.Store.Finish(context.Background(), op, compute.Observation{Status: "running", Ready: true}, nil); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-guest-token" {
			http.Error(w, "missing guest authentication", 401)
			return
		}
		if r.URL.Path == "/v1/health" {
			_ = json.NewEncoder(w).Encode(guestproto.Health{Version: guestproto.Version, MachineID: created.Machine.ID, BootID: "boot-current", Ready: true, SSHAvailable: true, Capabilities: []string{"exec", "terminals", "ssh", "network"}})
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	service.Drivers = map[string]compute.Driver{"lima": accessDriver{address: strings.TrimPrefix(server.URL, "http://")}}
	service.rebuild()
	machine, err := service.Store.Machine(context.Background(), created.Machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	return service, machine
}

func TestSSHMetadataRejectsWrongGuestIdentityAndInvalidHostKeys(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ssh.NewPublicKey(public)
	info := guestproto.SSHInfo{Version: guestproto.Version, BootID: "boot-current", User: "axv", Available: true, HostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))}
	service, machine := runningAccessService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(info) }))
	info.MachineID = machine.ID
	for _, field := range []string{"valid", "version", "machine", "key", "missing-key"} {
		t.Run(field, func(t *testing.T) {
			saved := info
			defer func() { info = saved }()
			want := http.StatusServiceUnavailable
			switch field {
			case "version":
				info.Version--
			case "machine":
				info.MachineID = "replaced-machine"
			case "key":
				info.HostKey = "ssh-ed25519 invalid"
			case "missing-key":
				info.HostKey = ""
			default:
				want = http.StatusOK
			}
			request := httptest.NewRequest("GET", "/v1/machines/"+machine.ID+"/ssh", nil)
			request.Header.Set("X-AXV-Host-ID", service.Store.HostID)
			response := httptest.NewRecorder()
			Handler(service.Service).ServeHTTP(response, request)
			if response.Code != want {
				t.Fatalf("HTTP %d: %s", response.Code, response.Body)
			}
			if field == "valid" && !strings.Contains(response.Body.String(), `"hostId":"`+service.Store.HostID+`"`) {
				t.Fatal("metadata omitted the verified host identity")
			}
		})
	}
}

func TestStaleSSHBootNeverReachesRegistrationOrAttachment(t *testing.T) {
	var machine compute.Machine
	var forwarded atomic.Int32
	service, created := runningAccessService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			forwarded.Add(1)
			return
		}
		_ = json.NewEncoder(w).Encode(guestproto.Health{Version: guestproto.Version, MachineID: machine.ID, BootID: "boot-current", Ready: true})
	}))
	machine = created
	for _, path := range []string{"/ssh/clients", "/ssh/attach?bootId=boot-old"} {
		method := "GET"
		if path == "/ssh/clients" {
			method = "POST"
		}
		request := httptest.NewRequest(method, "/v1/machines/"+machine.ID+path, strings.NewReader(`{"bootId":"boot-old","clientId":"profile-test","publicKey":"unused"}`))
		request.Header.Set("X-AXV-Host-ID", service.Store.HostID)
		request.Header.Set("Upgrade", "axv-ssh-v1")
		request.Header.Set("Connection", "Upgrade")
		response := httptest.NewRecorder()
		Handler(service.Service).ServeHTTP(response, request)
		if response.Code != 409 || !strings.Contains(response.Body.String(), `"code":"boot_changed"`) {
			t.Fatalf("HTTP %d: %s", response.Code, response.Body)
		}
	}
	if forwarded.Load() != 0 {
		t.Fatal("stale SSH request reached the guest access route")
	}
}

func TestNetworkReachabilityComesFromProviderAndStoppedVMIsNotProbed(t *testing.T) {
	var calls atomic.Int32
	info := guestproto.NetworkInfo{Version: guestproto.Version, BootID: "boot-current", ObservedAt: time.Now(), Addresses: []guestproto.NetworkAddress{{Interface: "eth0", Address: "10.0.0.2", PrefixLength: 30, Family: "ipv4", Reachability: guestproto.Reachability{State: "reachable"}}}}
	service, machine := runningAccessService(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _ = json.NewEncoder(w).Encode(info) }))
	info.MachineID = machine.ID
	handler := Handler(service.Service)
	request := httptest.NewRequest("GET", "/v1/machines/"+machine.ID+"/network", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var result guestproto.NetworkInfo
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || len(result.Addresses) != 1 || result.Addresses[0].Reachability.State != "unreachable" || result.Provider != "lima" || result.HostID != service.Store.HostID {
		t.Fatalf("network result: HTTP %d, %s, %v", response.Code, response.Body, err)
	}
	if err := service.Store.Observe(context.Background(), machine, compute.Observation{Status: "stopped"}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 409 || calls.Load() != 1 {
		t.Fatalf("stopped machine probed: HTTP %d, calls %d", response.Code, calls.Load())
	}
}

func TestSSHHostIdentityIsRequiredBeforeResourceLookup(t *testing.T) {
	store, err := control.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := Handler(control.NewService(control.Options{Store: store}))
	for _, host := range []string{"", "other-host"} {
		for _, path := range []string{"/v1/machines/missing/ssh", "/v1/machines/missing/ssh/clients", "/v1/machines/missing/ssh/attach"} {
			method := "GET"
			if path == "/v1/machines/missing/ssh/clients" {
				method = "POST"
			}
			request := httptest.NewRequest(method, path, nil)
			request.Header.Set("X-AXV-Host-ID", host)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 409 {
				t.Fatalf("%s host %q: %d %s", path, host, response.Code, response.Body)
			}
		}
	}
}
