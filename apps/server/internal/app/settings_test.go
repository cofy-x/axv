package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/config"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestDesktopFingerprintPreservesExistingIdentity(t *testing.T) {
	s := Settings{
		Inputs: launch.Inputs{
			LaunchID: "launch-original", ComputeOverride: "/run/override.sock", Home: "/profile",
			Environment: map[string]string{"AXV_CACHE_HOME": "/cache", "HTTPS_PROXY": "http://proxy.invalid:7890"},
			Desktop:     config.Config{Addr: "127.0.0.1:3001", DataDir: "/profile/data", Home: "/profile", ConfigPath: "/profile/config.json", WorkspaceDir: "/projects", TmuxSocket: "private"},
		},
		ComputeSettings: ComputeSettings{Compute: profile.Compute{Mode: "managed", HostConfig: "/profile/host.json"}, Host: &compute.Config{Socket: "/run/host.sock"}, ComputeError: "runtime problem"},
		ProcessState:    ProcessState{RunnerExecutable: "/profile/bin/axv-pinned"},
	}
	// Captured from the pre-refactor format-4 Settings identity projection.
	const original = "c1baf83b5cdfc140de1e979d146e33a1b5f4a030559981ed8ac4cb31393bdc53"
	if s.Fingerprint() != original || s.Snapshot().Fingerprint() != original {
		t.Fatal("existing desktop identity changed")
	}
	next := s
	next.LaunchID, next.RunnerExecutable = "another-launch", "/another/axv"
	next.Compute, next.Host, next.ComputeError = profile.Compute{Mode: "external", Socket: "/run/replacement.sock"}, nil, ""
	if next.Fingerprint() != original {
		t.Fatal("runtime or live compute settings changed launch identity")
	}
	next.ComputeOverride = ""
	if next.Fingerprint() == original {
		t.Fatal("immutable socket override was omitted from launch identity")
	}
	s.Environment = nil
	next = s
	next.Environment = map[string]string{}
	if s.Fingerprint() != next.Fingerprint() {
		t.Fatal("empty environment changed its existing omitempty identity")
	}
}

func TestDesktopLaunchSnapshotRetainsFormat4WireContract(t *testing.T) {
	const original = `{"launchId":"launch-original","computeOverride":"/run/override.sock","home":"/profile","environment":{"AXV_CACHE_HOME":"/cache"},"desktop":{"Addr":"127.0.0.1:3001","DataDir":"/profile/data","Home":"/profile","ConfigPath":"/profile/config.json","WorkspaceDir":"/projects","ProxyURL":"","TmuxSocket":"private"},"compute":{"mode":"external","socket":"/run/override.sock"}}`
	var snapshot launch.Snapshot
	if err := json.Unmarshal([]byte(original), &snapshot); err != nil {
		t.Fatal(err)
	}
	s := SettingsFromSnapshot(snapshot)
	if s.Home != "/profile" || s.LaunchID != "launch-original" || s.Compute.Socket != "/run/override.sock" || s.Desktop.WorkspaceDir != "/projects" || s.Environment["AXV_CACHE_HOME"] != "/cache" {
		t.Fatal("captured launch fields were lost")
	}
	s.RunnerExecutable, s.ComputeError = "/new/runtime/axv", "runtime-only"
	encoded, err := json.Marshal(s.Snapshot())
	if err != nil || !bytes.Equal(encoded, []byte(original)) {
		t.Fatalf("format-4 snapshot changed: %s, %v", encoded, err)
	}
	if s.Fingerprint() != snapshot.Fingerprint() {
		t.Fatal("restoring a snapshot changed desktop identity")
	}
}

func TestSnapshotAndRestorationOwnCapturedReferenceValues(t *testing.T) {
	group := 42
	s := Settings{
		Inputs:          launch.Inputs{Environment: map[string]string{"PATH": "/captured"}},
		ComputeSettings: ComputeSettings{Host: &compute.Config{SocketGroup: &group, Lima: &compute.LimaConfig{Binary: "/captured/lima"}, Firecracker: &compute.FirecrackerConfig{Binary: "/captured/firecracker"}, Images: []compute.Image{{ID: "original", Guest: &guestimage.Contract{Capabilities: []string{"exec"}}}}}},
	}
	snapshot := s.Snapshot()
	restored := SettingsFromSnapshot(snapshot)
	s.Environment["PATH"], restored.Environment["PATH"] = "/changed", "/restored"
	*s.Host.SocketGroup, *restored.Host.SocketGroup = 43, 44
	s.Host.Lima.Binary, restored.Host.Lima.Binary = "/changed", "/restored"
	s.Host.Firecracker.Binary, restored.Host.Firecracker.Binary = "/changed", "/restored"
	s.Host.Images[0].ID, restored.Host.Images[0].ID = "changed", "restored"
	s.Host.Images[0].Guest.Capabilities[0], restored.Host.Images[0].Guest.Capabilities[0] = "changed", "restored"
	if snapshot.Environment["PATH"] != "/captured" || *snapshot.Host.SocketGroup != 42 || snapshot.Host.Lima.Binary != "/captured/lima" || snapshot.Host.Firecracker.Binary != "/captured/firecracker" || snapshot.Host.Images[0].ID != "original" || snapshot.Host.Images[0].Guest.Capabilities[0] != "exec" {
		t.Fatal("runtime mutation changed captured launch inputs")
	}
}

func TestPreCanceledDesktopDoesNotInitializeTheProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := Settings{Inputs: launch.Inputs{Home: home}}
	if err := ServeContext(ctx, s, io.Discard, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled desktop returned %v", err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("canceled desktop initialized profile state")
	}
}
