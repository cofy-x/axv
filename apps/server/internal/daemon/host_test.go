package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type hostSupervisor struct {
	starts  []Job
	stops   []Job
	loaded  map[string]bool
	onStart func(Job) error
}

func (s *hostSupervisor) Loaded(_ context.Context, j Job) (bool, error) {
	return s.loaded[j.Label], nil
}
func (s *hostSupervisor) Start(_ context.Context, j Job) error {
	s.starts = append(s.starts, j)
	if s.onStart != nil {
		if err := s.onStart(j); err != nil {
			return err
		}
	}
	s.loaded[j.Label] = true
	return nil
}
func (s *hostSupervisor) Stop(_ context.Context, j Job) error {
	s.stops = append(s.stops, j)
	delete(s.loaded, j.Label)
	return nil
}

func managedHostFixture(t *testing.T) (app.Settings, manifest) {
	t.Helper()
	s := managedHostSettings(t)
	home := s.Home
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(s.RunDir(), "desktop-test.json")
	label := jobLabel(home, "desktop", marker)
	job := Job{Role: "desktop", Label: label, Args: []string{filepath.Join(home, "bin", "axv-test"), "_desktop", "--snapshot", marker}, Marker: marker, Definition: filepath.Join(s.RunDir(), label+".plist"), Log: filepath.Join(home, "logs", "desktop.log"), Environment: map[string]string{"AXV_HOME": home}}
	original := s.Snapshot()
	original.Compute = profile.Compute{Mode: "disabled"}
	original.Host = nil
	m := manifest{Version: 4, Home: home, Desktop: &original, Jobs: []Job{job}}
	if err := jsonfile.Write(manifestPath(home), m, 0600); err != nil {
		t.Fatal(err)
	}
	return s, m
}

func managedHostSettings(t *testing.T) app.Settings {
	t.Helper()
	home, err := os.MkdirTemp("", "axv-dh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	s := app.Settings{Inputs: desktoplaunch.Inputs{Home: home, LaunchID: "fixture-launch"}, ComputeSettings: app.ComputeSettings{Compute: profile.Compute{Mode: "managed", HostConfig: filepath.Join(home, "host.json"), Socket: filepath.Join(home, "control.sock")}}}
	s.Host = &compute.Config{DataDir: filepath.Join(home, "compute"), Socket: s.Compute.Socket, Lima: &compute.LimaConfig{Binary: "/fake/limactl", Version: compute.SupportedLimaVersion}}
	return s
}

func hostListener(t *testing.T, s app.Settings) func() {
	t.Helper()
	listener, err := net.Listen("unix", s.Compute.Socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runtime":
			json.NewEncoder(w).Encode(compute.Runtime{Ready: true, HostID: "fixture", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: s.Host.Fingerprint()})
		case "/v1/info":
			json.NewEncoder(w).Encode(compute.Info{HostID: "fixture", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: s.Host.Fingerprint(), Providers: []compute.Capability{{Provider: "lima", Available: true}}})
		case "/v1/machines":
			json.NewEncoder(w).Encode(map[string]any{"machines": []compute.Machine{}})
		default:
			http.NotFound(w, r)
		}
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return func() { server.Close() }
}

func TestManagedHostOwnershipIsPublishedBeforeLaunchAndRetryReusesIt(t *testing.T) {
	s, _ := managedHostFixture(t)
	supervisor := &hostSupervisor{loaded: map[string]bool{}}
	fail := true
	supervisor.onStart = func(job Job) error {
		m, err := readManifest(s.Home)
		if err != nil || m.OwnedHost == nil || m.OwnedHost.Fingerprint() != s.Host.Fingerprint() || len(m.Jobs) != 2 || m.Jobs[0].Marker != job.Marker {
			t.Fatalf("host launched before durable ownership: %#v %v", m, err)
		}
		if m.Desktop.Compute.Mode != "disabled" {
			t.Fatal("host startup rewrote immutable desktop settings")
		}
		if fail {
			return errors.New("fake launch failure")
		}
		hostListener(t, s)
		return nil
	}
	if _, err := ensureHost(context.Background(), s, io.Discard, supervisor); err == nil {
		t.Fatal("launch failure masked")
	}
	fail = false
	stop, err := ensureHost(context.Background(), s, io.Discard, supervisor)
	if err != nil {
		t.Fatal(err)
	}
	if stop != nil {
		t.Fatal("desktop owns native host shutdown")
	}
	if len(supervisor.starts) != 2 || supervisor.starts[0].Marker != supervisor.starts[1].Marker {
		t.Fatal("retry replaced the recorded host job")
	}
	if _, err := ensureHost(context.Background(), s, io.Discard, supervisor); err != nil {
		t.Fatal(err)
	}
	if len(supervisor.starts) != 2 {
		t.Fatal("ready host was started again")
	}
}

func TestExistingHostIsReusedWithoutClaimingItsSupervisor(t *testing.T) {
	s, _ := managedHostFixture(t)
	hostListener(t, s)
	supervisor := &hostSupervisor{loaded: map[string]bool{}}
	if stop, err := ensureHost(context.Background(), s, io.Discard, supervisor); err != nil || stop != nil {
		t.Fatalf("reuse: %v", err)
	}
	m, err := readManifest(s.Home)
	if err != nil || m.OwnedHost != nil || len(m.Jobs) != 1 || len(supervisor.starts) != 0 {
		t.Fatalf("claimed independent service: %#v %v", m, err)
	}
}

func TestManagedHostRefusesChangedOwnedConfiguration(t *testing.T) {
	s, m := managedHostFixture(t)
	job, err := hostJob(s, m.Jobs[0].Args[0])
	if err != nil {
		t.Fatal(err)
	}
	m.OwnedHost = s.Host
	m.Jobs = append([]Job{job}, m.Jobs...)
	if err := jsonfile.Write(manifestPath(s.Home), m, 0600); err != nil {
		t.Fatal(err)
	}
	next := *s.Host
	next.DataDir = filepath.Join(s.Home, "other-storage")
	s.Host = &next
	supervisor := &hostSupervisor{loaded: map[string]bool{job.Label: true}}
	if _, err := ensureHost(context.Background(), s, io.Discard, supervisor); err == nil {
		t.Fatal("replaced the owned service configuration")
	}
	if len(supervisor.starts) != 0 || len(supervisor.stops) != 0 {
		t.Fatal("changed an existing service")
	}
}

func TestHostActivationLockWaitCancelsDuringDesktopShutdown(t *testing.T) {
	s, _ := managedHostFixture(t)
	lock, err := profile.Lock(s.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := commandLock(ctx, s.Home); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("activation prevented desktop shutdown while launcher held command.lock")
	}
}
