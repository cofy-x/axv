package sshstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.AcceptTCP()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

func TestInputEOFStillDeliversCompleteServerOutput(t *testing.T) {
	target, peer := tcpPair(t)
	request := bytes.Repeat([]byte{0, 1, 2, 255}, MaxFrame)
	response := bytes.Repeat([]byte("final output after stdin EOF\x00"), 8192)
	backend := make(chan error, 1)
	go func() {
		data, err := io.ReadAll(peer)
		if err == nil && !bytes.Equal(data, request) {
			err = fmt.Errorf("input was truncated")
		}
		if err == nil {
			_, err = peer.Write(response)
		}
		if err == nil {
			err = peer.CloseWrite()
		}
		backend <- err
	}()
	bridge := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Accept(w, r)
		if err != nil {
			bridge <- err
			return
		}
		bridge <- Bridge(r.Context(), conn, target)
	}))
	defer server.Close()
	r, _ := http.NewRequest(http.MethodGet, server.URL+"/attach", nil)
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err = Upgrade(ctx, conn, r)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err = Pump(ctx, conn, bytes.NewReader(request), &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), response) {
		t.Fatalf("response length %d, want %d", got.Len(), len(response))
	}
	if err = <-backend; err != nil {
		t.Fatal(err)
	}
	if err = <-bridge; err != nil {
		t.Fatal(err)
	}
}

func TestRemoteHalfCloseDoesNotDiscardRemainingInput(t *testing.T) {
	target, peer := tcpPair(t)
	client, server := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Bridge(ctx, server, target) }()
	backend := make(chan error, 1)
	go func() {
		_, err := peer.Write([]byte("ready"))
		if err == nil {
			err = peer.CloseWrite()
		}
		if err == nil {
			var data []byte
			data, err = io.ReadAll(peer)
			if string(data) != "late input" {
				err = fmt.Errorf("input = %q", data)
			}
		}
		backend <- err
	}()
	framed := NewConn(client)
	data, err := io.ReadAll(framed)
	if err != nil || string(data) != "ready" {
		t.Fatalf("read = %q, %v", data, err)
	}
	if _, err = framed.Write([]byte("late input")); err != nil {
		t.Fatal(err)
	}
	if err = framed.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err = <-backend; err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPumpRemoteEOFClosesOutputAndStillAcceptsLateInput(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	input, sendInput := io.Pipe()
	output, receiveOutput := io.Pipe()
	defer input.Close()
	defer sendInput.Close()
	defer output.Close()
	defer receiveOutput.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pumped := make(chan error, 1)
	go func() { pumped <- Pump(ctx, client, input, receiveOutput) }()
	remote := make(chan error, 1)
	go func() {
		framed := NewConn(server)
		_, err := framed.Write([]byte("server output"))
		if err == nil {
			err = framed.CloseWrite()
		}
		if err == nil {
			var data []byte
			data, err = io.ReadAll(framed)
			if string(data) != "late client input" {
				err = fmt.Errorf("client input = %q", data)
			}
		}
		remote <- err
	}()
	data, err := io.ReadAll(output)
	if err != nil || string(data) != "server output" {
		t.Fatalf("server output = %q, %v", data, err)
	}
	if _, err = io.WriteString(sendInput, "late client input"); err != nil {
		t.Fatal(err)
	}
	if err = sendInput.Close(); err != nil {
		t.Fatal(err)
	}
	if err = <-pumped; err != nil {
		t.Fatal(err)
	}
	if err = <-remote; err != nil {
		t.Fatal(err)
	}
}

func TestUpgradePreservesBufferedFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: upgrade\r\nUpgrade: %s\r\n\r\n", Protocol)
		buffer.Write([]byte{dataFrame, 0, 0, 0, 3, 's', 's', 'h', eofFrame, 0, 0, 0, 0})
		buffer.Flush()
	}))
	defer server.Close()
	request, _ := http.NewRequest("GET", server.URL, nil)
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	conn, err = Upgrade(context.Background(), conn, request)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data, err := io.ReadAll(NewConn(conn))
	if err != nil || string(data) != "ssh" {
		t.Fatalf("buffered data = %q, %v", data, err)
	}
}

func TestMalformedAndTruncatedFramesFail(t *testing.T) {
	oversize := []byte{dataFrame, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(oversize[1:], MaxFrame+1)
	for _, data := range [][]byte{oversize, {0, 0, 0, 0, 1}, {dataFrame, 0, 0, 0, 0}, {eofFrame, 0, 0, 0, 1}, {dataFrame, 0, 0, 0, 2, 'x'}} {
		t.Run(fmt.Sprintf("%x", data), func(t *testing.T) {
			reader, writer := net.Pipe()
			defer reader.Close()
			go func() { defer writer.Close(); writer.Write(data) }()
			if _, err := NewConn(reader).Read(make([]byte, 16)); err == nil {
				t.Fatal("accepted malformed frame")
			}
		})
	}
}

func TestBridgeCancellationClosesBothDirections(t *testing.T) {
	target, _ := tcpPair(t)
	client, server := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Bridge(ctx, server, target) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation left a stream blocked")
	}
}
