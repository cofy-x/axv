//go:build linux

package guest

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex/vmproxy"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/mdlayher/vsock"
)

// guestProxyBridge forwards guest loopback HTTP requests to the host-side
// zero-credential LLM proxy over vsock (Firecracker) or a Unix socket (Lima/tests).
type guestProxyBridge struct {
	mu       sync.Mutex
	listener net.Listener
	addr     string
	closed   bool
}

func dialHostProxy(ctx context.Context) (net.Conn, error) {
	if sock := os.Getenv("AXV_GUEST_CODEX_PROXY_SOCKET"); sock != "" {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", sock)
	}
	return vsock.Dial(vsock.Host, protocol.ProxyPort, nil)
}

func (b *guestProxyBridge) probe(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	transport := &http.Transport{
		DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
			return dialHostProxy(c)
		},
	}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://host-proxy/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 256))
	return resp.StatusCode == http.StatusOK
}

func (b *guestProxyBridge) ensure(ctx context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return "", fmt.Errorf("guest proxy bridge is closed")
	}
	if b.addr != "" {
		return b.addr, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	b.listener = listener
	b.addr = listener.Addr().String()
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go func(local net.Conn) {
				defer local.Close()
				upstream, err := dialHostProxy(ctx)
				if err != nil {
					return
				}
				defer upstream.Close()
				done := make(chan struct{}, 2)
				copySide := func(dst io.Writer, src io.Reader) {
					_, _ = io.Copy(dst, src)
					done <- struct{}{}
				}
				go copySide(upstream, local)
				go copySide(local, upstream)
				<-done
			}(client)
		}
	}()
	return b.addr, nil
}

func (b *guestProxyBridge) env(ctx context.Context) []string {
	if !b.probe(ctx) {
		return nil
	}
	addr, err := b.ensure(ctx)
	if err != nil {
		return nil
	}
	return []string{
		"OPENAI_BASE_URL=http://" + addr + "/v1",
		"OPENAI_API_KEY=" + vmproxy.ProxyToken,
	}
}

func (b *guestProxyBridge) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.listener != nil {
		_ = b.listener.Close()
		b.listener = nil
		b.addr = ""
	}
}
