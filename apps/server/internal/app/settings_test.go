package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/config"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestResolveKeepsSelectedProfileAndLaunchingEnvironment(t *testing.T) {
	home := filepath.Join(t.TempDir(), "uninitialized-profile")
	userHome := t.TempDir()
	t.Setenv("AXV_COMPUTE_SOCKET", "")
	t.Setenv("HOME", userHome)
	t.Setenv("USER", "launching-user")
	t.Setenv("LOGNAME", "launching-user")
	t.Setenv("CODEX_HOME", filepath.Join(userHome, "codex"))
	desktop := config.Config{
		Addr: "127.0.0.1:0", Home: home,
		DataDir: filepath.Join(home, "data"), ConfigPath: filepath.Join(home, "config.json"),
		WorkspaceDir: filepath.Join(userHome, "projects"),
	}
	uid, gid := os.Geteuid(), os.Getegid()
	s, err := Resolve(profile.Profile{Home: home}, desktop)
	if err != nil {
		t.Fatal(err)
	}
	if s.Home != home || s.Desktop != desktop {
		t.Fatal("resolution changed the selected profile or desktop settings")
	}
	for _, key := range []string{"HOME", "USER", "LOGNAME", "CODEX_HOME"} {
		if s.Environment[key] != os.Getenv(key) {
			t.Fatalf("resolution replaced the launching %s", key)
		}
	}
	if os.Geteuid() != uid || os.Getegid() != gid {
		t.Fatal("resolution changed the launching process identity")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("resolution initialized the selected profile: %v", err)
	}
}

func TestResolveDesktopRequiresIndependentFirecrackerHost(t *testing.T) {
	t.Setenv("AXV_COMPUTE_SOCKET", "")
	s, c := runtimeFixture(t)
	c.Lima = nil
	c.Firecracker = &compute.FirecrackerConfig{Binary: "/test/firecracker"}
	path := filepath.Join(s.Home, "host.json")
	if err := jsonfile.Write(path, c, 0600); err != nil {
		t.Fatal(err)
	}
	p := profile.Profile{Home: s.Home, Compute: profile.Compute{Mode: "managed", HostConfig: path}}
	resolved, err := Resolve(p, s.Desktop)
	if err != nil {
		t.Fatalf("unavailable compute blocked the desktop: %v", err)
	}
	if resolved.Host != nil || !strings.Contains(resolved.ComputeError, "independently administered Linux service") {
		t.Fatalf("desktop accepted managed Firecracker: %#v", resolved.ComputeSettings)
	}
	p.Compute = profile.Compute{Mode: "external", Socket: c.Socket}
	resolved, err = Resolve(p, s.Desktop)
	if err != nil || resolved.Host != nil || resolved.ComputeError != "" || resolved.Compute != p.Compute {
		t.Fatalf("independent host resolution: %#v %v", resolved.ComputeSettings, err)
	}
}

func TestDesktopFingerprintIncludesCapturedInputs(t *testing.T) {
	s := Settings{
		Inputs: launch.Inputs{
			LaunchID: "launch-original", ComputeOverride: "/run/override.sock", Home: "/profile",
			Environment: map[string]string{"AXV_CACHE_HOME": "/cache", "HTTPS_PROXY": "http://proxy.invalid:7890"},
			Desktop:     config.Config{Addr: "127.0.0.1:3001", DataDir: "/profile/data", Home: "/profile", ConfigPath: "/profile/config.json", WorkspaceDir: "/projects", TmuxSocket: "private"},
		},
		ComputeSettings: ComputeSettings{Compute: profile.Compute{Mode: "managed", HostConfig: "/profile/host.json"}, Host: &compute.Config{Socket: "/run/host.sock"}, ComputeError: "runtime problem"},
		ProcessState:    ProcessState{RunnerExecutable: "/profile/bin/axv-pinned"},
	}
	// The current identity projection includes the default-disabled remote settings.
	const baseline = "2e08ce5423df3ce0c1c550779275be5e573f2a18374d314d19de8a82ae56d49f"
	if s.Fingerprint() != baseline || s.Snapshot().Fingerprint() != baseline {
		t.Fatal("captured desktop identity changed")
	}
	next := s
	next.LaunchID, next.RunnerExecutable = "another-launch", "/another/axv"
	next.Compute, next.Host, next.ComputeError = profile.Compute{Mode: "external", Socket: "/run/replacement.sock"}, nil, ""
	if next.Fingerprint() != baseline {
		t.Fatal("runtime or live compute settings changed launch identity")
	}
	next.ComputeOverride = ""
	if next.Fingerprint() == baseline {
		t.Fatal("immutable socket override was omitted from launch identity")
	}
	for name, remote := range map[string]profile.Remote{
		"enabled":  {Enabled: true},
		"hostname": {Hostname: "axv-office"},
		"owner":    {OwnerUserID: "123456"},
	} {
		next = s
		next.Desktop.Remote = remote
		if next.Fingerprint() == baseline {
			t.Fatalf("remote %s was omitted from launch identity", name)
		}
	}
	s.Environment = nil
	next = s
	next.Environment = map[string]string{}
	if s.Fingerprint() != next.Fingerprint() {
		t.Fatal("empty environment changed its existing omitempty identity")
	}
}

func TestDesktopLaunchSnapshotCapturesFormat4Settings(t *testing.T) {
	const captured = `{"launchId":"launch-original","computeOverride":"/run/override.sock","home":"/profile","environment":{"AXV_CACHE_HOME":"/cache"},"desktop":{"Addr":"127.0.0.1:3001","DataDir":"/profile/data","Home":"/profile","ConfigPath":"/profile/config.json","WorkspaceDir":"/projects","ProxyURL":"","TmuxSocket":"private","Remote":{"enabled":true,"hostname":"axv-office","owner_user_id":"123456"}},"compute":{"mode":"external","socket":"/run/override.sock"}}`
	var snapshot launch.Snapshot
	if err := json.Unmarshal([]byte(captured), &snapshot); err != nil {
		t.Fatal(err)
	}
	s := SettingsFromSnapshot(snapshot)
	if s.Home != "/profile" || s.LaunchID != "launch-original" || s.Compute.Socket != "/run/override.sock" || s.Desktop.WorkspaceDir != "/projects" || s.Environment["AXV_CACHE_HOME"] != "/cache" {
		t.Fatal("captured launch fields were lost")
	}
	if s.Desktop.Remote != (profile.Remote{Enabled: true, Hostname: "axv-office", OwnerUserID: "123456"}) {
		t.Fatal("captured remote settings were lost")
	}
	s.RunnerExecutable, s.ComputeError = "/new/runtime/axv", "runtime-only"
	encoded, err := json.Marshal(s.Snapshot())
	if err != nil || !bytes.Equal(encoded, []byte(captured)) {
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
