//go:build darwin || linux

package remoteaccess

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/terminal"
)

func TestClosingRemoteTerminalsPreservesTheSharedLocalTerminal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	t.Setenv("BASH_ENV", "")
	manager := terminal.New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/terminal", manager.Connect)
	mux.HandleFunc("GET /api/terminal/info", manager.Info)
	localServer := httptest.NewServer(mux)
	remoteServer := httptest.NewUnstartedServer(nil)
	input := options()
	input.Origin = "https://" + remoteServer.Listener.Addr().String()
	input.AllowRoute = func(r *http.Request) bool { return r.URL.Path == "/api/terminal" || r.URL.Path == "/api/terminal/info" }
	m := newMiddleware(t, input)
	remoteServer.Config.Handler = m.Wrap(mux)
	remoteServer.StartTLS()
	t.Cleanup(func() { m.Close(); manager.Close(); remoteServer.Close(); localServer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(server *httptest.Server) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/terminal", &websocket.DialOptions{HTTPClient: server.Client(), HTTPHeader: http.Header{"Origin": []string{server.URL}}})
		if err != nil {
			t.Fatal(err)
		}
		kind, data, err := conn.Read(ctx)
		if err != nil || kind != websocket.MessageText || !strings.Contains(string(data), `"ready":true`) {
			_ = conn.CloseNow()
			t.Fatalf("shared terminal not ready: %s %v", data, err)
		}
		return conn
	}
	local := dial(localServer)
	defer local.CloseNow()
	remote := dial(remoteServer)
	defer remote.CloseNow()
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		manager.Close()
		t.Fatal("remote terminal did not detach and reap its shell during shutdown")
	}
	for {
		_, _, err := remote.Read(ctx)
		if err != nil {
			break
		}
	}
	// Both listeners use the same Manager. Closing the remote entrypoint must
	// preserve its local shell and the Manager's remaining capacity.
	if err := local.Write(ctx, websocket.MessageBinary, []byte("printf 'AXV_LOCAL_%s\\n' alive\r")); err != nil {
		t.Fatalf("remote shutdown affected the local terminal: %v", err)
	}
	var output strings.Builder
	for !strings.Contains(output.String(), "AXV_LOCAL_alive") {
		kind, data, err := local.Read(ctx)
		if err != nil {
			t.Fatalf("local terminal stopped with remote access: %v", err)
		}
		if kind == websocket.MessageBinary {
			output.Write(data)
		}
		if output.Len() > 64<<10 {
			t.Fatal("unexpectedly large local terminal output")
		}
	}
	newLocal := dial(localServer)
	_ = newLocal.CloseNow()
}

func TestRetaggingAPeerClosesOnlyItsRemoteTerminal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	t.Setenv("BASH_ENV", "")
	manager := terminal.New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/terminal", manager.Connect)
	localServer := httptest.NewServer(mux)
	remoteServer := httptest.NewUnstartedServer(nil)
	input := options()
	input.Origin = "https://" + remoteServer.Listener.Addr().String()
	input.IdentityRefresh = 5 * time.Millisecond
	var mu sync.Mutex
	firstPeer := ""
	revoked := false
	input.WhoIs = func(_ context.Context, peer string) (Identity, error) {
		mu.Lock()
		defer mu.Unlock()
		if firstPeer == "" {
			firstPeer = peer
		}
		identity := ownerIdentity()
		identity.Tagged = peer == firstPeer && revoked
		return identity, nil
	}
	m := newMiddleware(t, input)
	remoteServer.Config.Handler = m.Wrap(mux)
	remoteServer.StartTLS()
	t.Cleanup(func() { m.Close(); manager.Close(); remoteServer.Close(); localServer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(server *httptest.Server) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/terminal", &websocket.DialOptions{HTTPClient: server.Client(), HTTPHeader: http.Header{"Origin": []string{server.URL}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		kind, data, err := conn.Read(ctx)
		if err != nil || kind != websocket.MessageText || !strings.Contains(string(data), `"ready":true`) {
			t.Fatalf("terminal not ready: %s %v", data, err)
		}
		return conn
	}
	revokedTerminal := dial(remoteServer)
	otherRemote := dial(remoteServer)
	local := dial(localServer)
	mu.Lock()
	revoked = true
	mu.Unlock()
	closed, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	for {
		_, _, err := revokedTerminal.Read(closed)
		if err != nil {
			if closed.Err() != nil {
				t.Fatal("retagged peer kept its upgraded terminal")
			}
			break
		}
	}
	for _, conn := range []*websocket.Conn{otherRemote, local} {
		if err := conn.Write(ctx, websocket.MessageBinary, []byte("printf 'AXV_%s\\n' unaffected\r")); err != nil {
			t.Fatalf("peer revocation affected another connection: %v", err)
		}
		var output strings.Builder
		for !strings.Contains(output.String(), "AXV_unaffected") {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("unrelated terminal stopped during peer revocation: %v", err)
			}
			if kind == websocket.MessageBinary {
				output.Write(data)
			}
			if output.Len() > 64<<10 {
				t.Fatal("unexpectedly large terminal output")
			}
		}
	}
}
