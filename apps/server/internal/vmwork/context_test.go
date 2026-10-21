//go:build darwin || linux

package vmwork

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func contextFixture() Context {
	return Context{
		Version: Version, SessionID: "session-original", RunID: "run-original",
		Project: Project{
			Project: compute.Project{ID: "project-original", HostID: "host-original", Kind: "vm", Name: "Example", VolumeID: "volume-original", Root: "repo", RootIdentity: "inode:original", Revision: 2},
			Binding: compute.ProjectBinding{HostID: "host-original", ProjectID: "project-original", ProjectRevision: 2, VolumeID: "volume-original", VolumeRevision: 3, MachineID: "vm-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BootID: "boot-original", InstanceID: "guest-original"},
		},
		Socket: "/private/host.sock", ConfigFingerprint: "config-original", Executable: "/private/bin/axv-original",
	}
}

func privateTestDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestPublicationPreservesExactContextAndPackagedSkill(t *testing.T) {
	directory := privateTestDirectory(t)
	want := contextFixture()
	contextPath, skillPath, err := Publish(directory, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadContext(contextPath)
	if err != nil || got != want || ValidateSkill(skillPath) != nil {
		t.Fatalf("publication lost its captured context or skill: %+v %v", got, err)
	}
	for _, path := range []string{contextPath, skillPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("publication is not private: %s %v %v", path, info, err)
		}
	}
	if repeated, repeatedSkill, err := Publish(directory, want); err != nil || repeated != contextPath || repeatedSkill != skillPath {
		t.Fatalf("identical publication was not recoverable: %q %q %v", repeated, repeatedSkill, err)
	}
	changed := want
	changed.RunID, changed.Socket = "run-new", "/private/changed.sock"
	if _, _, err = Publish(directory, changed); err == nil {
		t.Fatal("different publication overwrote the original run")
	}
	if got, err := ReadContext(contextPath); err != nil || got != want {
		t.Fatalf("failed publication modified the original context: %+v %v", got, err)
	}
	original, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("untrusted replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if ValidateSkill(skillPath) == nil {
		t.Fatal("edited skill remained trusted")
	}
	if _, _, err := Publish(directory, want); err == nil {
		t.Fatal("publication silently replaced an edited skill")
	}
	current, _ := os.ReadFile(skillPath)
	if bytes.Equal(original, current) {
		t.Fatal("failed publication discarded the edited file")
	}
}

func TestContextRejectsAmbiguousDamagedOrUnsupportedMetadata(t *testing.T) {
	data, err := json.Marshal(contextFixture())
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	for name, content := range map[string]string{
		"unknown field":             strings.Replace(valid, `"version":1`, `"version":1,"unknown":true`, 1),
		"unknown nested field":      strings.Replace(valid, `"kind":"vm"`, `"kind":"vm","unknown":true`, 1),
		"duplicate identity":        strings.Replace(valid, `"runId":"run-original"`, `"runId":"run-new","runId":"run-original"`, 1),
		"duplicate field alias":     strings.Replace(valid, `"runId":"run-original"`, `"runId":"run-new","RunID":"run-original"`, 1),
		"duplicate Unicode alias":   strings.Replace(valid, `"sessionId":"session-original"`, `"sessionId":"session-new","ſessionId":"session-original"`, 1),
		"duplicate nested identity": strings.Replace(valid, `"bootId":"boot-original"`, `"bootId":"boot-new","bootId":"boot-original"`, 1),
		"invalid unicode":           strings.Replace(valid, "run-original", `run-\ud800`, 1),
		"trailing object":           valid + `{}`,
		"null":                      `null`,
		"unsupported version":       strings.Replace(valid, `"version":1`, `"version":2`, 1),
		"oversized":                 valid + strings.Repeat(" ", maxContextBytes),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(privateTestDirectory(t), "vm-project.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadContext(path); err == nil {
				t.Fatal("unsafe context metadata was accepted")
			}
			current, _ := os.ReadFile(path)
			if string(current) != content {
				t.Fatal("invalid metadata was silently reset")
			}
		})
	}
}

func TestContextRejectsChangedProjectAndClientIdentities(t *testing.T) {
	for name, modify := range map[string]func(*Context){
		"project ID":               func(c *Context) { c.Project.Project.ID = "project-other" },
		"project host":             func(c *Context) { c.Project.Project.HostID = "host-other" },
		"project volume":           func(c *Context) { c.Project.Project.VolumeID = "volume-other" },
		"project revision":         func(c *Context) { c.Project.Project.Revision++ },
		"missing root identity":    func(c *Context) { c.Project.Project.RootIdentity = "" },
		"host project":             func(c *Context) { c.Project.Project.Kind = "host" },
		"escaping root":            func(c *Context) { c.Project.Project.Root = "../outside" },
		"VM name instead of ID":    func(c *Context) { c.Project.Binding.MachineID = "my-vm" },
		"missing boot":             func(c *Context) { c.Project.Binding.BootID = "" },
		"missing guest instance":   func(c *Context) { c.Project.Binding.InstanceID = "" },
		"zero attachment revision": func(c *Context) { c.Project.Binding.VolumeRevision = 0 },
		"missing fingerprint":      func(c *Context) { c.ConfigFingerprint = "" },
		"relative socket":          func(c *Context) { c.Socket = "host.sock" },
		"noncanonical executable":  func(c *Context) { c.Executable = "/bin/../axv" },
		"control run identity":     func(c *Context) { c.RunID = "run\nother" },
	} {
		t.Run(name, func(t *testing.T) {
			value := contextFixture()
			modify(&value)
			if err := ValidateContext(value); err == nil {
				t.Fatal("context accepted incomplete or mismatched identities")
			}
		})
	}
}

func TestContextRequiresPrivateOwnedRegularFiles(t *testing.T) {
	for _, mode := range []string{"symlink", "hardlink", "public file", "public directory", "directory"} {
		t.Run(mode, func(t *testing.T) {
			directory := privateTestDirectory(t)
			path, _, err := Publish(directory, contextFixture())
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".alias"); err != nil {
					t.Fatal(err)
				}
			case "public file":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "public directory":
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadContext(path); err == nil {
				t.Fatal("unsafe metadata path was accepted")
			}
			if _, _, err := Publish(directory, contextFixture()); err == nil {
				t.Fatal("publication replaced an unsafe existing path")
			}
		})
	}
}
