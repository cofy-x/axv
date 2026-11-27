//go:build linux

package firecracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func (d *Driver) directory(m compute.Machine) string { return filepath.Join(d.root, "machines", m.ID) }
func (d *Driver) jail(m compute.Machine) string {
	return filepath.Join(d.root, "jails", filepath.Base(d.config.Binary), m.ID, "root")
}
func (d *Driver) cgroup(m compute.Machine) string {
	return filepath.Join("/sys/fs/cgroup", d.cgroupParent(), m.ID)
}
func (d *Driver) cgroupParent() string         { return filepath.Join(d.config.CgroupParent, "axv-"+d.owner) }
func (d *Driver) tap(m compute.Machine) string { return fmt.Sprintf("%s%03d", d.prefix, m.Slot) }

// Only a missing directory proves absence. A missing marker in an existing
// directory is damaged ownership metadata, never permission to adopt or delete.
func checkManagedDirectory(path, owner string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return true, fmt.Errorf("runtime path is not a real directory: %s", path)
	}
	if err = owneddir.Check(path, owner); err != nil {
		return true, fmt.Errorf("verify runtime ownership at %s: %v", path, err)
	}
	return true, nil
}

func (d *Driver) checkMachineDirectory(m compute.Machine) (bool, error) {
	exists, err := checkManagedDirectory(d.directory(m), d.owner+":"+m.ID)
	if exists || err != nil {
		return exists, err
	}
	for _, path := range []string{filepath.Dir(d.jail(m)), d.cgroup(m)} {
		if _, err = os.Lstat(path); err == nil {
			return false, fmt.Errorf("machine ownership metadata is missing while provider resources remain at %s", path)
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

func (d *Driver) load(m compute.Machine) (state, error) {
	var st state
	if exists, err := d.checkMachineDirectory(m); err != nil {
		return st, err
	} else if !exists {
		return st, os.ErrNotExist
	}
	err := jsonfile.Read(filepath.Join(d.directory(m), "state.json"), &st)
	if err == nil && st.MachineID != m.ID {
		err = fmt.Errorf("Firecracker runtime identity mismatch")
	}
	return st, err
}

func (d *Driver) checkStateCreation(m compute.Machine) error {
	if info, err := os.Lstat(filepath.Join(d.directory(m), "state.json")); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("machine state is not a regular file")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	// A first preparation may have committed only ownership/image metadata or
	// left an unpublished atomic-write file. Disk preparation follows state.json.
	entries, err := os.ReadDir(d.directory(m))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".axv-owner" || entry.Name() == "image.json" || strings.HasPrefix(entry.Name(), ".axv-write-") && entry.Type().IsRegular() {
			continue
		}
		return fmt.Errorf("machine state is missing while prepared resources remain; refusing to replace its identity")
	}
	for _, path := range []string{filepath.Dir(d.jail(m)), d.cgroup(m)} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("machine state is missing while runtime resources remain; refusing to replace its identity")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// prepareJail is called only after inspection confirms the VM is stopped.
// Jailer creates devices, PID files and architecture-specific files exclusively,
// so each launch needs a fresh jail. Durable disks stay outside that directory.
func (d *Driver) prepareJail(m compute.Machine) error {
	if exists, err := d.checkMachineDirectory(m); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("machine ownership metadata is missing")
	}
	names := []string{"root.ext4", "project.ext4", "seed.ext4", "kernel"}
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(d.directory(m), name))
		if err != nil {
			return fmt.Errorf("prepared machine artifact %s is unavailable: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("prepared machine artifact %s is not a regular file", name)
		}
	}
	if err := owneddir.Ensure(filepath.Dir(d.jail(m)), d.owner+":"+m.ID); err != nil {
		return err
	}
	if err := os.RemoveAll(d.jail(m)); err != nil {
		return err
	}
	if err := os.Mkdir(d.jail(m), 0700); err != nil {
		return err
	}
	for _, name := range names {
		if err := os.Link(filepath.Join(d.directory(m), name), filepath.Join(d.jail(m), name)); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) jailerArgs(m compute.Machine) []string {
	// With no --cgroup properties, jailer only joins this prepared leaf.
	// Its property-writing mode recursively enables controllers in ancestors,
	// which would cross the administrator's delegated cgroup boundary.
	return []string{"--id", m.ID, "--exec-file", d.config.Binary, "--uid", strconv.Itoa(d.config.UID + m.Slot), "--gid", strconv.Itoa(d.config.GID), "--chroot-base-dir", filepath.Join(d.root, "jails"), "--cgroup-version", "2", "--parent-cgroup", filepath.Join(d.cgroupParent(), m.ID), "--new-pid-ns", "--daemonize", "--", "--api-sock", "/api.sock", "--config-file", "/machine.json"}
}

func (d *Driver) api(ctx context.Context, m compute.Machine, method, path string, body any, target any) error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(d.jail(m), "api.sock"))
	}}
	defer transport.CloseIdleConnections()
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, "http://firecracker"+path, input)
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Firecracker API: %s", strings.TrimSpace(string(b)))
	}
	if target != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(target)
	}
	return nil
}

func (d *Driver) confirmStopped(m compute.Machine) error {
	// A confirmed stop supersedes startup's grace period, including a fast
	// stop/start or restart immediately after the first successful launch.
	err := os.Remove(filepath.Join(d.directory(m), "launch.json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
