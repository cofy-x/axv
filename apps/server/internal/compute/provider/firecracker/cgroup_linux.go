//go:build linux

package firecracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func validateCgroupParent(parent string) error {
	if parent == "" || parent == "." || filepath.IsAbs(parent) || filepath.Clean(parent) != parent || strings.ContainsFunc(parent, unicode.IsControl) {
		return fmt.Errorf("Firecracker cgroupParent must name a clean relative delegated cgroup v2 directory")
	}
	for _, component := range strings.Split(parent, string(filepath.Separator)) {
		if component == ".." {
			return fmt.Errorf("Firecracker cgroupParent must stay within the cgroup v2 hierarchy")
		}
	}
	return nil
}

func checkCgroupParent(root, parent string) error {
	if err := validateCgroupParent(parent); err != nil {
		return err
	}
	path := filepath.Join(root, parent)
	data, err := os.ReadFile(filepath.Join(path, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("Firecracker requires an administrator-delegated cgroup v2 directory at %s: %w", path, err)
	}
	controllers := strings.Fields(string(data))
	for _, required := range []string{"cpu", "memory", "pids"} {
		found := false
		for _, controller := range controllers {
			found = found || controller == required
		}
		if !found {
			return fmt.Errorf("delegate the %s controller to Firecracker cgroupParent %s", required, path)
		}
	}
	if _, err = os.Stat(filepath.Join(path, "cgroup.kill")); err != nil {
		return fmt.Errorf("Firecracker requires cgroup v2 with cgroup.kill: %w", err)
	}
	data, err = os.ReadFile(filepath.Join(path, "cgroup.procs"))
	if err != nil {
		return fmt.Errorf("inspect delegated Firecracker cgroup: %w", err)
	}
	if strings.TrimSpace(string(data)) != "" {
		return fmt.Errorf("Firecracker cgroupParent %s must have no resident processes; place the controller in a separate subgroup", path)
	}
	f, err := os.OpenFile(filepath.Join(path, "cgroup.subtree_control"), os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("Firecracker cgroupParent %s must allow configuring delegated controllers: %w", path, err)
	}
	if err = f.Close(); err != nil {
		return err
	}
	return nil
}

func (d *Driver) prepareCgroup(m compute.Machine) error {
	const root = "/sys/fs/cgroup"
	if err := checkCgroupParent(root, d.config.CgroupParent); err != nil {
		return err
	}
	// Only the explicitly delegated parent and this host's private subtree are
	// configured. Never let jailer change controller state in their ancestors.
	for _, path := range []string{filepath.Join(root, d.config.CgroupParent), filepath.Join(root, d.cgroupParent())} {
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0200); err != nil {
			return fmt.Errorf("enable delegated Firecracker controllers at %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(d.cgroup(m), 0755); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"memory.max": fmt.Sprint((m.Spec.Resources.MemoryMiB + 128) << 20),
		"cpu.max":    fmt.Sprintf("%d 100000", m.Spec.Resources.CPUs*100000),
		"pids.max":   "128",
	} {
		if err := os.WriteFile(filepath.Join(d.cgroup(m), name), []byte(value), 0200); err != nil {
			return fmt.Errorf("configure Firecracker %s: %w", name, err)
		}
	}
	return nil
}
