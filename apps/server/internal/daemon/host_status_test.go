package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func providerStatusHost(t *testing.T, s app.Settings, inventory http.HandlerFunc) {
	t.Helper()
	listener, err := net.Listen("unix", s.Compute.Socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			json.NewEncoder(w).Encode(compute.Runtime{Ready: true, HostID: "fixture", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: s.Host.Fingerprint()})
			return
		}
		if r.URL.Path == "/v1/info" {
			inventory(w, r)
			return
		}
		http.NotFound(w, r)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
}

func TestHostStatusDisplaysExplicitProviderProbeResults(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		want   string
	}{
		{name: "provider available", status: http.StatusOK, want: "lima: available"},
		{name: "provider unavailable", status: http.StatusOK, want: "lima: unavailable (missing runtime)"},
		{name: "probe failed", status: http.StatusServiceUnavailable, want: "VM provider status: unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := managedHostSettings(t)
			var probes atomic.Int32
			providerStatusHost(t, s, func(w http.ResponseWriter, r *http.Request) {
				probes.Add(1)
				if test.status != http.StatusOK {
					http.Error(w, "inventory unavailable", test.status)
					return
				}
				if r.Header.Get("X-AXV-Host-ID") != "fixture" {
					t.Error("provider query did not retain the verified host")
				}
				json.NewEncoder(w).Encode(compute.Info{Providers: []compute.Capability{{Provider: "lima", Available: test.name == "provider available", Reason: "missing runtime"}}})
			})
			var output bytes.Buffer
			if err := printHost(t.Context(), s, &output); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "VM controller: ready (fixture)") || !strings.Contains(output.String(), test.want) || probes.Load() != 1 {
				t.Fatalf("provider status: %s; probes=%d", output.String(), probes.Load())
			}
		})
	}
}

func TestHostStatusCancelsProviderProbe(t *testing.T) {
	s := managedHostSettings(t)
	requested := make(chan struct{})
	providerStatusHost(t, s, func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var output bytes.Buffer
	finished := make(chan error, 1)
	go func() { finished <- printHost(ctx, s, &output) }()
	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("provider probe did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) || !strings.Contains(output.String(), "VM provider status: unavailable") {
			t.Fatalf("provider cancellation was lost: %v %s", err, output.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider probe ignored cancellation")
	}
}
