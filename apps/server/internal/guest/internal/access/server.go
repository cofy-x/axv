// Package access owns private SSH access and guest network observations.
// Its HTTP routes must be wrapped in the guest's per-machine authentication.
package access

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/guest/sshkey"
	"github.com/cofy-x/axv/apps/server/internal/sshstream"
	"golang.org/x/crypto/ssh"
)

const Address = "127.0.0.1:22222"
const HostPublicKey = "/etc/ssh/axv/ssh_host_ed25519_key.pub"
const AuthorizedKeys = "/var/lib/axv-ssh/clients/authorized_keys"
const maxClients = 128
const maxKeysFile = 64 << 10

var clientID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

type Server struct {
	ctx                                    context.Context
	machineID, bootID                      string
	hostPublicKey, authorizedKeys, address string
	mu                                     sync.Mutex
	connections                            chan struct{}
}

func New(ctx context.Context, machineID, bootID string) *Server {
	return &Server{ctx: ctx, machineID: machineID, bootID: bootID,
		hostPublicKey: HostPublicKey, authorizedKeys: AuthorizedKeys, address: Address,
		connections: make(chan struct{}, 32)}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/ssh", func(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, s.Info(r.Context())) })
	mux.HandleFunc("POST /v1/ssh/clients", s.registerClient)
	mux.HandleFunc("GET /v1/ssh/attach", s.attach)
	mux.HandleFunc("GET /v1/network", s.network)
}

func readRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("SSH file has unsafe type or permissions")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("SSH file exceeds its size limit")
	}
	return data, err
}

func (s *Server) Info(ctx context.Context) protocol.SSHInfo {
	info := protocol.SSHInfo{Version: protocol.Version, MachineID: s.machineID, BootID: s.bootID, User: "axv"}
	data, err := readRegular(s.hostPublicKey, 2048)
	if err == nil {
		info.HostKey, err = sshkey.Normalize(string(data))
	}
	if err != nil {
		info.Reason = "The guest SSH host key is unavailable. Use an SSH-capable AXV guest image."
		return info
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.address)
	if err != nil {
		info.Reason = "The dedicated guest SSH service is not listening."
		return info
	}
	conn.Close()
	info.Available = true
	return info
}

func (s *Server) registerClient(w http.ResponseWriter, r *http.Request) {
	var request protocol.SSHClientRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF {
		problem(w, 400, "invalid_request", "Expected one SSH client registration.")
		return
	}
	if request.BootID != s.bootID {
		problem(w, 409, "boot_changed", "The VM boot changed; SSH registration was not applied.")
		return
	}
	if !clientID.MatchString(request.ClientID) {
		problem(w, 400, "invalid_request", "clientId must contain 8–128 letters, digits, underscores or hyphens.")
		return
	}
	publicKey, err := sshkey.Normalize(request.PublicKey)
	if err != nil {
		problem(w, 400, "invalid_request", err.Error())
		return
	}
	if err = s.putClient(request.ClientID, publicKey); err != nil {
		problem(w, 503, "ssh_registration_failed", err.Error())
		return
	}
	reply(w, http.StatusOK, protocol.SSHClient{BootID: s.bootID, ClientID: request.ClientID, PublicKey: publicKey})
}

func (s *Server) putClient(id, publicKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := os.Lstat(filepath.Dir(s.authorizedKeys))
	if err != nil {
		return fmt.Errorf("SSH client directory is not provisioned: %w", err)
	}
	if !dir.IsDir() || dir.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("SSH client directory must be private")
	}
	clients := map[string]string{}
	data, err := readRegular(s.authorizedKeys, maxKeysFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		key, comment, options, rest, parseErr := ssh.ParseAuthorizedKey([]byte(line))
		id, owned := strings.CutPrefix(comment, "axv-client:")
		if parseErr != nil || !owned || !clientID.MatchString(id) || len(options) != 0 || len(rest) != 0 || key.Type() != ssh.KeyAlgoED25519 {
			return fmt.Errorf("The dedicated SSH key file contains an invalid or unowned entry; it was not replaced")
		}
		if _, exists := clients[id]; exists {
			return fmt.Errorf("The SSH key file contains duplicate client identities")
		}
		clients[id] = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	}
	if clients[id] == publicKey {
		return nil
	}
	clients[id] = publicKey
	if len(clients) > maxClients {
		return fmt.Errorf("The guest SSH client limit has been reached")
	}
	ids := make([]string, 0, len(clients))
	for id := range clients {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var result strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&result, "%s axv-client:%s\n", clients[id], id)
	}
	if result.Len() > maxKeysFile {
		return fmt.Errorf("The guest SSH key file is full")
	}
	return atomicfile.Write(s.authorizedKeys, []byte(result.String()), 0600)
}

func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("bootId") != s.bootID {
		problem(w, 409, "boot_changed", "The VM boot changed; this SSH connection was not redirected.")
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), sshstream.Protocol) {
		problem(w, 400, "invalid_request", "An AXV SSH stream upgrade is required.")
		return
	}
	select {
	case s.connections <- struct{}{}:
		defer func() { <-s.connections }()
	default:
		problem(w, 503, "ssh_connection_limit", "The guest SSH connection limit has been reached.")
		return
	}
	setup, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	target, err := (&net.Dialer{}).DialContext(setup, "tcp", s.address)
	cancel()
	if err != nil {
		problem(w, 503, "ssh_unavailable", "The dedicated guest SSH service is unavailable.")
		return
	}
	defer target.Close()
	conn, err := sshstream.Accept(w, r)
	if err != nil {
		// Accept may already have hijacked and closed the response connection.
		return
	}
	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	shutdown := context.AfterFunc(s.ctx, stop)
	defer shutdown()
	_ = sshstream.Bridge(ctx, conn, target)
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func problem(w http.ResponseWriter, status int, code, message string) {
	reply(w, status, map[string]string{"code": code, "error": message})
}
