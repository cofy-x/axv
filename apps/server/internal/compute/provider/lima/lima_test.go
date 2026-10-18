package lima

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func TestHealthyStartRepairsAccountWithoutRestartingSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake limactl uses a POSIX shell")
	}
	for _, tc := range []struct{ unavailable, failRepair bool }{{}, {failRepair: true}, {unavailable: true}} {
		t.Run(fmt.Sprintf("unavailable %t repair failure %t", tc.unavailable, tc.failRepair), func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "limactl")
			body, err := json.Marshal(guestproto.Health{Version: guestproto.Version, MachineID: "vm-test", Ready: true, SSHAvailable: !tc.unavailable})
			if err != nil {
				t.Fatal(err)
			}
			exitCode := 0
			if tc.failRepair {
				exitCode = 1
			}
			// Unexpected calls (including unit restarts and binary copies) fail.
			script := fmt.Sprintf(`#!/bin/sh
case "$*" in
'--tty=false start vm-test') ;;
'--tty=false shell --workdir=/ vm-test -- sudo -n sh -s -- arm64 '* ) cat > transport-script;;
*'axv-project /projects false vol-test') ;;
'--tty=false shell --workdir=/ vm-test -- socat STDIO UNIX-CONNECT:/run/axv/guest.sock')
  while IFS= read -r line; do [ "$line" = "$(printf '\r')" ] && break; done
  printf 'HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%%s' '%s'
  ;;
'--tty=false shell --workdir=/ vm-test -- sudo -n /usr/local/lib/axv/prepare-ssh vm-test --repair-account')
  printf repaired > repair-script
  exit %d
  ;;
'--tty=false shell --workdir=/ vm-test -- sudo -n /usr/local/lib/axv/prepare-ssh vm-test')
  printf initialized > repair-script;;
'--tty=false shell --workdir=/ vm-test -- sudo -n systemctl start axv-ssh.service')
  printf started > ssh-started;;
*) echo "unexpected call: $*" >&2; exit 2;;
esac
`, len(body), body, exitCode)
			if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			d := &Driver{config: compute.LimaConfig{Binary: binary}, root: root, home: root, owner: "host-test"}
			m := compute.Machine{ID: "vm-test"}
			if err = owneddir.Ensure(d.directory(m), d.owner+":"+m.ID); err != nil {
				t.Fatal(err)
			}
			st := state{MachineID: m.ID, Guest: guestproto.Config{MachineID: m.ID, Token: "token-test", ProjectRoot: "/projects"}}
			data, err := json.Marshal(st)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(d.directory(m), "state.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Join(root, "volumes", "vol-test"), 0700); err != nil {
				t.Fatal(err)
			}
			err = d.PrepareGuest(context.Background(), m, compute.Image{Arch: "arm64", Guest: &guestimage.Contract{ImageVersion: 1, ProtocolVersion: guestproto.Version, Capabilities: guestimage.Capabilities()}}, compute.Volume{ID: "vol-test"})
			if (err != nil) != tc.failRepair {
				t.Fatalf("Start error = %v, want failure %t", err, tc.failRepair)
			}
			if tc.failRepair && !strings.Contains(err.Error(), "prepare guest SSH account") {
				t.Fatalf("account repair failure was not exposed: %v", err)
			}
			want := "repaired"
			if tc.unavailable {
				want = "initialized"
			}
			if data, err = os.ReadFile(filepath.Join(root, "repair-script")); err != nil || string(data) != want {
				t.Fatalf("healthy Start did not invoke account repair: %v", err)
			}
			if _, err = os.Stat(filepath.Join(root, "ssh-started")); (err == nil) != tc.unavailable {
				t.Fatalf("SSH recovery did not match availability: %v", err)
			}
			if data, err = os.ReadFile(filepath.Join(root, "transport-script")); err != nil || string(data) != verifyImageScript {
				t.Fatalf("healthy Start did not verify its guest transport: %v", err)
			}
		})
	}
}

func TestStartSurfacesImageContractFailureBeforeHealth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake limactl uses a POSIX shell")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "limactl")
	script := `#!/bin/sh
case "$*" in
'--tty=false start vm-test') ;;
'--tty=false shell --workdir=/ vm-test -- sudo -n sh -s -- arm64 '* )
  cat > transport-script
  echo 'fixture image is incompatible' >&2
  exit 7
  ;;
*) echo "unexpected call after dependency failure: $*" >&2; exit 2;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d := &Driver{config: compute.LimaConfig{Binary: binary}, root: root, home: root, owner: "host-test"}
	m := compute.Machine{ID: "vm-test"}
	if err := owneddir.Ensure(d.directory(m), d.owner+":"+m.ID); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state{MachineID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(d.directory(m), "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	err = d.PrepareGuest(context.Background(), m, compute.Image{Arch: "arm64", Guest: &guestimage.Contract{ImageVersion: 1, ProtocolVersion: guestproto.Version, Capabilities: guestimage.Capabilities()}}, compute.Volume{})
	if err == nil || !strings.Contains(err.Error(), "verify guest image") || !strings.Contains(err.Error(), "fixture image is incompatible") {
		t.Fatalf("Start did not surface dependency installation failure: %v", err)
	}
	if data, err = os.ReadFile(filepath.Join(root, "transport-script")); err != nil || string(data) != verifyImageScript {
		t.Fatalf("Start did not execute the required transport provisioner: %v", err)
	}
}

func TestImageVerifierRejectsMissingDependencyWithoutInstalling(t *testing.T) {
	cmd := exec.Command("sh", "-s", "--", "1", "3", "arm64", strings.Repeat("a", 64))
	// Stop at the missing relay before any host paths are touched.
	cmd.Stdin = strings.NewReader("command() { [ \"$*\" != '-v socat' ]; }\n" + verifyImageScript)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "missing socat") {
		t.Fatalf("missing image dependency was not rejected: %v: %s", err, output)
	}
	if strings.Contains(verifyImageScript, "apt-get") || strings.Contains(verifyImageScript, "install -") {
		t.Fatal("image validation installs packages or changes guest files")
	}
}

func TestImageVerifierBindsSelectedBundleExecutable(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required by image verification")
	}
	_, body, found := strings.Cut(verifyImageScript, "<<'PY'\n")
	if !found {
		t.Fatal("missing production image verifier")
	}
	body = strings.TrimSuffix(body, "PY\n")
	root := t.TempDir()
	guestPath, manifestPath := filepath.Join(root, "axv-guest"), filepath.Join(root, "image.json")
	executable := []byte("fixture guest executable")
	if err = os.WriteFile(guestPath, executable, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(executable)
	contract := guestimage.Contract{ImageVersion: 1, ProtocolVersion: guestproto.Version, Capabilities: []string{"exec"}, BinarySHA256: hex.EncodeToString(digest[:])}
	manifest, _ := json.Marshal(guestimage.Installed{Version: 1, Provider: "lima", Arch: "arm64", Guest: contract})
	if err = os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	// Relocate only the two immutable image files into this owned fixture;
	// execute the production comparison and hashing code unchanged.
	body = strings.ReplaceAll(body, "'/usr/share/axv/image.json'", fmt.Sprintf("%q", manifestPath))
	body = strings.ReplaceAll(body, "'/usr/local/bin/axv-guest'", fmt.Sprintf("%q", guestPath))
	for _, matching := range []bool{true, false} {
		expected := contract
		if !matching {
			expected.BinarySHA256 = strings.Repeat("a", 64)
		}
		data, _ := json.Marshal(expected)
		cmd := exec.Command(python, "-", "arm64", string(data))
		cmd.Stdin = strings.NewReader(body)
		output, err := cmd.CombinedOutput()
		if (err == nil) != matching {
			t.Fatalf("matching=%t: %v %s", matching, err, output)
		}
		if !matching && !strings.Contains(string(output), "does not match selected image") {
			t.Fatalf("selected bundle integrity mismatch lost its diagnosis: %s", output)
		}
	}
}

func TestNewMachineDisablesForwardingForEveryGuestInterface(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Lima VZ requires macOS")
	}
	root := t.TempDir()
	home, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	v := compute.Volume{ID: "vol-test", SizeGiB: 1}
	diskDir := filepath.Join(home, "_disks", v.ID)
	if err = os.MkdirAll(diskDir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(diskDir, "datadisk"))
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(1 << 30)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	disk, _ := json.Marshal(diskInfo{Name: v.ID, Dir: diskDir, Format: "raw", Size: 1 << 30})
	binary := filepath.Join(root, "limactl")
	script := "#!/bin/sh\ncase \"$*\" in\n'--tty=false --version') echo 'limactl version " + compute.SupportedLimaVersion + "';;\n'--tty=false disk list --json') printf '%s' '" + string(disk) + "';;\n'--tty=false list --format=json') ;;\n'--tty=false create '*) ;;\n*) exit 2;;\nesac\n"
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(root, "image")
	if err = os.WriteFile(imagePath, []byte("verified artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("verified artifact"))
	image := compute.Image{ID: "image", Arch: "arm64", Rootfs: imagePath, RootfsSHA256: hex.EncodeToString(digest[:]), Guest: &guestimage.Contract{ImageVersion: 1, ProtocolVersion: guestproto.Version}}
	d := &Driver{config: compute.LimaConfig{Binary: binary, Version: compute.SupportedLimaVersion}, root: root, home: root, owner: "test-host"}
	m := compute.Machine{ID: "vm-test", Spec: compute.Spec{Resources: compute.Resources{CPUs: 1, MemoryMiB: 512, DiskGiB: 1}}}
	if err = d.Ensure(context.Background(), m, image, v); err != nil {
		t.Fatal(err)
	}
	var configuration struct {
		PortForwards []struct {
			GuestIP           string `json:"guestIP"`
			GuestIPMustBeZero *bool  `json:"guestIPMustBeZero"`
			GuestPortRange    [2]int `json:"guestPortRange"`
			Proto             string
			Ignore            bool
		} `json:"portForwards"`
	}
	if err = jsonfile.Read(filepath.Join(d.directory(m), "instance.yaml"), &configuration); err != nil {
		t.Fatal(err)
	}
	if len(configuration.PortForwards) != 1 {
		t.Fatalf("unexpected forwarding rules: %+v", configuration.PortForwards)
	}
	rule := configuration.PortForwards[0]
	if rule.GuestIP != "0.0.0.0" || rule.GuestIPMustBeZero == nil || *rule.GuestIPMustBeZero || rule.GuestPortRange != [2]int{1, 65535} || rule.Proto != "any" || !rule.Ignore {
		t.Fatalf("guest listeners can bypass isolation: %+v", rule)
	}
}

func TestNewRejectsLongMacOSPathBeforeCreatingStorage(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS socket limit")
	}
	root := filepath.Join(t.TempDir(), strings.Repeat("x", 100))
	_, err := New(root, "host-test", compute.LimaConfig{Binary: "/bin/true"})
	if err == nil || !strings.Contains(err.Error(), "shorter lima.home") {
		t.Fatalf("expected actionable path error, got %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("invalid configuration created storage: %v", err)
	}
}

func TestInspect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake limactl uses a POSIX shell")
	}
	for _, tc := range []struct {
		name, output, status string
		exit                 bool
		wantError            bool
	}{
		{name: "empty home", status: "absent"},
		{name: "other instance", output: `{"name":"other","status":"Running"}`, status: "absent"},
		{name: "multiple instances", output: "{\"name\":\"other\",\"status\":\"Stopped\"}\n{\"name\":\"vm-test\",\"status\":\"Running\"}", status: "running"},
		{name: "stopped", output: `{"name":"vm-test","status":"Stopped"}`, status: "stopped"},
		{name: "unknown", output: `{"name":"vm-test","status":"Broken"}`, status: "unknown"},
		{name: "inspection failed", exit: true, wantError: true},
		{name: "instance errors", output: `{"name":"vm-test","status":"Stopped","errors":["disk unreadable"]}`, wantError: true},
		{name: "missing identity", output: `{"status":"Stopped"}`, wantError: true},
		{name: "malformed output", output: `not JSON`, wantError: true},
		{name: "trailing malformed output", output: "{\"name\":\"vm-test\",\"status\":\"Running\"}\ninvalid", wantError: true},
		{name: "duplicate identity", output: "{\"name\":\"vm-test\"}\n{\"name\":\"vm-test\"}", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "limactl")
			script := "#!/bin/sh\n[ \"$*\" = '--tty=false list --format=json' ] || exit 2\n"
			if tc.exit {
				script += "echo 'inspection failed' >&2\nexit 1\n"
			} else {
				script += "printf '%s' '" + strings.ReplaceAll(tc.output, "'", "'\\''") + "'\n"
			}
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			d := &Driver{config: compute.LimaConfig{Binary: binary}, root: root, home: root, owner: "host-test"}
			m := compute.Machine{ID: "vm-test"}
			if err := owneddir.Ensure(d.directory(m), d.owner+":"+m.ID); err != nil {
				t.Fatal(err)
			}
			got, err := d.Inspect(context.Background(), m)
			if (err != nil) != tc.wantError {
				t.Fatalf("Inspect error = %v, want error %v", err, tc.wantError)
			}
			if err == nil && got.Status != tc.status {
				t.Fatalf("Inspect status = %q, want %q", got.Status, tc.status)
			}
		})
	}
}

func TestExplicitHomePreservesStorageOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Lima hosting is not supported on Windows")
	}
	base, err := os.MkdirTemp("/tmp", "axv-lima-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	root := filepath.Join(t.TempDir(), "compute")
	home := filepath.Join(base, "lima")
	config := compute.LimaConfig{Binary: "/bin/true", Version: "test", Home: home}
	d, err := New(root, "host-first", config)
	if err != nil {
		t.Fatal(err)
	}
	if d.home != home {
		t.Fatal(d.home)
	}
	if err = owneddir.Check(home, "host-first"); err != nil {
		t.Fatal(err)
	}
	if _, err = New(root, "host-first", config); err != nil {
		t.Fatal("same host could not reopen home", err)
	}
	if _, err = New(filepath.Join(t.TempDir(), "other"), "host-second", config); err == nil {
		t.Fatal("adopted another host's Lima home")
	}
}

func TestStartUsesOnlyProviderBootForAnImageWithoutProjectVolume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "limactl")
	script := `#!/bin/sh
[ "$*" = '--tty=false start vm-test' ] || { echo "unexpected startup action: $*" >&2; exit 2; }
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d := &Driver{config: compute.LimaConfig{Binary: binary}, root: root, home: root, owner: "fixture-host"}
	m := compute.Machine{ID: "vm-test"}
	if err := owneddir.Ensure(d.directory(m), d.owner+":"+m.ID); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(state{MachineID: m.ID})
	if err := os.WriteFile(filepath.Join(d.directory(m), "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, image := range []compute.Image{{Arch: "arm64"}, {Arch: "arm64", Guest: &guestimage.Contract{ImageVersion: 1, ProtocolVersion: guestproto.Version, Capabilities: guestimage.Capabilities()}}} {
		if err := d.Start(context.Background(), m, image, compute.Volume{}); err != nil {
			t.Fatalf("Start attempted guest installation or readiness work: %v", err)
		}
	}
}
