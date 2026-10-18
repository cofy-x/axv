// Package hostconn verifies a compute host on the connection used for subsequent
// requests. Capturing a Unix socket path alone cannot identify its later owner.
package hostconn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type Binding struct {
	HostID            string `json:"hostId"`
	ConfigFingerprint string `json:"configFingerprint"`
}

var (
	ErrUnavailable = errors.New("VM service is not ready or uses an incompatible protocol; inspect its log and running executable")
	ErrChanged     = errors.New("VM service identity or configuration changed; the command was not redirected")
)

// Capture discovers a ready host, requiring any nonempty expected identity fields
// to match. The returned identity supplies the binding for later Verify calls.
func Capture(ctx context.Context, conn net.Conn, expected Binding) (net.Conn, compute.Runtime, error) {
	return handshake(ctx, conn, expected, true)
}

// Verify requires the complete expected binding on this connection. The caller
// retains ownership of conn and must close it on error. Cancellation closes conn
// to interrupt an in-progress handshake; success preserves any buffered bytes.
func Verify(ctx context.Context, conn net.Conn, expected Binding) (net.Conn, error) {
	verified, _, err := handshake(ctx, conn, expected, false)
	return verified, err
}

func handshake(ctx context.Context, conn net.Conn, expected Binding, capture bool) (net.Conn, compute.Runtime, error) {
	deadline := time.Now().Add(5 * time.Second)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, compute.Runtime{}, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	request, _ := http.NewRequest(http.MethodGet, "http://host/v1/runtime", nil)
	if err := request.Write(conn); err != nil {
		return nil, compute.Runtime{}, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, compute.Runtime{}, err
	}
	var identity compute.Runtime
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&identity)
	response.Body.Close()
	if err != nil {
		return nil, compute.Runtime{}, err
	}
	if response.StatusCode != http.StatusOK || response.Close || !identity.Ready || identity.ProtocolVersion != compute.ProtocolVersion || (capture && (identity.HostID == "" || identity.ConfigFingerprint == "")) {
		return nil, compute.Runtime{}, ErrUnavailable
	}
	binding := Binding{HostID: identity.HostID, ConfigFingerprint: identity.ConfigFingerprint}
	matches := binding == expected
	if capture {
		matches = (expected.HostID == "" || expected.HostID == identity.HostID) && (expected.ConfigFingerprint == "" || expected.ConfigFingerprint == identity.ConfigFingerprint)
	}
	if !matches {
		return nil, compute.Runtime{}, ErrChanged
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, compute.Runtime{}, err
	}
	return &bufferedConn{Conn: conn, reader: reader}, identity, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.reader.Read(b) }
