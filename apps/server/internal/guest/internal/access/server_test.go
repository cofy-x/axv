package access

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"golang.org/x/crypto/ssh"
)

func testPublicKey(t *testing.T) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func testServer(t *testing.T) *Server {
	t.Helper()
	s := New(context.Background(), "vm-test", "boot-test")
	s.authorizedKeys = filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.Chmod(filepath.Dir(s.authorizedKeys), 0700); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRegistrationPersistsRotationWithoutDiscardingOtherProfiles(t *testing.T) {
	s := testServer(t)
	one, two, rotated := testPublicKey(t), testPublicKey(t), testPublicKey(t)
	for _, pair := range [][2]string{{"profile-one", one}, {"profile-two", two}, {"profile-one", rotated}} {
		if err := s.putClient(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	reopened := New(context.Background(), s.machineID, s.bootID)
	reopened.authorizedKeys = s.authorizedKeys
	if err := reopened.putClient("profile-two", two); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.authorizedKeys)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), one) || !strings.Contains(string(data), two) || !strings.Contains(string(data), rotated) || strings.Count(string(data), "\n") != 2 {
		t.Fatalf("key rotation lost ownership: %s", data)
	}
	info, err := os.Stat(s.authorizedKeys)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key permissions: %v %v", info, err)
	}
}

func TestConcurrentRegistrationsRetainEveryProfile(t *testing.T) {
	s := testServer(t)
	public := testPublicKey(t)
	var group sync.WaitGroup
	errors := make(chan error, 32)
	for i := range 32 {
		group.Add(1)
		go func() { defer group.Done(); errors <- s.putClient(fmt.Sprintf("profile-%03d", i), public) }()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(s.authorizedKeys)
	if err != nil || strings.Count(string(data), "\n") != 32 {
		t.Fatalf("concurrent writes lost clients: %s, %v", data, err)
	}
}

func TestRegistrationRejectsStaleBootAndUnsafeKeys(t *testing.T) {
	s := testServer(t)
	key := testPublicKey(t)
	for _, tc := range []struct {
		name, boot, public string
		status             int
	}{
		{"old boot", "boot-old", key, 409},
		{"key options", s.bootID, "command=\"sh\" " + key, 400},
		{"multiple keys", s.bootID, key + "\n" + key, 400},
		{"bad key", s.bootID, "ssh-ed25519 broken", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, _ := json.Marshal(protocol.SSHClientRequest{BootID: tc.boot, ClientID: "profile-test", PublicKey: tc.public})
			response := httptest.NewRecorder()
			s.registerClient(response, httptest.NewRequest(http.MethodPost, "/v1/ssh/clients", strings.NewReader(string(data))))
			if response.Code != tc.status {
				t.Fatalf("status %d: %s", response.Code, response.Body)
			}
			if _, err := os.Stat(s.authorizedKeys); !os.IsNotExist(err) {
				t.Fatal("rejected request changed key storage")
			}
		})
	}
}

func TestRegistrationRejectsMalformedBodiesWithoutChangingKeys(t *testing.T) {
	key := testPublicKey(t)
	data, err := json.Marshal(protocol.SSHClientRequest{BootID: "boot-test", ClientID: "profile-test", PublicKey: key})
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	for _, tc := range []struct {
		name, body string
	}{
		{"empty", ""},
		{"incomplete", "{"},
		{"multiple objects", valid + valid},
		{"unknown field", strings.TrimSuffix(valid, "}") + `,"extra":true}`},
		{"oversized", strings.Replace(valid, key, strings.Repeat("x", 4096), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer(t)
			if err := s.putClient("profile-existing", key); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(s.authorizedKeys)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			s.registerClient(response, httptest.NewRequest(http.MethodPost, "/v1/ssh/clients", strings.NewReader(tc.body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", response.Code, response.Body)
			}
			after, err := os.ReadFile(s.authorizedKeys)
			if err != nil || string(after) != string(before) {
				t.Fatalf("rejected body changed the registry: %q, %v", after, err)
			}
		})
	}
}

func TestRegistrationAtCapacityAllowsReplayAndRotation(t *testing.T) {
	s := testServer(t)
	key, rotated := testPublicKey(t), testPublicKey(t)
	var registry strings.Builder
	for i := range maxClients {
		fmt.Fprintf(&registry, "%s axv-client:profile-%03d\n", key, i)
	}
	if err := os.WriteFile(s.authorizedKeys, []byte(registry.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.putClient("profile-000", key); err != nil {
		t.Fatalf("replay at capacity: %v", err)
	}
	if err := s.putClient("profile-000", rotated); err != nil {
		t.Fatalf("rotation at capacity: %v", err)
	}
	before, err := os.ReadFile(s.authorizedKeys)
	if err != nil || strings.Count(string(before), "\n") != maxClients || !strings.Contains(string(before), rotated+" axv-client:profile-000\n") {
		t.Fatalf("rotation changed the registry's ownership: %q, %v", before, err)
	}
	if err := s.putClient("profile-overflow", key); err == nil {
		t.Fatal("accepted a new client beyond capacity")
	}
	after, err := os.ReadFile(s.authorizedKeys)
	if err != nil || string(after) != string(before) {
		t.Fatalf("rejected registration changed the registry: %q, %v", after, err)
	}
}

func TestRegistrationPreservesInvalidOwnedRegistries(t *testing.T) {
	key := testPublicKey(t)
	owned := key + " axv-client:profile-existing\n"
	for _, tc := range []struct {
		name, registry string
		fileMode       os.FileMode
		directoryMode  os.FileMode
	}{
		{"duplicate client", owned + owned, 0600, 0700},
		{"oversized file", strings.Repeat(" ", maxKeysFile+1), 0600, 0700},
		{"writable file", owned, 0666, 0700},
		{"nonprivate directory", owned, 0600, 0755},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer(t)
			if err := os.WriteFile(s.authorizedKeys, []byte(tc.registry), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(s.authorizedKeys, tc.fileMode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Dir(s.authorizedKeys), tc.directoryMode); err != nil {
				t.Fatal(err)
			}
			if err := s.putClient("profile-new", key); err == nil {
				t.Fatal("replaced an invalid or unsafe registry")
			}
			data, err := os.ReadFile(s.authorizedKeys)
			if err != nil || string(data) != tc.registry {
				t.Fatalf("rejected registration changed the registry: %q, %v", data, err)
			}
		})
	}
}

func TestRegistrationPreservesUnownedAndSymlinkFiles(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			s := testServer(t)
			original := testPublicKey(t) + " personal-key\n"
			path := s.authorizedKeys
			if symlink {
				path = filepath.Join(t.TempDir(), "personal")
			}
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink(path, s.authorizedKeys); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.putClient("profile-test", testPublicKey(t)); err == nil {
				t.Fatal("adopted an unowned key file")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != original {
				t.Fatalf("existing file changed: %s %v", data, err)
			}
		})
	}
}

func TestAttachRequiresExactBootAndUpgrade(t *testing.T) {
	s := testServer(t)
	for _, tc := range []struct {
		query, upgrade string
		status         int
	}{{"bootId=old", "axv-ssh-v1", 409}, {"bootId=boot-test", "", 400}} {
		request := httptest.NewRequest("GET", "/v1/ssh/attach?"+tc.query, nil)
		request.Header.Set("Upgrade", tc.upgrade)
		response := httptest.NewRecorder()
		s.attach(response, request)
		if response.Code != tc.status {
			t.Fatalf("status %d: %s", response.Code, response.Body)
		}
	}
}

func TestNetworkAddressFiltersOnlyNonusableInterfaceAddresses(t *testing.T) {
	for _, tc := range []struct {
		value, family string
		ok            bool
	}{
		{"127.0.0.1/8", "", false}, {"::1/128", "", false}, {"169.254.1.2/16", "", false}, {"fe80::1/64", "", false},
		{"0.0.0.0/0", "", false}, {"224.0.0.1/24", "", false}, {"10.0.0.2/30", "ipv4", true}, {"fd00::2/64", "ipv6", true},
	} {
		value, ok := networkAddress("eth0", tc.value)
		if ok != tc.ok || (ok && (value.Family != tc.family || value.Reachability.State != "unknown")) {
			t.Fatalf("%s = %+v, %v", tc.value, value, ok)
		}
	}
}
