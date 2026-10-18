// Package sshstream carries a standard SSH byte stream over HTTP upgrades.
// Explicit EOF frames preserve half-closes across transports without CloseWrite.
package sshstream

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const Protocol = "axv-ssh-v1"
const MaxFrame = 32 << 10

const (
	dataFrame byte = 1
	eofFrame  byte = 2
)

type Conn struct {
	net.Conn
	readMu   sync.Mutex
	writeMu  sync.Mutex
	pending  []byte
	readEOF  bool
	writeEOF bool
}

func NewConn(conn net.Conn) *Conn { return &Conn{Conn: conn} }

func (c *Conn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	if c.readEOF {
		return 0, io.EOF
	}
	var header [5]byte
	if _, err := io.ReadFull(c.Conn, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return 0, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if header[0] == eofFrame && size == 0 {
		c.readEOF = true
		return 0, io.EOF
	}
	if header[0] != dataFrame || size == 0 || size > MaxFrame {
		return 0, fmt.Errorf("invalid SSH stream frame")
	}
	c.pending = make([]byte, int(size))
	if _, err := io.ReadFull(c.Conn, c.pending); err != nil {
		c.pending = nil
		return 0, err
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (c *Conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.writeEOF {
		return 0, net.ErrClosed
	}
	total := 0
	for len(p) > 0 {
		size := min(len(p), MaxFrame)
		var header [5]byte
		header[0] = dataFrame
		binary.BigEndian.PutUint32(header[1:], uint32(size))
		if err := writeAll(c.Conn, header[:]); err != nil {
			return total, err
		}
		if err := writeAll(c.Conn, p[:size]); err != nil {
			return total, err
		}
		total += size
		p = p[size:]
	}
	return total, nil
}

func (c *Conn) CloseWrite() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.writeEOF {
		return nil
	}
	c.writeEOF = true
	return writeAll(c.Conn, []byte{eofFrame, 0, 0, 0, 0})
}

// Bridge runs until both peers have half-closed, or a failure cancels both sides.
// target must support CloseWrite, as guest TCP connections do.
func Bridge(ctx context.Context, raw net.Conn, target net.Conn) error {
	stream := NewConn(raw)
	defer stream.Close()
	defer target.Close()
	stop := context.AfterFunc(ctx, func() { stream.Close(); target.Close() })
	defer stop()
	results := make(chan error, 2)
	copySide := func(dst io.Writer, src io.Reader, closeWrite func() error) {
		_, err := io.Copy(dst, src)
		if err == nil {
			err = closeWrite()
		}
		results <- err
	}
	half, ok := target.(interface{ CloseWrite() error })
	if !ok {
		return fmt.Errorf("SSH target does not support half-close")
	}
	go copySide(target, stream, half.CloseWrite)
	go copySide(stream, target, stream.CloseWrite)
	first := <-results
	if first != nil {
		stream.Close()
		target.Close()
	}
	second := <-results
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(first, second)
}

// Pump connects ProxyCommand stdio to the framed transport. Each clean EOF
// closes only that direction. Output is closed, when possible, on remote EOF
// so the native client can observe it while still finishing its input. Both
// streams are closed on failure or cancellation to release blocked pipe I/O.
func Pump(ctx context.Context, raw net.Conn, input io.Reader, output io.Writer) error {
	stream := NewConn(raw)
	defer stream.Close()
	closeInput := func() {
		if c, ok := input.(io.Closer); ok {
			_ = c.Close()
		}
	}
	closeOutput := func() {
		if c, ok := output.(io.Closer); ok {
			_ = c.Close()
		}
	}
	defer closeInput()
	defer closeOutput()
	stop := context.AfterFunc(ctx, func() { stream.Close(); closeInput(); closeOutput() })
	defer stop()
	written := make(chan error, 1)
	go func() {
		_, err := io.Copy(stream, input)
		if err == nil {
			err = stream.CloseWrite()
		}
		written <- err
		if err != nil {
			stream.Close()
		}
	}()
	_, err := io.Copy(output, stream)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		stream.Close()
		closeInput()
		select {
		case inputErr := <-written:
			return errors.Join(inputErr, err)
		default:
			return err
		}
	}
	closeOutput()
	select {
	case inputErr := <-written:
		return inputErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func isUpgrade(header http.Header) bool {
	if !strings.EqualFold(header.Get("Upgrade"), Protocol) {
		return false
	}
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

// Upgrade performs the bounded HTTP handshake on a verified private connection.
// The returned connection has no deadline and preserves bytes after the headers.
func Upgrade(ctx context.Context, conn net.Conn, request *http.Request) (net.Conn, error) {
	request = request.Clone(ctx)
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", Protocol)
	deadline := time.Now().Add(10 * time.Second)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := request.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if response.StatusCode != http.StatusSwitchingProtocols || !isUpgrade(response.Header) {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		response.Body.Close()
		conn.Close()
		return nil, fmt.Errorf("SSH transport HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

// Accept upgrades an already authorized request. Its caller owns the connection
// until Bridge finishes; HTTP server shutdown alone does not close upgrades.
func Accept(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	if r.Method != http.MethodGet || !isUpgrade(r.Header) {
		http.Error(w, "An AXV SSH stream upgrade is required.", http.StatusBadRequest)
		return nil, fmt.Errorf("SSH transport requires Upgrade: %s", Protocol)
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "HTTP connection cannot upgrade.", http.StatusInternalServerError)
		return nil, fmt.Errorf("HTTP connection cannot upgrade")
	}
	conn, buffer, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	if _, err = fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\nCache-Control: no-store\r\n\r\n", Protocol); err == nil {
		err = buffer.Flush()
	}
	if err == nil {
		err = conn.SetDeadline(time.Time{})
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &bufferedConn{Conn: conn, reader: buffer.Reader}, nil
}
