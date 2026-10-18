package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/hostconn"
)

func socketHost(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "axv-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "host.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return path
}

func runtime(w http.ResponseWriter, host string) {
	json.NewEncoder(w).Encode(map[string]any{"ready": true, "hostId": host, "configFingerprint": "config", "protocolVersion": compute.ProtocolVersion})
}

func TestProbeUsesRuntimeWithoutProviderChecks(t *testing.T) {
	identity := compute.Runtime{Ready: true, HostID: "host", ConfigFingerprint: "config", ProtocolVersion: compute.ProtocolVersion}
	var inventoryRequests atomic.Int32
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runtime" {
			inventoryRequests.Add(1)
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(identity)
	}))
	actual, err := Probe(t.Context(), socket, Binding{ConfigFingerprint: "config"})
	if err != nil || actual != identity {
		t.Fatalf("runtime probe: %+v %v", actual, err)
	}
	for _, expected := range []Binding{{HostID: "other"}, {ConfigFingerprint: "other"}} {
		if _, err := Probe(t.Context(), socket, expected); !errors.Is(err, hostconn.ErrChanged) {
			t.Fatalf("accepted another host or configuration: %+v %v", expected, err)
		}
	}
	if inventoryRequests.Load() != 0 {
		t.Fatal("runtime probe requested provider inventory")
	}
}

func TestProbeCancellationInterruptsRuntimeRequest(t *testing.T) {
	requested := make(chan struct{})
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		<-r.Context().Done()
	}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := Probe(ctx, socket, Binding{})
		finished <- err
	}()
	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime request did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("probe did not retain cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime probe ignored cancellation")
	}
}

func TestReadsRejectChangedConfigurationBeforeRequest(t *testing.T) {
	var fingerprint atomic.Value
	fingerprint.Store("original")
	var reads atomic.Int32
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			json.NewEncoder(w).Encode(compute.Runtime{Ready: true, HostID: "host", ConfigFingerprint: fingerprint.Load().(string), ProtocolVersion: compute.ProtocolVersion})
			return
		}
		reads.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"machines": []any{}})
	}))
	c, err := Connect(t.Context(), socket, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Machines(t.Context()); err != nil {
		t.Fatal(err)
	}
	fingerprint.Store("replacement")
	if _, err := c.Machines(t.Context()); !errors.Is(err, hostconn.ErrChanged) {
		t.Fatalf("machine query accepted changed configuration: %v", err)
	}
	if _, err := c.Info(t.Context()); !errors.Is(err, hostconn.ErrChanged) {
		t.Fatalf("inventory query accepted changed configuration: %v", err)
	}
	if reads.Load() != 1 {
		t.Fatalf("replacement received read requests: %d", reads.Load()-1)
	}
}

func TestEveryActualConnectionVerifiesHostBeforeMutation(t *testing.T) {
	var identity atomic.Value
	identity.Store("host-original")
	var writes atomic.Int32
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			runtime(w, identity.Load().(string))
			return
		}
		if r.Method == "POST" {
			writes.Add(1)
		}
		json.NewEncoder(w).Encode(map[string]any{"machines": []any{}})
	}))
	c, err := Connect(context.Background(), socket, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Machines(context.Background()); err != nil {
		t.Fatal(err)
	}
	identity.Store("host-replacement")
	_, err = c.Create(context.Background(), compute.CreateRequest{RequestID: "request-original"})
	if err == nil || !strings.Contains(err.Error(), "not redirected") || writes.Load() != 0 {
		t.Fatalf("replacement received mutation: writes=%d error=%v", writes.Load(), err)
	}
	if _, err = c.Dial(context.Background()); err == nil {
		t.Fatal("SSH dial accepted a replacement")
	}
}

func TestLostMutationResponseIsNotRetriedAndRequestLookupEscapes(t *testing.T) {
	var writes atomic.Int32
	requestID := "original/request?#%value"
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			runtime(w, "host")
			return
		}
		if r.Method == "POST" {
			writes.Add(1)
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		if !strings.HasSuffix(r.URL.Path, requestID) {
			t.Errorf("request path was corrupted: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(compute.Operation{ID: "operation", RequestID: requestID, Status: "succeeded"})
	}))
	c, err := Connect(context.Background(), socket, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Create(context.Background(), compute.CreateRequest{RequestID: requestID})
	if err == nil || writes.Load() != 1 {
		t.Fatalf("lost response was retried: writes=%d err=%v", writes.Load(), err)
	}
	op, err := c.Operation(context.Background(), requestID, true)
	if err != nil || op.RequestID != requestID {
		t.Fatalf("cannot recover original: %+v %v", op, err)
	}
}

func TestConnectRejectsUnreadyAndWrongProtocol(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "unready", true: "old protocol"}[ready], func(t *testing.T) {
			socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"ready": ready, "hostId": "host", "configFingerprint": "config", "protocolVersion": compute.ProtocolVersion - 1})
			}))
			if c, err := Connect(context.Background(), socket, Binding{}); err == nil {
				c.Close()
				t.Fatal("accepted incompatible host")
			}
		})
	}
}
