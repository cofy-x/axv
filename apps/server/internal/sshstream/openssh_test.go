package sshstream

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// The helper is a real OpenSSH ProxyCommand process. Test output must never
// share stdout with the SSH protocol bytes.
func TestNativeProxyProcess(t *testing.T) {
	url := os.Getenv("AXV_TEST_SSH_PROXY_URL")
	if url == "" {
		return
	}
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err == nil {
		var conn net.Conn
		conn, err = net.DialTimeout("tcp", request.URL.Host, 5*time.Second)
		if err == nil {
			conn, err = Upgrade(context.Background(), conn, request)
		}
		if err == nil {
			err = Pump(context.Background(), conn, os.Stdin, os.Stdout)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func nativeKey(t *testing.T, path string) string {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "AXV isolated transport test")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func availableTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func nativeProcess(t *testing.T, ctx context.Context, command *exec.Cmd) {
	t.Helper()
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("native SSH test process did not stop")
		}
	})
	// A failed startup must be reported with the daemon's diagnostic, while the
	// owning test has its own readiness probe for successfully started daemons.
	select {
	case err := <-done:
		done <- err
		t.Fatalf("native SSH process %s exited: %v (context %v): %s", command.String(), err, ctx.Err(), stderr.String())
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(100 * time.Millisecond):
	}
}

func waitTCP(t *testing.T, ctx context.Context, address string) net.Conn {
	t.Helper()
	for {
		conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "tcp", address)
		if err == nil {
			return conn
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v", address, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestNativeOpenSSHCommandsTransfersAndForwarding(t *testing.T) {
	if os.Getenv("AXV_TEST_OPENSSH") != "1" {
		t.Skip("set AXV_TEST_OPENSSH=1 to run an isolated native OpenSSH server")
	}
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		if _, err = os.Stat("/usr/sbin/sshd"); err == nil {
			sshd = "/usr/sbin/sshd"
		} else {
			t.Skip("OpenSSH server is not installed")
		}
	}
	for _, name := range []string{"ssh", "scp", "sftp"} {
		if _, err = exec.LookPath(name); err != nil {
			t.Skipf("OpenSSH %s is not installed", name)
		}
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	serverKey, clientKey := filepath.Join(dir, "host"), filepath.Join(dir, "client")
	hostPublic := nativeKey(t, serverKey)
	clientPublic := nativeKey(t, clientKey)
	authorized := filepath.Join(dir, "authorized_keys")
	if err = os.WriteFile(authorized, []byte(clientPublic+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	address := availableTCPAddress(t)
	_, port, _ := net.SplitHostPort(address)
	serverConfig := filepath.Join(dir, "sshd_config")
	config := fmt.Sprintf("ListenAddress 127.0.0.1\nPort %s\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nAllowUsers %s\nPermitRootLogin prohibit-password\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nStrictModes no\nAllowTcpForwarding yes\nGatewayPorts no\nUseDNS no\nSubsystem sftp internal-sftp\n", port, serverKey, filepath.Join(dir, "pid"), authorized, current.Username)
	if err = os.WriteFile(serverConfig, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	nativeProcess(t, ctx, exec.CommandContext(ctx, sshd, "-D", "-e", "-f", serverConfig))
	waitTCP(t, ctx, address).Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", address)
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		defer target.Close()
		conn, err := Accept(w, r)
		if err == nil {
			_ = Bridge(ctx, conn, target)
		}
	}))
	defer server.Close()
	t.Setenv("AXV_TEST_SSH_PROXY_URL", server.URL)
	knownHosts := filepath.Join(dir, "known_hosts")
	if err = os.WriteFile(knownHosts, []byte("axv-stream-test "+hostPublic+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	clientConfig := filepath.Join(dir, "ssh_config")
	config = fmt.Sprintf("Host stream\n  HostName axv-stream-test\n  User %s\n  IdentityFile %s\n  UserKnownHostsFile %s\n  GlobalKnownHostsFile /dev/null\n  StrictHostKeyChecking yes\n  IdentitiesOnly yes\n  BatchMode yes\n  PasswordAuthentication no\n  ExitOnForwardFailure yes\n  ProxyCommand %s -test.run=^TestNativeProxyProcess$\n", current.Username, clientKey, knownHosts, strconv.Quote(os.Args[0]))
	if err = os.WriteFile(clientConfig, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(tool string, input io.Reader, args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, tool, append([]string{"-F", clientConfig}, args...)...)
		command.Stdin = input
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			t.Fatalf("%s %v: %v: %s", tool, args, err, stderr.String())
		}
		return output
	}
	payload := bytes.Repeat([]byte("SSH binary stream\x00\xff\n"), 8192)
	if output := run("ssh", bytes.NewReader(payload), "stream", "cat"); !bytes.Equal(output, payload) {
		t.Fatalf("SSH command truncated output: %d bytes", len(output))
	}
	local, remote, downloaded := filepath.Join(dir, "local"), filepath.Join(dir, "remote"), filepath.Join(dir, "downloaded")
	if err = os.WriteFile(local, payload, 0600); err != nil {
		t.Fatal(err)
	}
	run("scp", nil, local, "stream:"+remote)
	run("sftp", strings.NewReader(fmt.Sprintf("get %s %s\n", remote, downloaded)), "-b", "-", "stream")
	got, err := os.ReadFile(downloaded)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("SCP/SFTP changed contents: %d bytes, %v", len(got), err)
	}

	// A native OpenSSH client exercises local forwarding. The TCP endpoint
	// belongs to this invocation and is never exposed off loopback.
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	listen := availableTCPAddress(t)
	command := exec.CommandContext(ctx, "ssh", "-F", clientConfig, "-N", "-L", listen+":"+echo.Addr().String(), "stream")
	nativeProcess(t, ctx, command)
	localConn := waitTCP(t, ctx, listen)
	_ = localConn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = localConn.Write(payload); err != nil {
		t.Fatal(err)
	}
	localData := make([]byte, len(payload))
	_, err = io.ReadFull(localConn, localData)
	localConn.Close()
	if err != nil || !bytes.Equal(localData, payload) {
		t.Fatalf("local forwarding changed bytes: %v", err)
	}
	// The remote-forward channel is also exercised against real sshd. Using the
	// Go SSH client makes the ephemeral remote port observable without scraping
	// native OpenSSH diagnostics or reserving a supposedly free remote port.
	private, err := os.ReadFile(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hostPublic))
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	raw, err := net.Dial("tcp", request.URL.Host)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = Upgrade(ctx, raw, request)
	if err != nil {
		t.Fatal(err)
	}
	connection, channels, requests, err := ssh.NewClientConn(NewConn(raw), "axv-stream-test:22", &ssh.ClientConfig{User: current.Username, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(hostKey)})
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	client := ssh.NewClient(connection, channels, requests)
	defer client.Close()
	remoteListener, err := client.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remoteListener.Close()
	remoteDone := make(chan error, 1)
	go func() {
		conn, err := remoteListener.Accept()
		if err == nil {
			defer conn.Close()
			_, err = io.Copy(conn, conn)
		}
		remoteDone <- err
	}()
	conn := waitTCP(t, ctx, remoteListener.Addr().String())
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len(payload))
	_, err = io.ReadFull(conn, data)
	conn.Close()
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("remote forwarding changed bytes: %v", err)
	}
	if err = <-remoteDone; err != nil {
		t.Fatal(err)
	}
}
