//go:build linux && amd64

package hostinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/artifacts"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/hostconn"
)

type systemFixture struct {
	loaded, active, rejectStart bool
	dropIns                     string
	stages, starts, reloads     int
}

func newInstallation(t *testing.T) (installation, *systemFixture) {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"opt", "etc/systemd/system", "var/lib", "run"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	f := new(systemFixture)
	i := installation{
		installFS: installFS{root: root, uid: uint32(os.Getuid())},
		source:    filepath.Join(root, "source"),
		config: func(_ context.Context, gid int) (compute.Config, error) {
			return compute.Config{
				DataDir: "/var/lib/axv", Socket: "/run/axv-host/control.sock", SocketGroup: &gid,
				Capacity:    compute.Capacity{Resources: compute.Resources{CPUs: 4, MemoryMiB: 4096, DiskGiB: 20}, Machines: 2, Workers: 1},
				Firecracker: &compute.FirecrackerConfig{Binary: binDir + "/firecracker", Jailer: binDir + "/jailer", Version: firecrackerVersion},
				Images:      []compute.Image{},
			}, nil
		},
		stage: func(_ context.Context, dir string) error {
			f.stages++
			for _, name := range []string{"firecracker", "jailer"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0755); err != nil {
					return err
				}
			}
			return nil
		},
		stageGuest: func(context.Context, io.Writer) error { return nil },
		image:      defaultGuestImage(artifacts.DefaultFirecrackerGuest()),
		systemctl: func(_ context.Context, args ...string) ([]byte, error) {
			switch args[1] {
			case "show":
				load, active, fragment := "not-found", "inactive", ""
				if f.loaded {
					load, fragment = "loaded", unitFile
				}
				if f.active {
					active = "active"
				}
				return []byte(fmt.Sprintf("LoadState=%s\nActiveState=%s\nFragmentPath=%s\nDropInPaths=%s\nNeedDaemonReload=no\n", load, active, fragment, f.dropIns)), nil
			case "daemon-reload":
				f.reloads++
				f.loaded = true
			case "start", "try-restart":
				f.starts++
				if f.rejectStart {
					return nil, errors.New("service start failed")
				}
				f.active = true
			default:
				t.Fatalf("unknown systemctl action: %v", args)
			}
			return nil, nil
		},
		ready: func(context.Context, compute.Config) error { return nil },
	}
	if err := os.WriteFile(i.source, []byte("this AXV executable"), 0755); err != nil {
		t.Fatal(err)
	}
	return i, f
}

func TestInstallServiceStagesAndVerifiesBeforeReportingReady(t *testing.T) {
	i, f := newInstallation(t)
	if err := i.run(t.Context(), 1001, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := i.requireAbsent(binDir, configFile, unitFile, "/run/axv-host-install.lock"); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := i.run(t.Context(), 1001, false, &output); err != nil {
		t.Fatal(err)
	}
	if f.stages != 1 || f.reloads != 1 || f.starts != 1 {
		t.Fatalf("unexpected installation %+v %s", f, output.String())
	}
	if !strings.Contains(output.String(), "Choose Connect in Virtual Machines.") || !strings.Contains(output.String(), "Use Check connection if") {
		t.Fatalf("unexpected connection guidance: %s", output.String())
	}
	for path, expected := range map[string]string{binDir + "/axv": "this AXV executable", binDir + "/firecracker": "firecracker", binDir + "/jailer": "jailer", unitFile: string(unit)} {
		b, err := os.ReadFile(i.path(path))
		if err != nil || string(b) != expected {
			t.Fatalf("installed %s: %s %v", path, b, err)
		}
	}
	cfg, err := i.read(configFile, 0600)
	if err != nil || !strings.Contains(string(cfg), `"socketGroup": 1001`) {
		t.Fatalf("installed configuration: %v %s", err, cfg)
	}
	config, err := compute.DecodeConfig(bytes.NewReader(cfg))
	if err != nil || len(config.Images) != 1 || config.Images[0].ID != i.image.ID {
		t.Fatalf("expected a registered default image: %+v, %v", config.Images, err)
	}
	if err = i.run(t.Context(), 1001, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	f.active = false
	if err = i.run(t.Context(), 1001, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if f.starts != 1 || f.reloads != 1 || f.stages != 1 {
		t.Fatalf("reinstallation restarted previously stopped service: %+v", f)
	}
	if err = i.run(t.Context(), 1002, false, io.Discard); err == nil {
		t.Fatal("adopted another account's transaction")
	}
}

func TestInstallServiceRetriesOriginalFilesAndPreservesChangedConfig(t *testing.T) {
	i, f := newInstallation(t)
	var errReady = errors.New("runtime not ready")
	i.ready = func(context.Context, compute.Config) error { return errReady }
	if err := i.run(t.Context(), 0, false, io.Discard); !errors.Is(err, errReady) {
		t.Fatal(err)
	}
	// Rechecking the active service must not issue another start or publish new binaries.
	i.ready = func(context.Context, compute.Config) error { return nil }
	if err := i.run(t.Context(), 0, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if f.stages != 1 || f.starts != 1 || f.reloads != 1 {
		t.Fatalf("unexpected retries %+v", f)
	}
	if err := os.WriteFile(i.path(configFile), []byte("admin changes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.run(t.Context(), 0, false, io.Discard); err == nil {
		t.Fatal("overwrote edited host config")
	}
	if got, _ := os.ReadFile(i.path(configFile)); string(got) != "admin changes" {
		t.Fatalf("edited config changed: %s", got)
	}
}

func TestInstallServiceInterruptedBeforeStartCanFinish(t *testing.T) {
	i, f := newInstallation(t)
	fail := true
	systemctl := i.systemctl
	i.systemctl = func(ctx context.Context, args ...string) ([]byte, error) {
		if args[1] == "daemon-reload" && fail {
			return nil, errors.New("simulated failure")
		}
		return systemctl(ctx, args...)
	}
	if err := i.run(t.Context(), 1001, false, io.Discard); err == nil {
		t.Fatal("expected failed daemon-reload")
	}
	fail = false
	f.rejectStart = true
	if err := i.run(t.Context(), 1001, false, io.Discard); err == nil {
		t.Fatal("expected failed start")
	}
	f.rejectStart = false
	if err := i.run(t.Context(), 1001, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if f.stages != 1 || f.starts != 2 {
		t.Fatalf("unexpected retries %+v", f)
	}
}

func TestInstallServiceNeverAdoptsConflictsOrUntrustedPaths(t *testing.T) {
	for _, path := range []string{configFile, unitFile, binDir, "/var/lib/axv", "/run/axv-host"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			i, f := newInstallation(t)
			if err := os.MkdirAll(filepath.Dir(i.path(path)), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(i.path(path), []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, check := range []bool{true, false} {
				if err := i.run(t.Context(), 0, check, io.Discard); err == nil {
					t.Fatal("existing state accepted")
				}
			}
			if f.stages != 0 || f.starts != 0 || f.reloads != 0 {
				t.Fatal("side effects on conflict")
			}
			if got, _ := os.ReadFile(i.path(path)); string(got) != "existing" {
				t.Fatal("changed existing state")
			}
		})
	}
	for _, kind := range []string{"writable", "symlink", "unit", "drop-ins"} {
		t.Run(kind, func(t *testing.T) {
			i, f := newInstallation(t)
			switch kind {
			case "writable":
				if err := os.Chmod(i.path("/opt"), 0777); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), i.path("/opt/axv")); err != nil {
					t.Fatal(err)
				}
			case "unit":
				f.loaded = true
			case "drop-ins":
				f.dropIns = "/etc/systemd/system/axv-host.service.d/custom.conf"
			}
			if err := i.run(t.Context(), 0, false, io.Discard); err == nil {
				t.Fatal("conflicting host accepted")
			}
			if f.stages != 0 || f.starts != 0 || f.reloads != 0 {
				t.Fatal("side effects on conflict")
			}
		})
	}
}

func TestInstallServiceDownloadFailureAndConcurrentInstaller(t *testing.T) {
	i, f := newInstallation(t)
	i.stage = func(context.Context, string) error { return errors.New("download checksum mismatch") }
	if err := i.run(t.Context(), 0, false, io.Discard); err == nil {
		t.Fatal("accepted incomplete staging")
	}
	if err := i.requireAbsent(binDir, unitFile, configFile); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(i.path("/opt/axv"))
	if err != nil || len(entries) != 0 {
		t.Fatal("staging directory not cleaned", err)
	}
	lock, err := i.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = i.run(t.Context(), 0, false, io.Discard); err == nil {
		t.Fatal("concurrent installer accepted")
	}
	if f.starts != 0 || f.reloads != 0 {
		t.Fatal("inconsistent service mutations")
	}
}

func TestSocketGroupSelection(t *testing.T) {
	for _, tc := range []struct {
		name, group, passwd, members string
		uid                          int
		sudoUID                      string
		want                         int
		reject                       bool
	}{
		{name: "private group with a different name", uid: 1001, want: 2001},
		{name: "sudo account", uid: 0, sudoUID: "1001", want: 2001},
		{name: "root-only host", uid: 0, want: 0},
		{name: "shared primary group", uid: 1001, passwd: "bob:x:1002:2001::/:/bin/sh", reject: true},
		{name: "other supplementary member", uid: 1001, members: "bob", reject: true},
		{name: "shared group explicitly authorized", group: "developers", uid: 1001, members: "amy,bob", want: 2001},
		{name: "explicit numeric group", group: "2001", uid: 0, want: 2001},
		{name: "unknown override", group: "unknown", uid: 0, reject: true},
		{name: "invalid sudo identity", uid: 0, sudoUID: "unknown", reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := func(_ context.Context, tool string, args ...string) ([]byte, error) {
				if tool != "getent" {
					t.Fatal(tool)
				}
				switch strings.Join(args, " ") {
				case "passwd":
					return []byte("amy:x:1001:2001::/:/bin/sh\n" + tc.passwd), nil
				case "-- group developers", "-- group 2001":
					return []byte("developers:x:2001:" + tc.members), nil
				default:
					return nil, fmt.Errorf("no entry %v", args)
				}
			}
			got, err := socketGroup(t.Context(), tc.group, tc.uid, tc.sudoUID, query)
			if tc.reject != (err != nil) || (!tc.reject && got != tc.want) {
				t.Fatalf("group %d err %v", got, err)
			}
		})
	}
}

func TestReadyRejectsAnotherConfigurationOnTheSameSocket(t *testing.T) {
	// Linux limits sockaddr_un paths; keep only the socket in a short private directory.
	dir, err := os.MkdirTemp("/tmp", "axvi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var inventoryReads atomic.Int32
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runtime" {
			inventoryReads.Add(1)
		}
		json.NewEncoder(w).Encode(compute.Runtime{HostID: "another-host", Ready: true, ConfigFingerprint: "changed", ProtocolVersion: compute.ProtocolVersion})
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = waitReady(ctx, compute.Config{Socket: socket})
	if !errors.Is(err, hostconn.ErrChanged) || inventoryReads.Load() != 0 {
		t.Fatalf("changed configuration accepted: %v reads %d", err, inventoryReads.Load())
	}
}

func TestEmbeddedUnitMatchesDownloadableSample(t *testing.T) {
	download, err := os.ReadFile("../../../../../config/examples/axv-host.service")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(download, unit) {
		t.Fatal("embedded systemd unit and downloadable example differ")
	}
}

func TestInstallServicePreservesHostPreparedDuringDownload(t *testing.T) {
	i, f := newInstallation(t)
	stage := i.stage
	i.stage = func(ctx context.Context, dir string) error {
		if err := stage(ctx, dir); err != nil {
			return err
		}
		return os.WriteFile(i.path(unitFile), []byte("administrator unit"), 0644)
	}
	if err := i.run(t.Context(), 0, false, io.Discard); err == nil {
		t.Fatal("adopted service prepared by administrator during download")
	}
	if err := i.requireAbsent(binDir, configFile); err != nil {
		t.Fatal(err)
	}
	if unit, err := os.ReadFile(i.path(unitFile)); err != nil || string(unit) != "administrator unit" {
		t.Fatal("administrator unit changed", err)
	}
	if f.starts != 0 || f.reloads != 0 {
		t.Fatal("altered system services")
	}
}
