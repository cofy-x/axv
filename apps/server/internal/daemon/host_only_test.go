package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

func hostOnlyFixture(t *testing.T) (app.Settings, manifest) {
	t.Helper()
	s := managedHostSettings(t)
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	job, err := hostJob(s, filepath.Join(s.Home, "bin", "axv-test"))
	if err != nil {
		t.Fatal(err)
	}
	m := manifest{Version: 4, Home: s.Home, OwnedHost: s.Host, Jobs: []Job{job}}
	if err := jsonfile.Write(manifestPath(s.Home), m, 0600); err != nil {
		t.Fatal(err)
	}
	return s, m
}

func TestHostOnlyLaunchPublishesOwnershipAndUnlocksBeforeReadiness(t *testing.T) {
	s := managedHostSettings(t)
	supervisor := &hostSupervisor{loaded: map[string]bool{}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lockResult := make(chan error, 1)
	supervisor.onStart = func(job Job) error {
		m, err := readManifest(s.Home)
		if err != nil || m.Desktop != nil || m.OwnedHost == nil || len(m.Jobs) != 1 || m.Jobs[0].Marker != job.Marker {
			t.Fatalf("host launched without independent ownership: %#v %v", m, err)
		}
		info, err := os.Stat(job.Args[0])
		if err != nil || !info.Mode().IsRegular() || filepath.Dir(job.Args[0]) != filepath.Join(s.Home, "bin") {
			t.Fatalf("host executable was not pinned: %v", err)
		}
		listener, err := net.Listen("unix", s.Compute.Socket)
		if err != nil {
			return err
		}
		var ready atomic.Bool
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(compute.Runtime{Ready: ready.Load(), HostID: "fixture", ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: s.Host.Fingerprint()})
		})}
		go server.Serve(listener)
		t.Cleanup(func() { server.Close() })
		go func() {
			lock, err := commandLock(ctx, s.Home)
			if err == nil {
				lock.Close()
				ready.Store(true)
			}
			lockResult <- err
		}()
		return nil
	}
	if stop, err := ensureHost(ctx, s, io.Discard, supervisor); err != nil || stop != nil {
		t.Fatalf("independent host start: %v", err)
	}
	if err := <-lockResult; err != nil {
		t.Fatalf("readiness retained the profile lock: %v", err)
	}
	if _, err := ensureHost(ctx, s, io.Discard, supervisor); err != nil || len(supervisor.starts) != 1 {
		t.Fatalf("ready host was launched twice: %v", err)
	}
	for _, path := range []string{"data", "workspace", "config.json", "run/desktop.json"} {
		if _, err := os.Stat(filepath.Join(s.Home, path)); !os.IsNotExist(err) {
			t.Fatalf("host-only launch initialized %s: %v", path, err)
		}
	}
}

func TestAddingDesktopPreservesHostOnlyOwnership(t *testing.T) {
	for _, daily := range []bool{false, true} {
		for _, active := range []bool{false, true} {
			t.Run(map[bool]string{false: "explicit", true: "daily"}[daily]+map[bool]string{false: "/stopped host", true: "/running host"}[active], func(t *testing.T) {
				s, original := hostOnlyFixture(t)
				s.Desktop = config.Config{Addr: "127.0.0.1:0", DataDir: filepath.Join(s.Home, "data")}
				job := original.Jobs[0]
				manager := &hostSupervisor{loaded: map[string]bool{job.Label: active}}
				manager.onStart = func(job Job) error {
					if job.Role != "desktop" {
						t.Fatalf("desktop launch restarted host %s", job.Role)
					}
					var snapshot desktoplaunch.Snapshot
					if err := jsonfile.Read(job.Marker, &snapshot); err != nil {
						return err
					}
					readyLaunchFixture(t, app.SettingsFromSnapshot(snapshot))
					return nil
				}
				var err error
				if daily {
					err = launch(context.Background(), s.Home, io.Discard, func() (app.Settings, error) { return s, nil }, func() (supervisor, error) { return manager, nil }, func(string) error { return nil })
				} else {
					err = start(context.Background(), s, io.Discard, false, manager)
				}
				if err != nil {
					t.Fatal(err)
				}
				m, err := readManifest(s.Home)
				if err != nil || m.Desktop == nil || m.OwnedHost == nil || m.OwnedHost.Fingerprint() != original.OwnedHost.Fingerprint() || len(m.Jobs) != 2 || m.job("host").Marker != job.Marker || manager.loaded[job.Label] != active || len(manager.starts) != 1 || len(manager.stops) != 0 {
					t.Fatalf("desktop launch lost host ownership: %#v %v", m, err)
				}
			})
		}
	}
}

func TestHostOnlyRestartDoesNotAddDesktop(t *testing.T) {
	s, original := hostOnlyFixture(t)
	before, err := os.ReadFile(manifestPath(s.Home))
	if err != nil {
		t.Fatal(err)
	}
	manager := &hostSupervisor{loaded: map[string]bool{original.Jobs[0].Label: true}}
	err = restart(context.Background(), s, io.Discard, false, manager)
	if err == nil || !strings.Contains(err.Error(), "desktop only") || len(manager.starts) != 0 || len(manager.stops) != 0 {
		t.Fatalf("host-only restart changed services: %v", err)
	}
	after, err := os.ReadFile(manifestPath(s.Home))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("restart rewrote host-only ownership: %v", err)
	}
}

func TestStoppedHostUsesNewExecutableWithoutReplacingOwnedStorage(t *testing.T) {
	s := managedHostSettings(t)
	s.RunnerExecutable = filepath.Join(s.Home, "entry")
	firstBytes := []byte("#!/bin/sh\n# first executable\n")
	if err := os.WriteFile(s.RunnerExecutable, firstBytes, 0700); err != nil {
		t.Fatal(err)
	}
	manager := &hostSupervisor{loaded: map[string]bool{}}
	var closeHost func()
	manager.onStart = func(job Job) error {
		m, err := readManifest(s.Home)
		if err != nil || m.job("host").Marker != job.Marker || m.OwnedHost.Fingerprint() != s.Host.Fingerprint() {
			t.Fatalf("replacement launched before ownership: %#v %v", m, err)
		}
		closeHost = hostListener(t, s)
		return nil
	}
	if _, err := ensureHost(context.Background(), s, io.Discard, manager); err != nil {
		t.Fatal(err)
	}
	first := manager.starts[0]
	if err := os.WriteFile(s.RunnerExecutable, []byte("#!/bin/sh\n# installed replacement\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureHost(context.Background(), s, io.Discard, manager); err != nil || len(manager.starts) != 1 {
		t.Fatalf("installation restarted a live host: %v", err)
	}
	closeHost()
	manager.loaded[first.Label] = false
	if _, err := ensureHost(context.Background(), s, io.Discard, manager); err != nil {
		t.Fatal(err)
	}
	if len(manager.starts) != 2 || manager.starts[1].Args[0] == first.Args[0] || manager.starts[1].Marker == first.Marker || len(manager.stops) != 0 {
		t.Fatalf("stopped host did not get an independent executable: %#v", manager.starts)
	}
	preserved, err := os.ReadFile(first.Args[0])
	if err != nil || !bytes.Equal(preserved, firstBytes) {
		t.Fatalf("replaced the previous fixed executable: %v", err)
	}
}

func TestExternalModeNeverRegistersOrStartsAService(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	s := app.Settings{Inputs: desktoplaunch.Inputs{Home: home}}
	s.Compute.Mode, s.Compute.Socket = "external", "/run/admin.sock"
	manager := &hostSupervisor{loaded: map[string]bool{}}
	if _, err := ensureHost(context.Background(), s, io.Discard, manager); err == nil {
		t.Fatal("external service accepted for management")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) || len(manager.starts) != 0 || len(manager.stops) != 0 {
		t.Fatalf("external management touched the profile: %v", err)
	}
}

func TestHostOnlyStatusLogsAndStopUseRecordedOwnership(t *testing.T) {
	s, m := hostOnlyFixture(t)
	hostListener(t, s)
	if err := os.WriteFile(filepath.Join(s.Home, "config.json"), []byte("broken current configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	job := m.Jobs[0]
	manager := &hostSupervisor{loaded: map[string]bool{job.Label: true}}
	var output bytes.Buffer
	if err := status(context.Background(), s.Home, &output, func() (supervisor, error) { return manager, nil }); err != nil || !strings.Contains(output.String(), "host service registered: true") || !strings.Contains(output.String(), "VM controller: ready (fixture)") || !strings.Contains(output.String(), "lima: available") {
		t.Fatalf("host-only status: %v %s", err, output.String())
	}
	if err := os.WriteFile(job.Log, []byte("host-only diagnostics\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := Logs(context.Background(), s.Home, &output); err != nil || !strings.Contains(output.String(), "host-only diagnostics") {
		t.Fatalf("host-only logs: %v %s", err, output.String())
	}
	if err := stop(context.Background(), s.Home, io.Discard, manager); err != nil || len(manager.stops) != 1 || manager.stops[0].Marker != job.Marker {
		t.Fatalf("host-only stop: %v %#v", err, manager.stops)
	}
}

func TestStopContinuesWhenOwnedHostCannotBeVerified(t *testing.T) {
	s, m := hostOnlyFixture(t)
	other := s
	otherConfig := *s.Host
	otherConfig.DataDir = filepath.Join(s.Home, "unrelated-compute")
	other.Host = &otherConfig
	hostListener(t, other)
	manager := &hostSupervisor{loaded: map[string]bool{m.Jobs[0].Label: true}}
	var output bytes.Buffer
	if err := stop(t.Context(), s.Home, &output, manager); err != nil {
		t.Fatal(err)
	}
	if len(manager.stops) != 1 || manager.stops[0].Marker != m.Jobs[0].Marker {
		t.Fatalf("unavailable inventory prevented owned service stop: %#v", manager.stops)
	}
	if !strings.Contains(output.String(), "VM state could not be queried.") {
		t.Fatalf("unverified inventory was reported as current VM state: %s", output.String())
	}
}

func TestLegacyDaemonOwnershipIsRejectedWithoutReplacement(t *testing.T) {
	s, m := managedHostFixture(t)
	m.Version = 1
	if err := jsonfile.Write(manifestPath(s.Home), m, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestPath(s.Home))
	if err != nil {
		t.Fatal(err)
	}
	manager := &hostSupervisor{loaded: map[string]bool{}}
	if _, err := ensureHost(context.Background(), s, io.Discard, manager); err == nil {
		t.Fatal("accepted obsolete ownership")
	}
	after, err := os.ReadFile(manifestPath(s.Home))
	if err != nil || !bytes.Equal(before, after) || len(manager.starts) != 0 || len(manager.stops) != 0 {
		t.Fatalf("replaced obsolete ownership: %v", err)
	}
}

func TestStoppedHostDoesNotReplaceAnotherServiceOnItsSocket(t *testing.T) {
	s, _ := hostOnlyFixture(t)
	before, err := os.ReadFile(manifestPath(s.Home))
	if err != nil {
		t.Fatal(err)
	}
	other := s
	otherConfig := *s.Host
	otherConfig.DataDir = filepath.Join(s.Home, "unrelated-storage")
	other.Host = &otherConfig
	hostListener(t, other)
	manager := &hostSupervisor{loaded: map[string]bool{}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := ensureHost(ctx, s, io.Discard, manager); err == nil {
		t.Fatal("accepted an unrelated host")
	}
	after, err := os.ReadFile(manifestPath(s.Home))
	if err != nil || !bytes.Equal(before, after) || len(manager.starts) != 0 || len(manager.stops) != 0 {
		t.Fatalf("changed ownership while another service held the socket: %v", err)
	}
}

func TestConcurrentHostOnlyLaunchesShareOneRecordedJob(t *testing.T) {
	s := managedHostSettings(t)
	manager := &hostSupervisor{loaded: map[string]bool{}}
	manager.onStart = func(Job) error {
		hostListener(t, s)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := ensureHost(ctx, s, io.Discard, manager)
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if len(manager.starts) != 1 || len(manager.stops) != 0 {
		t.Fatalf("concurrent CLI commands launched multiple hosts: %#v", manager.starts)
	}
}
