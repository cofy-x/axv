package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/app"
	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestManifestRejectsMalformedOrForeignOwnership(t *testing.T) {
	for _, kind := range []string{"valid", "legacy version", "legacy label", "missing launch identity", "missing desktop settings", "missing arguments", "foreign label", "foreign executable", "foreign snapshot", "foreign profile", "duplicate job"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, "run"), 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(home, "run", "desktop-test.json")
			label := jobLabel(home, "desktop", marker)
			job := Job{Role: "desktop", Label: label, Args: []string{filepath.Join(home, "bin", "axv-test"), "_desktop", "--snapshot", marker}, Marker: marker, Definition: filepath.Join(home, "run", label+".plist"), Log: filepath.Join(home, "logs", "desktop.log"), Environment: map[string]string{"AXV_HOME": home}}
			switch kind {
			case "legacy label":
				job.Label = "com.axv." + profile.Identity(home) + ".desktop"
				job.Definition = filepath.Join(home, "run", job.Label+".plist")
			case "missing arguments":
				job.Args = nil
			case "foreign label":
				job.Label = "another.service"
			case "foreign executable":
				job.Args[0] = filepath.Join(home, "other", "axv-test")
			case "foreign snapshot":
				job.Marker = filepath.Join(home, "other.json")
				job.Args[3] = job.Marker
			}
			m := manifest{Version: 4, Home: home, Desktop: &desktoplaunch.Snapshot{Inputs: desktoplaunch.Inputs{Home: home, LaunchID: "fixture-launch"}}, Jobs: []Job{job}}
			if kind == "missing launch identity" {
				m.Desktop.LaunchID = ""
			}
			if kind == "missing desktop settings" {
				m.Desktop = nil
			}
			if kind == "legacy version" {
				m.Version = 1
			}
			if kind == "foreign profile" {
				m.Home = filepath.Join(home, "other")
			}
			if kind == "duplicate job" {
				m.Jobs = append(m.Jobs, job)
			}
			if err := jsonfile.Write(manifestPath(home), m, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := readManifest(home)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}

func TestDesktopFingerprintExcludesLiveComputeConfiguration(t *testing.T) {
	previous := app.Settings{Inputs: desktoplaunch.Inputs{Home: t.TempDir(), Desktop: config.Config{WorkspaceDir: "/projects"}}, ComputeSettings: app.ComputeSettings{Compute: profile.Compute{Mode: "disabled"}}}
	next := previous
	next.Compute = profile.Compute{Mode: "managed", HostConfig: "/profile/host.json"}
	next.Host = &compute.Config{Socket: "/profile/host.sock"}
	next.LaunchID = "another-launch"
	next.RunnerExecutable = "/profile/bin/axv-pinned"
	if previous.Fingerprint() != next.Fingerprint() {
		t.Fatal("compute activation invalidated desktop fingerprint")
	}
	next.Environment = map[string]string{"AXV_CACHE_HOME": "/explicit/cache"}
	if previous.Fingerprint() == next.Fingerprint() {
		t.Fatal("cache location was omitted from immutable desktop snapshot")
	}
}

func TestServiceIncarnationsDoNotReuseUnitsWithPersistentProcesses(t *testing.T) {
	home := t.TempDir()
	first := jobLabel(home, "desktop", filepath.Join(home, "run", "desktop-first.json"))
	next := jobLabel(home, "desktop", filepath.Join(home, "run", "desktop-next.json"))
	if first == next || first == jobLabel(home, "host", filepath.Join(home, "run", "desktop-first.json")) {
		t.Fatal("distinct service incarnations reused a supervisor unit")
	}
}
