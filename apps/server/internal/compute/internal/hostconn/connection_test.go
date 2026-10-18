package hostconn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func runtimeResponse(status, version int, ready bool, host, fingerprint, headers string) string {
	body := fmt.Sprintf(`{"ready":%t,"hostId":%q,"configFingerprint":%q,"protocolVersion":%d}`, ready, host, fingerprint, version)
	return fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\n%s\r\n%s", status, http.StatusText(status), len(body), headers, body)
}

func responseConnection(t *testing.T, response string) net.Conn {
	t.Helper()
	conn, peer := net.Pipe()
	t.Cleanup(func() { conn.Close(); peer.Close() })
	go func() {
		defer peer.Close()
		request, err := http.ReadRequest(bufio.NewReader(peer))
		if err != nil {
			return
		}
		if request.Method != http.MethodGet || request.URL.Path != "/v1/runtime" {
			t.Errorf("unexpected identity request: %s %s", request.Method, request.URL.Path)
			return
		}
		if _, err := io.WriteString(peer, response); err == nil {
			io.Copy(io.Discard, peer)
		}
	}()
	return conn
}

func TestCaptureRequiresReadyIdentityAndHonorsPartialExpectation(t *testing.T) {
	for _, expected := range []Binding{{}, {HostID: "host"}, {ConfigFingerprint: "config"}, {HostID: "host", ConfigFingerprint: "config"}} {
		t.Run(fmt.Sprintf("%s/%s", expected.HostID, expected.ConfigFingerprint), func(t *testing.T) {
			conn := responseConnection(t, runtimeResponse(http.StatusOK, compute.ProtocolVersion, true, "host", "config", ""))
			verified, identity, err := Capture(t.Context(), conn, expected)
			if err != nil {
				t.Fatal(err)
			}
			defer verified.Close()
			if identity != (compute.Runtime{Ready: true, HostID: "host", ConfigFingerprint: "config", ProtocolVersion: compute.ProtocolVersion}) {
				t.Fatalf("incomplete captured identity: %+v", identity)
			}
		})
	}
}

func TestHandshakeRejectsUnavailableOrReplacedHosts(t *testing.T) {
	valid := Binding{HostID: "host", ConfigFingerprint: "config"}
	for _, test := range []struct {
		name     string
		response string
		expected Binding
		capture  bool
		want     error
	}{
		{"unready", runtimeResponse(200, compute.ProtocolVersion, false, "host", "config", ""), valid, false, ErrUnavailable},
		{"protocol", runtimeResponse(200, compute.ProtocolVersion-1, true, "host", "config", ""), valid, false, ErrUnavailable},
		{"status", runtimeResponse(503, compute.ProtocolVersion, true, "host", "config", ""), valid, false, ErrUnavailable},
		{"closing connection", runtimeResponse(200, compute.ProtocolVersion, true, "host", "config", "Connection: close\r\n"), valid, false, ErrUnavailable},
		{"missing captured host", runtimeResponse(200, compute.ProtocolVersion, true, "", "config", ""), Binding{}, true, ErrUnavailable},
		{"missing captured config", runtimeResponse(200, compute.ProtocolVersion, true, "host", "", ""), Binding{}, true, ErrUnavailable},
		{"changed host", runtimeResponse(200, compute.ProtocolVersion, true, "other", "config", ""), valid, false, ErrChanged},
		{"changed config", runtimeResponse(200, compute.ProtocolVersion, true, "host", "other", ""), valid, false, ErrChanged},
		{"captured host mismatch", runtimeResponse(200, compute.ProtocolVersion, true, "other", "config", ""), Binding{HostID: "host"}, true, ErrChanged},
		{"captured config mismatch", runtimeResponse(200, compute.ProtocolVersion, true, "host", "other", ""), Binding{ConfigFingerprint: "config"}, true, ErrChanged},
		{"strict empty config", runtimeResponse(200, compute.ProtocolVersion, true, "host", "config", ""), Binding{HostID: "host"}, false, ErrChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := responseConnection(t, test.response)
			var err error
			if test.capture {
				_, _, err = Capture(t.Context(), conn, test.expected)
			} else {
				_, err = Verify(t.Context(), conn, test.expected)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v; want %v", err, test.want)
			}
		})
	}
}

func TestVerifyMatchesAnExplicitEmptyFingerprint(t *testing.T) {
	conn := responseConnection(t, runtimeResponse(200, compute.ProtocolVersion, true, "host", "", ""))
	verified, err := Verify(t.Context(), conn, Binding{HostID: "host"})
	if err != nil {
		t.Fatal(err)
	}
	verified.Close()
}

type deadlineConn struct {
	net.Conn
	deadlines []time.Time
}

func (c *deadlineConn) SetDeadline(deadline time.Time) error {
	c.deadlines = append(c.deadlines, deadline)
	return c.Conn.SetDeadline(deadline)
}

func TestHandshakePreservesBufferedBytesAndConnectionOwnership(t *testing.T) {
	conn, peer := net.Pipe()
	t.Cleanup(func() { conn.Close(); peer.Close() })
	more := make(chan struct{})
	go func() {
		defer peer.Close()
		if _, err := http.ReadRequest(bufio.NewReader(peer)); err != nil {
			return
		}
		if _, err := io.WriteString(peer, runtimeResponse(200, compute.ProtocolVersion, true, "host", "config", "")+"buffered"); err != nil {
			return
		}
		<-more
		io.WriteString(peer, "continued")
	}()
	deadline := time.Now().Add(2 * time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	tracked := &deadlineConn{Conn: conn}
	verified, err := Verify(ctx, tracked, Binding{HostID: "host", ConfigFingerprint: "config"})
	if err != nil {
		close(more)
		t.Fatal(err)
	}
	if len(tracked.deadlines) != 2 || !tracked.deadlines[0].Equal(deadline) || !tracked.deadlines[1].IsZero() {
		close(more)
		t.Fatalf("handshake did not apply and clear the caller's deadline: %v", tracked.deadlines)
	}
	buffered := make([]byte, len("buffered"))
	if _, err := io.ReadFull(verified, buffered); err != nil || string(buffered) != "buffered" {
		close(more)
		t.Fatalf("lost bytes after handshake response: %q %v", buffered, err)
	}
	cancel()
	close(more)
	continued := make([]byte, len("continued"))
	if _, err := io.ReadFull(verified, continued); err != nil || string(continued) != "continued" {
		t.Fatalf("completed handshake still owns connection cancellation: %q %v", continued, err)
	}
}

func TestHandshakeLimitsItsOwnDeadline(t *testing.T) {
	conn := responseConnection(t, runtimeResponse(200, compute.ProtocolVersion, true, "host", "config", ""))
	tracked := &deadlineConn{Conn: conn}
	before := time.Now()
	verified, err := Verify(t.Context(), tracked, Binding{HostID: "host", ConfigFingerprint: "config"})
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	if len(tracked.deadlines) != 2 || tracked.deadlines[0].Before(before.Add(5*time.Second)) || tracked.deadlines[0].After(time.Now().Add(5*time.Second)) || !tracked.deadlines[1].IsZero() {
		t.Fatalf("unexpected handshake deadline: %v", tracked.deadlines)
	}
}

func TestCancellationClosesAnIncompleteHandshake(t *testing.T) {
	conn, peer := net.Pipe()
	t.Cleanup(func() { conn.Close(); peer.Close() })
	requestRead := make(chan struct{})
	peerClosed := make(chan struct{})
	go func() {
		defer close(peerClosed)
		if _, err := http.ReadRequest(bufio.NewReader(peer)); err != nil {
			return
		}
		close(requestRead)
		io.Copy(io.Discard, peer)
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := Verify(ctx, conn, Binding{HostID: "host", ConfigFingerprint: "config"})
		finished <- err
	}()
	select {
	case <-requestRead:
	case <-time.After(2 * time.Second):
		t.Fatal("host did not receive handshake request")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled handshake succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled handshake remained blocked")
	}
	select {
	case <-peerClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled handshake left its connection open")
	}
}

func TestMalformedRuntimeResponseFailsVerification(t *testing.T) {
	conn := responseConnection(t, "HTTP/1.1 200 OK\r\nContent-Length: 1\r\n\r\n{")
	if verified, err := Verify(t.Context(), conn, Binding{}); err == nil || verified != nil {
		t.Fatalf("accepted incomplete runtime identity: %v", err)
	}
}
