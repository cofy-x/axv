package prepare

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

func TestLinuxServiceDiagnosticChecksOnlyLocalCandidates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "host.json")
	socket := filepath.Join(dir, "control.sock")
	var diagnostic linuxServiceDiagnostic
	if check := diagnostic.check(ctx, socket, configPath); check.Status != localServiceMissing || check.Socket != socket {
		t.Fatalf("expected missing socket, got %+v", check)
	}
	if err := os.WriteFile(socket, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if check := diagnostic.check(ctx, socket, configPath); check.Status != localServiceUnavailable {
		t.Fatalf("regular file accepted as socket: %+v", check)
	}
	if err := os.Remove(socket); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	configuredSocket := testHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/info" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		hostResponse(w)
	})
	config := compute.Config{
		DataDir:     dir,
		Socket:      configuredSocket,
		Capacity:    compute.Capacity{Resources: compute.Resources{CPUs: 1, MemoryMiB: 256, DiskGiB: 1}, Machines: 1, Workers: 1},
		Firecracker: &compute.FirecrackerConfig{},
	}
	if err := jsonfile.Write(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if check := diagnostic.check(ctx, socket, configPath); check.Status != localServiceReady || check.Socket != configuredSocket {
			t.Fatalf("configured socket not found: %+v", check)
		}
	}
	if count := requests.Load(); count != 1 {
		t.Fatalf("expected one probe across repeated checks, got %d", count)
	}
	if err := os.Remove(configuredSocket); err != nil {
		t.Fatal(err)
	}
	if check := diagnostic.check(ctx, socket, configPath); check.Status != localServiceMissing || check.Socket != configuredSocket {
		t.Fatalf("removed socket still appears ready: %+v", check)
	}
}

func TestLinuxServiceDiagnosticReportsUnavailableAndPermissionDenied(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "missing-config.json")
	unavailable := testHost(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hostId":"incompatible","protocolVersion":0}`))
	})
	var diagnostic linuxServiceDiagnostic
	if check := diagnostic.check(context.Background(), unavailable, configPath); check.Status != localServiceUnavailable {
		t.Fatalf("incompatible service accepted: %+v", check)
	}
	wrapped := fmt.Errorf("dial test socket: %w", &fs.PathError{Op: "connect", Path: unavailable, Err: fs.ErrPermission})
	if check := localServiceFailure(unavailable, wrapped); check.Status != localServicePermission {
		t.Fatalf("permission failure was not distinguished: %+v", check)
	}
}

func TestLinuxServiceDiagnosticHonorsCanceledCheck(t *testing.T) {
	socket := testHost(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var diagnostic linuxServiceDiagnostic
	if check := diagnostic.check(ctx, socket, filepath.Join(t.TempDir(), "host.json")); check.Status != localServiceUnavailable || ctx.Err() == nil {
		t.Fatalf("timed-out socket reported incorrectly: %+v, %v", check, ctx.Err())
	}
}

func TestLinuxServiceDiagnosticSystemdHints(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemctl fixture uses a POSIX shell")
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	t.Setenv("PATH", bin)
	t.Setenv("AXV_TEST_UNIT_CALLS", calls)
	script := `#!/bin/sh
[ "$*" = "--system show --property=LoadState,ActiveState --no-pager axv-host.service" ] || exit 1
echo x >> "$AXV_TEST_UNIT_CALLS"
printf '%s\n' "$AXV_TEST_UNIT_SHOW"
`
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	var diagnostic linuxServiceDiagnostic
	missing := LocalServiceCheck{Socket: defaultFirecrackerSocket, Status: localServiceMissing}
	for _, result := range []LocalServiceCheck{
		{Socket: defaultFirecrackerSocket, Status: localServiceReady},
		{Socket: defaultFirecrackerSocket, Status: localServicePermission},
		{Socket: defaultFirecrackerSocket, Status: localServiceUnavailable},
		{Socket: "/custom.sock", Status: localServiceMissing},
	} {
		if got := diagnostic.withSystemdState(ctx, result); got != result {
			t.Errorf("unrelated diagnostic changed: %+v", got)
		}
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatalf("unexpected systemctl call for unrelated socket status: %v", err)
	}
	for _, test := range []struct{ load, active, want string }{
		{"not-found", "inactive", "not_installed"},
		{"loaded", "inactive", "inactive"},
		{"loaded", "active", "active"},
		{"loaded", "failed", "failed"},
		{"loaded", "activating", ""},
		{"masked", "inactive", ""},
		{"not-found", "failed", ""},
		{"", "inactive", ""},
	} {
		t.Setenv("AXV_TEST_UNIT_SHOW", "ActiveState="+test.active+"\nLoadState="+test.load)
		diagnostic.systemdChecked = time.Now().Add(-localServiceProbeTTL)
		got := diagnostic.withSystemdState(ctx, missing)
		if got.SystemdState != test.want || got.Status != localServiceMissing {
			t.Errorf("%s/%s: got %+v, want %q", test.load, test.active, got, test.want)
		}
	}
	before, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic.withSystemdState(ctx, missing)
	after, err := os.ReadFile(calls)
	if err != nil || string(after) != string(before) {
		t.Fatalf("repeated check ran systemctl during the cache lifetime: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	diagnostic.systemdChecked = time.Time{}
	if got := diagnostic.withSystemdState(ctx, missing); got != missing {
		t.Fatalf("systemctl missing: got %+v", got)
	}
}

func TestDefaultSocketDiagnosticWithLocalConfig(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "host.json")
	config := compute.Config{
		DataDir: dir, Socket: defaultFirecrackerSocket, Firecracker: &compute.FirecrackerConfig{},
		Capacity: compute.Capacity{Resources: compute.Resources{CPUs: 1, MemoryMiB: 256, DiskGiB: 1}, Machines: 1, Workers: 1},
	}
	if err := jsonfile.Write(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := linuxServiceDiagnostic{systemdChecked: time.Now(), systemdState: "inactive"}
	missing := LocalServiceCheck{Status: localServiceMissing, Socket: defaultFirecrackerSocket}
	if got := diagnostic.checkConfig(ctx, missing, configPath); got.Status != localServiceMissing || got.SystemdState != "inactive" {
		t.Fatalf("readable config points at missing default socket: %+v", got)
	}
	got := diagnostic.configFailure(ctx, missing, configPath, info, fs.ErrPermission)
	if got.Status != localServicePermission || !strings.Contains(got.Detail, configPath) || got.SystemdState != "inactive" {
		t.Fatalf("unreadable regular config lost its diagnostic or the default unit state: %+v", got)
	}
	if got := diagnostic.configFailure(ctx, missing, configPath, nil, fs.ErrPermission); got.SystemdState != "" {
		t.Fatalf("unknown config path reported a unit state: %+v", got)
	}
	if got := diagnostic.configFailure(ctx, missing, configPath, info, fs.ErrNotExist); got.Status != localServiceUnavailable || got.SystemdState != "" {
		t.Fatalf("config removed during read was reported as a missing service: %+v", got)
	}
}
