//go:build linux

package hostinstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestLifecycleStatusStartStopRestartUpgradeUninstall(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"opt/axv/bin", "etc/axv", "etc/systemd/system", "var/lib/axv", "run"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	installedBin := filepath.Join(root, BinDir, "axv")
	if err := os.WriteFile(installedBin, []byte("#!/bin/sh\necho axv 0.1.0-old\n"), 0755); err != nil {
		t.Fatal(err)
	}
	sourceBin := filepath.Join(root, "new-axv")
	if err := os.WriteFile(sourceBin, []byte("#!/bin/sh\necho axv 0.1.0-new\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, UnitFile), []byte("[Unit]\nDescription=test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ConfigFile), []byte(`{"dataDir":"/var/lib/axv","socket":"/run/axv-host/control.sock"}`), 0600); err != nil {
		t.Fatal(err)
	}

	active := true
	enabled := true
	var actions []string
	env := lifecycleEnv{
		fs:     installFS{root: root, uid: uint32(os.Getuid())},
		source: sourceBin,
		systemctl: func(_ context.Context, args ...string) ([]byte, error) {
			action := args[1]
			actions = append(actions, action)
			switch action {
			case "show":
				act := "inactive"
				if active {
					act = "active"
				}
				en := "disabled"
				if enabled {
					en = "enabled"
				}
				return []byte("LoadState=loaded\nActiveState=" + act + "\nUnitFileState=" + en + "\nFragmentPath=" + UnitFile + "\n"), nil
			case "start", "restart":
				active = true
			case "stop":
				active = false
			case "disable":
				enabled = false
			case "daemon-reload":
			}
			return nil, nil
		},
		probe: func(context.Context, string) error { return nil },
	}

	st := env.status(t.Context(), "0.1.0-new")
	if !st.Installed || !st.Active || !st.Enabled || st.InstalledVersion != "0.1.0-old" || !st.VersionDrift {
		t.Fatalf("unexpected status: %+v", st)
	}

	var out bytes.Buffer
	if err := env.upgradeService(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0.1.0-old -> 0.1.0-new") {
		t.Fatalf("upgrade output missing version change: %s", out.String())
	}
	st = env.status(t.Context(), "0.1.0-new")
	if st.VersionDrift || st.InstalledVersion != "0.1.0-new" {
		t.Fatalf("version drift remained after upgrade: %+v", st)
	}

	out.Reset()
	if err := env.stopService(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("service remained active after stop")
	}

	out.Reset()
	if err := env.startService(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("service did not become active after start")
	}

	out.Reset()
	if err := env.restartService(t.Context(), &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := env.uninstallService(t.Context(), UninstallOptions{PurgeConfig: false, PurgeData: false}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, UnitFile)); !os.IsNotExist(err) {
		t.Fatalf("unit file remained after uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, BinDir)); !os.IsNotExist(err) {
		t.Fatalf("bin dir remained after uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ConfigFile)); err != nil {
		t.Fatalf("config should be retained without --purge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "/var/lib/axv")); err != nil {
		t.Fatalf("VM storage should be retained without --purge-data: %v", err)
	}
}

func TestConnectInvokingProfileSetsExternalSocketOnlyWhenDisabled(t *testing.T) {
	home := filepath.Join(t.TempDir(), "profile")
	gotHome, changed, err := connectInvokingProfile(StandardSocket, home)
	if err != nil || !changed || gotHome != home {
		t.Fatalf("connectInvokingProfile = %q, %t, %v", gotHome, changed, err)
	}
	p, err := profile.Read(home)
	if err != nil {
		t.Fatal(err)
	}
	if p.Compute.Mode != "external" || p.Compute.Socket != StandardSocket {
		t.Fatalf("unexpected compute config: %+v", p.Compute)
	}
	// Repeating should be a no-op.
	_, changed, err = connectInvokingProfile(StandardSocket, home)
	if err != nil || changed {
		t.Fatalf("repeated connect changed profile: %t, %v", changed, err)
	}
	// Existing custom socket must not be overwritten.
	if err := profile.SaveCompute(home, profile.Compute{Mode: "external", Socket: "/run/custom.sock"}); err != nil {
		t.Fatal(err)
	}
	_, changed, err = connectInvokingProfile(StandardSocket, home)
	if err != nil || changed {
		t.Fatalf("overwrote custom socket: %t, %v", changed, err)
	}
	p, err = profile.Read(home)
	if err != nil || p.Compute.Socket != "/run/custom.sock" {
		t.Fatalf("custom socket lost: %+v, %v", p.Compute, err)
	}
}
