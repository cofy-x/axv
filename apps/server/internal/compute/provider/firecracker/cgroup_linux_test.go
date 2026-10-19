//go:build linux

package firecracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCgroupParentRejectsEscapes(t *testing.T) {
	for _, parent := range []string{"", ".", "/system.slice/axv.service", "../axv", "service/../axv", "service/./axv", "service//axv", "service/", "service\naxv", "service\x00axv"} {
		if err := validateCgroupParent(parent); err == nil {
			t.Errorf("accepted invalid delegated parent %q", parent)
		}
	}
	if err := validateCgroupParent("system.slice/axv-host.service"); err != nil {
		t.Fatal(err)
	}
}

func TestDelegatedCgroupRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, controllers, processes, reason string
		kill, subtree                        bool
	}{
		{name: "ready", controllers: "cpu memory pids", kill: true, subtree: true},
		{name: "missing memory", controllers: "cpu pids", kill: true, reason: "memory controller"},
		{name: "missing kill", controllers: "cpu memory pids", reason: "cgroup.kill"},
		{name: "resident controller", controllers: "cpu memory pids", processes: "123\n", kill: true, reason: "no resident processes"},
		{name: "unavailable subtree control", controllers: "cpu memory pids", kill: true, reason: "allow configuring delegated controllers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			parent := "system.slice/axv-host.service"
			path := filepath.Join(root, parent)
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{"cgroup.controllers": tc.controllers, "cgroup.procs": tc.processes}
			if tc.kill {
				files["cgroup.kill"] = ""
			}
			if tc.subtree {
				files["cgroup.subtree_control"] = "cpuset\n"
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(path, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := checkCgroupParent(root, parent)
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("wanted %q diagnosis, got %v", tc.reason, err)
			}
			// Inspection must never enable ancestor controllers or create groups.
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 {
				t.Fatalf("probe changed the hierarchy: %v %v", entries, err)
			}
			if tc.subtree {
				if data, err := os.ReadFile(filepath.Join(path, "cgroup.subtree_control")); err != nil || string(data) != "cpuset\n" {
					t.Fatalf("probe changed delegated controllers: %q %v", data, err)
				}
			}
		})
	}
}
