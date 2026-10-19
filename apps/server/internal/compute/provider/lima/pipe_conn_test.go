package lima

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func relayDriver(t *testing.T, script string) (*Driver, compute.Machine) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake limactl uses a POSIX shell")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "limactl")
	script = "#!/bin/sh\n[ \"$*\" = '--tty=false shell --workdir=/ vm-test -- socat STDIO UNIX-CONNECT:/run/axv/guest.sock' ] || exit 2\n" + script
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d := &Driver{config: compute.LimaConfig{Binary: binary}, root: root, home: root, owner: "relay-test-host"}
	m := compute.Machine{ID: "vm-test"}
	if err := owneddir.Ensure(d.directory(m), d.owner+":"+m.ID); err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.Write(filepath.Join(d.directory(m), "state.json"), state{MachineID: m.ID, Guest: guestproto.Config{MachineID: m.ID, Token: "relay-test-token"}}, 0600); err != nil {
		t.Fatal(err)
	}
	return d, m
}

func TestGuestConnectionDrainsResponseAfterRelayIsReaped(t *testing.T) {
	const body = `{"version":3,"machineId":"vm-test","ready":true,"sshAvailable":false}`
	d, m := relayDriver(t, fmt.Sprintf("printf 'HTTP/1.1 200 OK\\r\\nContent-Length: %d\\r\\nConnection: close\\r\\n\\r\\n%%s' '%s'\n", len(body), body))
	conn, err := d.DialGuest(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// Wait until the relay has been reaped before consuming any buffered bytes.
	// Its exit must not close the connection's unread response descriptor.
	select {
	case <-conn.(*pipeConn).done:
	case <-time.After(2 * time.Second):
		t.Fatal("response fixture did not exit")
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("reaping the relay closed its unread response: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("could not read response after relay exit: %v", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(data) != body {
		t.Fatalf("relay exit truncated the guest response: status=%d bytes=%d error=%v", response.StatusCode, len(data), err)
	}
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("drained relay did not reach EOF: %v", err)
	}
}

func TestGuestConnectionCloseReapsRelayAndInterruptsReads(t *testing.T) {
	d, m := relayDriver(t, "while IFS= read -r line; do printf '%s\\n' \"$line\"; done\n")
	conn, err := d.DialGuest(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	read := make(chan error, 1)
	go func() { _, err := conn.Read(make([]byte, 1)); read <- err }()
	closed := make(chan error, 1)
	go func() { closed <- conn.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connection close did not reap its relay")
	}
	select {
	case err := <-read:
		if err == nil {
			t.Fatal("closed connection retained a readable pipe")
		}
	case <-time.After(time.Second):
		t.Fatal("connection close left a blocked read")
	}
	if conn.(*pipeConn).cmd.ProcessState == nil {
		t.Fatal("closed connection left its relay unreaped")
	}
	if _, err := conn.Write([]byte("after close")); err == nil {
		t.Fatal("closed connection retained its input pipe")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("repeated close failed: %v", err)
	}
}

func TestGuestConnectionFailedStartReturnsNoConnection(t *testing.T) {
	d, m := relayDriver(t, "exit 0\n")
	d.config.Binary = filepath.Join(d.root, "missing-limactl")
	conn, err := d.DialGuest(context.Background(), m)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("failed relay startup returned a connection")
	}
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "missing-limactl") {
		t.Fatalf("relay startup lost its native cause: %v", err)
	}
}
