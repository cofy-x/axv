package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestResolveComputeHasNoDesktopOrInitializationDependency(t *testing.T) {
	t.Setenv("AXV_COMPUTE_SOCKET", "")
	t.Setenv("DATA_DIR", "/unrelated-desktop-data")
	t.Setenv("ADDR", "invalid")
	t.Setenv("AXV_WORKSPACE_DIR", "/unrelated-host-projects")
	home := filepath.Join(t.TempDir(), "absent")
	s, err := ResolveCompute(home)
	if err != nil || s.Compute.Mode != "disabled" || s.Desktop != (config.Config{}) || s.Environment != nil {
		t.Fatalf("unexpected resolution: %#v %v", s, err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("resolution initialized the profile: %v", err)
	}
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"runtime_version":1,"desktop":{"addr":false,"data_dir":"relative"},"workspace_dir":42,"proxy_url":false,"compute":{"mode":"external","socket":"/run/profile.sock"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = ResolveCompute(home)
	if err != nil || s.Compute.Socket != "/run/profile.sock" || s.Host != nil || s.Desktop != (config.Config{}) {
		t.Fatalf("desktop settings blocked compute: %#v %v", s, err)
	}
}

func TestResolveComputeSocketOverridePrecedesBrokenProfileCompute(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"runtime_version":1,"compute":{"mode":"managed","host_config":"missing"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AXV_COMPUTE_SOCKET", "/run/override.sock")
	s, err := ResolveCompute(home)
	if err != nil || s.Compute != (profile.Compute{Mode: "external", Socket: "/run/override.sock"}) || s.Host != nil {
		t.Fatalf("override was not authoritative: %#v %v", s, err)
	}
	t.Setenv("AXV_COMPUTE_SOCKET", "")
	if _, err := ResolveCompute(home); err == nil {
		t.Fatal("accepted invalid compute configuration without override")
	}
	t.Setenv("AXV_COMPUTE_SOCKET", "relative")
	if _, err := ResolveCompute(home); err == nil {
		t.Fatal("accepted relative socket override")
	}
}

func TestResolveComputeLoadsManagedLimaButNeverManagesFirecracker(t *testing.T) {
	t.Setenv("AXV_COMPUTE_SOCKET", "")
	s, c := runtimeFixture(t)
	path := filepath.Join(s.Home, "host.json")
	if err := jsonfile.Write(path, c, 0600); err != nil {
		t.Fatal(err)
	}
	if err := profile.SaveCompute(s.Home, profile.Compute{Mode: "managed", HostConfig: path}); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveCompute(s.Home)
	if err != nil || resolved.Host == nil || resolved.Host.Fingerprint() != c.Fingerprint() || resolved.Compute.Socket != c.Socket {
		t.Fatalf("managed resolution: %#v %v", resolved, err)
	}
	c.Lima = nil
	c.Firecracker = &compute.FirecrackerConfig{Binary: "/test/firecracker"}
	if err := jsonfile.Write(path, c, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCompute(s.Home); err == nil {
		t.Fatal("accepted privileged managed host")
	}
}
