//go:build linux

// Package firecracker manages owned, jailed Firecracker processes on Linux.
package firecracker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	"github.com/cofy-x/axv/apps/server/internal/filedigest"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

type Driver struct {
	config              compute.FirecrackerConfig
	root, owner, prefix string
	subnet              netip.Prefix
	verifier            filedigest.Verifier
	networkMu           sync.Mutex
}

type state struct {
	MachineID string            `json:"machineId"`
	ImageID   string            `json:"imageId"`
	Guest     guestproto.Config `json:"guest"`
}

func New(root, owner string, config compute.FirecrackerConfig) (compute.Driver, error) {
	subnet, err := netip.ParsePrefix(config.Subnet)
	if err != nil || !subnet.Addr().Is4() || !subnet.Addr().IsPrivate() || subnet != subnet.Masked() || subnet.Bits() < 16 || subnet.Bits() > 22 {
		return nil, fmt.Errorf("Firecracker subnet must be a private, aligned IPv4 /16–/22 network")
	}
	if dns, err := netip.ParseAddr(config.DNS); err != nil || !compute.IsPublicIPv4(dns) {
		return nil, fmt.Errorf("configure a public IPv4 DNS resolver for isolated guests")
	}
	if !filepath.IsAbs(config.Binary) || !filepath.IsAbs(config.Jailer) || config.UID <= 0 || config.UID > 2147483547 || config.GID <= 0 || config.GID > 2147483647 || config.Version == "" {
		return nil, fmt.Errorf("Firecracker requires absolute binary paths, a version pin, and non-root jailer UID/GID")
	}
	if len(owner) < 6 || strings.ContainsFunc(owner, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) {
		return nil, fmt.Errorf("Firecracker requires a valid host ownership identity")
	}
	if err = validateCgroupParent(config.CgroupParent); err != nil {
		return nil, err
	}
	// Jailer resolves --exec-file before choosing the jail's directory name.
	// Pin the same concrete executables so an administrator's symlink cannot
	// select another jail or silently retarget an existing provider.
	config.Binary, err = filepath.EvalSymlinks(config.Binary)
	if err != nil {
		return nil, fmt.Errorf("resolve Firecracker executable: %w", err)
	}
	config.Jailer, err = filepath.EvalSymlinks(config.Jailer)
	if err != nil {
		return nil, fmt.Errorf("resolve jailer executable: %w", err)
	}
	root = filepath.Join(root, "firecracker")
	if err = owneddir.Ensure(root, owner); err != nil {
		return nil, err
	}
	if err = providerutil.PinJSON(filepath.Join(root, "provider.json"), config); err != nil {
		return nil, err
	}
	return &Driver{config: config, root: root, owner: owner, prefix: "axv" + owner[len(owner)-6:], subnet: subnet}, nil
}

func (d *Driver) Probe(ctx context.Context) compute.Capability {
	c := compute.Capability{Provider: "firecracker", PersistentVolumes: true, GuestExecution: true}
	if os.Geteuid() != 0 {
		c.Reason = "Run the isolated Firecracker host service with privileges for jailer, TAP and cgroups."
		return c
	}
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		c.Reason = "KVM is unavailable: " + err.Error()
		return c
	}
	f.Close()
	f, err = os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		c.Reason = "TUN/TAP is unavailable: " + err.Error()
		return c
	}
	f.Close()
	if err = checkCgroupParent("/sys/fs/cgroup", d.config.CgroupParent); err != nil {
		c.Reason = err.Error()
		return c
	}
	forward, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil || strings.TrimSpace(string(forward)) != "1" {
		c.Reason = "Enable IPv4 forwarding explicitly before running this host service."
		return c
	}
	for _, tool := range []string{"ip", "nft", "cp", "mkfs.ext4", "resize2fs"} {
		if _, err = exec.LookPath(tool); err != nil {
			c.Reason = "Required host tool is missing: " + tool
			return c
		}
	}
	for _, executable := range []string{d.config.Binary, d.config.Jailer} {
		out, err := providerutil.Command(ctx, executable, "--version")
		fields := strings.Fields(string(out))
		if err != nil || len(fields) < 2 || strings.TrimPrefix(fields[1], "v") != d.config.Version {
			c.Reason = "Firecracker and jailer must both match the configured version pin."
			return c
		}
	}
	c.Available = true
	return c
}

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

func (d *Driver) Ensure(ctx context.Context, m compute.Machine, image compute.Image, v compute.Volume) error {
	// Check before creating the metadata directory: otherwise missing metadata
	// could hide an existing jail/cgroup and authorize replacement state/disks.
	if exists, err := d.checkMachineDirectory(m); err != nil {
		return err
	} else if exists {
		if err = d.checkStateCreation(m); err != nil {
			return err
		}
	}
	if err := d.checkProjectVolume(m, v.ID); err != nil {
		return err
	}
	if cap := d.Probe(ctx); !cap.Available {
		return fmt.Errorf("%s", cap.Reason)
	}
	if err := d.verifier.Verify(image.Rootfs, image.RootfsSHA256); err != nil {
		return err
	}
	if err := d.verifier.Verify(image.Kernel, image.KernelSHA256); err != nil {
		return err
	}
	if err := owneddir.Ensure(d.directory(m), d.owner+":"+m.ID); err != nil {
		return err
	}
	if err := providerutil.PinJSON(filepath.Join(d.directory(m), "image.json"), image); err != nil {
		return err
	}
	st, err := d.load(m)
	if os.IsNotExist(err) {
		st = state{MachineID: m.ID, ImageID: image.ID, Guest: guestproto.Config{MachineID: m.ID, Token: compute.NewID("token"), ProjectRoot: "/projects", DNS: d.config.DNS}}
		if err = jsonfile.Write(filepath.Join(d.directory(m), "state.json"), st, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if st.ImageID != image.ID {
		return fmt.Errorf("machine image identity changed")
	}
	if _, err = os.Stat(filepath.Join(d.directory(m), "prepared")); err == nil {
		for _, name := range []string{"root.ext4", "kernel", "project.ext4", "seed.ext4"} {
			if _, err = os.Stat(filepath.Join(d.directory(m), name)); err != nil {
				return fmt.Errorf("prepared machine artifact %s is missing; refusing to replace it: %w", name, err)
			}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if len(filepath.Join(d.jail(m), "vsock.sock")) > 100 {
		return fmt.Errorf("Firecracker runtime directory is too long for Unix sockets; configure a shorter dataDir")
	}
	if err = d.ensureVolume(ctx, v); err != nil {
		return err
	}
	root := filepath.Join(d.directory(m), "root.ext4")
	if _, err = os.Stat(root); os.IsNotExist(err) {
		info, err := os.Stat(image.Rootfs)
		if err != nil {
			return err
		}
		if info.Size() > m.Spec.Resources.DiskGiB<<30 {
			return fmt.Errorf("root disk allocation is smaller than the selected image")
		}
		if _, err = providerutil.Command(ctx, "cp", "--reflink=auto", "--sparse=always", "--", image.Rootfs, root+".partial"); err != nil {
			return err
		}
		if err = os.Truncate(root+".partial", m.Spec.Resources.DiskGiB<<30); err != nil {
			return err
		}
		if _, err = providerutil.Command(ctx, "resize2fs", root+".partial"); err != nil {
			return err
		}
		if err = atomicfile.Commit(root+".partial", root); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(d.directory(m), "kernel")); os.IsNotExist(err) {
		if _, err = providerutil.Command(ctx, "cp", "--", image.Kernel, filepath.Join(d.directory(m), "kernel.partial")); err != nil {
			return err
		}
		if err = atomicfile.Commit(filepath.Join(d.directory(m), "kernel.partial"), filepath.Join(d.directory(m), "kernel")); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	project := filepath.Join(d.root, "volumes", v.ID, "disk.ext4")
	link := filepath.Join(d.directory(m), "project.ext4")
	if err = os.Link(project, link); os.IsExist(err) {
		a, aerr := os.Stat(project)
		b, berr := os.Stat(link)
		if aerr != nil || berr != nil || !os.SameFile(a, b) {
			return fmt.Errorf("project disk has unexpected ownership")
		}
	} else if err != nil {
		return err
	}
	seedDir := filepath.Join(d.directory(m), "seed")
	if err = os.MkdirAll(seedDir, 0700); err != nil {
		return err
	}
	if err = jsonfile.Write(filepath.Join(seedDir, "guest.json"), st.Guest, 0600); err != nil {
		return err
	}
	if err = os.Chown(filepath.Join(seedDir, "guest.json"), 1000, 1000); err != nil {
		return err
	}
	if err = disk(ctx, filepath.Join(d.directory(m), "seed.ext4"), 8<<20, "axv-seed", seedDir); err != nil {
		return err
	}
	for _, name := range []string{"root.ext4", "project.ext4", "seed.ext4", "kernel"} {
		if err = os.Chown(filepath.Join(d.directory(m), name), d.config.UID+m.Slot, d.config.GID); err != nil {
			return err
		}
	}
	return atomicfile.Write(filepath.Join(d.directory(m), "prepared"), []byte(m.ID), 0600)
}

func disk(ctx context.Context, path string, size int64, label, source string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	temporary := path + ".partial"
	f, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = f.Truncate(size)
	f.Close()
	if err != nil {
		return err
	}
	args := []string{"-F", "-q", "-L", label}
	if source != "" {
		args = append(args, "-d", source)
	}
	args = append(args, temporary)
	if _, err = providerutil.Command(ctx, "mkfs.ext4", args...); err != nil {
		return err
	}
	return atomicfile.Commit(temporary, path)
}

func (d *Driver) ensureVolume(ctx context.Context, v compute.Volume) error {
	dir := filepath.Join(d.root, "volumes", v.ID)
	if v.Revision > 1 {
		if exists, err := checkManagedDirectory(dir, d.owner+":"+v.ID); err != nil {
			return err
		} else if !exists {
			return fmt.Errorf("existing project volume %s storage is missing; refusing to recreate it", v.ID)
		}
	}
	if err := owneddir.Ensure(dir, d.owner+":"+v.ID); err != nil {
		return err
	}
	if data, err := os.ReadFile(filepath.Join(dir, "created")); err == nil {
		if string(data) != v.ID {
			return fmt.Errorf("project volume creation identity mismatch")
		}
		info, err := os.Lstat(filepath.Join(dir, "disk.ext4"))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("project volume disk is not a regular file")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if v.Revision > 1 {
		return fmt.Errorf("existing project volume %s creation metadata is missing; refusing to recreate it", v.ID)
	}
	if err := disk(ctx, filepath.Join(dir, "disk.ext4"), v.SizeGiB<<30, "axv-project", ""); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, "created"), []byte(v.ID), 0600)
}

func (d *Driver) checkProjectVolume(m compute.Machine, volumeID string) error {
	for _, path := range []string{filepath.Join(d.directory(m), "project.ext4"), filepath.Join(d.jail(m), "project.ext4")} {
		project, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue // Initial preparation may not have linked a project disk yet.
		}
		if err != nil {
			return err
		}
		if !project.Mode().IsRegular() || volumeID == "" {
			return fmt.Errorf("machine project disk has an invalid volume identity")
		}
		volume, err := d.projectVolumeDisk(volumeID)
		if err != nil {
			return err
		}
		if !os.SameFile(project, volume) {
			return fmt.Errorf("machine project disk does not match its owned project volume")
		}
	}
	return nil
}

func (d *Driver) checkRetainedProjectDeletion(m compute.Machine) error {
	if err := d.checkProjectVolume(m, m.Spec.VolumeID); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(d.directory(m), "prepared")); err == nil {
		// Preparation proves that a project disk existed, even if both machine
		// links have since disappeared. Its retained volume must still be valid.
		_, err = d.projectVolumeDisk(m.Spec.VolumeID)
		return err
	} else if !os.IsNotExist(err) {
		return err
	}
	// No project link and no completed preparation is an initial failure.
	// Deleting this runtime leaves any volume staging files untouched.
	return nil
}

func (d *Driver) projectVolumeDisk(volumeID string) (os.FileInfo, error) {
	if volumeID == "" {
		return nil, fmt.Errorf("machine has no project volume identity")
	}
	dir := filepath.Join(d.root, "volumes", volumeID)
	if exists, err := checkManagedDirectory(dir, d.owner+":"+volumeID); err != nil {
		return nil, err
	} else if !exists {
		return nil, fmt.Errorf("project volume %s storage is missing; preserve any linked project disk until its volume is recovered", volumeID)
	}
	data, err := os.ReadFile(filepath.Join(dir, "created"))
	if err != nil {
		return nil, fmt.Errorf("verify project volume creation identity: %w", err)
	}
	if string(data) != volumeID {
		return nil, fmt.Errorf("project volume creation identity mismatch")
	}
	volume, err := os.Lstat(filepath.Join(dir, "disk.ext4"))
	if err != nil {
		return nil, fmt.Errorf("project volume %s disk is missing; preserve any linked project disk until its volume is recovered: %w", volumeID, err)
	}
	if !volume.Mode().IsRegular() {
		return nil, fmt.Errorf("project volume disk is not a regular file")
	}
	return volume, nil
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

func (d *Driver) Start(ctx context.Context, m compute.Machine, _ compute.Image, _ compute.Volume) error {
	if _, err := d.load(m); err != nil {
		return err
	}
	o, err := d.Inspect(ctx, m)
	if err != nil {
		return err
	}
	if o.Status == "running" {
		return nil
	}
	if o.Status != "stopped" {
		return fmt.Errorf("runtime is not confirmed stopped: %s", o.Status)
	}
	if err = d.prepareJail(m); err != nil {
		return err
	}
	if err = d.prepareCgroup(m); err != nil {
		return err
	}
	if err = d.ensureNetwork(ctx, m); err != nil {
		return err
	}
	gateway, address := d.addresses(m)
	console := "ttyS0"
	if runtime.GOARCH == "arm64" {
		console = "ttyAMA0"
	}
	config := map[string]any{
		"boot-source":    map[string]any{"kernel_image_path": "/kernel", "boot_args": fmt.Sprintf("console=%s reboot=k panic=1 pci=off root=/dev/vda rw rootfstype=ext4 ip=%s::%s:255.255.255.252:%s:eth0:off", console, address, gateway, m.ID)},
		"machine-config": map[string]any{"vcpu_count": m.Spec.Resources.CPUs, "mem_size_mib": m.Spec.Resources.MemoryMiB},
		"drives": []any{
			map[string]any{"drive_id": "root", "path_on_host": "/root.ext4", "is_root_device": true, "is_read_only": false},
			map[string]any{"drive_id": "project", "path_on_host": "/project.ext4", "is_root_device": false, "is_read_only": false},
			map[string]any{"drive_id": "seed", "path_on_host": "/seed.ext4", "is_root_device": false, "is_read_only": true},
		},
		"network-interfaces": []any{map[string]any{"iface_id": "eth0", "host_dev_name": d.tap(m), "guest_mac": fmt.Sprintf("06:00:ac:10:00:%02x", m.Slot)}},
		"vsock":              map[string]any{"guest_cid": m.Slot + 3, "uds_path": "/vsock.sock"},
	}
	if err = jsonfile.Write(filepath.Join(d.jail(m), "machine.json"), config, 0644); err != nil {
		return err
	}
	if err = jsonfile.Write(filepath.Join(d.directory(m), "launch.json"), map[string]time.Time{"at": time.Now().UTC()}, 0600); err != nil {
		return err
	}
	if _, err = providerutil.Command(ctx, d.config.Jailer, d.jailerArgs(m)...); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		o, err = d.Inspect(ctx, m)
		if err == nil && o.Status == "running" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
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

func (d *Driver) Inspect(ctx context.Context, m compute.Machine) (compute.Observation, error) {
	if exists, err := d.checkMachineDirectory(m); err != nil {
		return compute.Observation{}, err
	} else if !exists {
		return compute.Observation{Status: "absent"}, nil
	}
	var info struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := d.api(ctx, m, "GET", "/", nil, &info); err == nil {
		if info.ID != m.ID {
			return compute.Observation{}, fmt.Errorf("Firecracker socket identity mismatch")
		}
		if info.State == "Running" {
			return compute.Observation{Status: "running"}, nil
		}
		return compute.Observation{Status: "unknown", Reason: "Firecracker state: " + info.State}, nil
	}
	procs, err := os.ReadFile(filepath.Join(d.cgroup(m), "cgroup.procs"))
	if err != nil && !os.IsNotExist(err) {
		return compute.Observation{}, err
	}
	if strings.TrimSpace(string(procs)) != "" {
		return compute.Observation{}, fmt.Errorf("VM process exists but its control socket is unavailable")
	}
	var launch struct {
		At time.Time `json:"at"`
	}
	if err = jsonfile.Read(filepath.Join(d.directory(m), "launch.json"), &launch); err == nil {
		if time.Since(launch.At) < 30*time.Second {
			return compute.Observation{Status: "unknown", Reason: "A recent launch has not settled."}, nil
		}
	} else if !os.IsNotExist(err) {
		return compute.Observation{}, err
	}
	return compute.Observation{Status: "stopped"}, nil
}

func (d *Driver) Stop(ctx context.Context, m compute.Machine) error {
	if exists, err := d.checkMachineDirectory(m); err != nil {
		return err
	} else if !exists {
		return nil
	}
	procs, err := os.ReadFile(filepath.Join(d.cgroup(m), "cgroup.procs"))
	if os.IsNotExist(err) {
		return d.confirmStopped(m)
	} else if err != nil {
		return err
	}
	if strings.TrimSpace(string(procs)) == "" {
		return d.confirmStopped(m)
	}
	// Ask the guest to shut down; an explicit stop has a bounded force-stop fallback.
	if token, tokenErr := d.GuestToken(m); tokenErr == nil {
		transport := compute.GuestTransport(m, d)
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://guest/v1/shutdown", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, _ := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Do(req)
		if res != nil {
			res.Body.Close()
		}
		transport.CloseIdleConnections()
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		procs, err = os.ReadFile(filepath.Join(d.cgroup(m), "cgroup.procs"))
		if os.IsNotExist(err) || (err == nil && strings.TrimSpace(string(procs)) == "") {
			return d.confirmStopped(m)
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	// cgroup.kill identifies the owned group atomically, without PID-reuse races.
	if err = os.WriteFile(filepath.Join(d.cgroup(m), "cgroup.kill"), []byte("1"), 0200); err != nil {
		return err
	}
	for {
		procs, err = os.ReadFile(filepath.Join(d.cgroup(m), "cgroup.procs"))
		if os.IsNotExist(err) || (err == nil && strings.TrimSpace(string(procs)) == "") {
			return d.confirmStopped(m)
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
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

func (d *Driver) Delete(ctx context.Context, m compute.Machine) error {
	o, err := d.Inspect(ctx, m)
	if err != nil {
		return err
	}
	if o.Status == "absent" {
		return nil
	}
	if o.Status != "stopped" {
		return fmt.Errorf("cannot delete an active VM")
	}
	if _, err = checkManagedDirectory(filepath.Dir(d.jail(m)), d.owner+":"+m.ID); err != nil {
		return err
	}
	if m.Spec.RetainVolume {
		if err = d.checkRetainedProjectDeletion(m); err != nil {
			return err
		}
	}
	if err = d.removeNetwork(ctx, m); err != nil {
		return err
	}
	if err = os.Remove(d.cgroup(m)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = os.RemoveAll(filepath.Dir(d.jail(m))); err != nil {
		return err
	}
	return os.RemoveAll(d.directory(m))
}

func (d *Driver) DeleteVolume(_ context.Context, v compute.Volume) error {
	dir := filepath.Join(d.root, "volumes", v.ID)
	if exists, err := checkManagedDirectory(dir, d.owner+":"+v.ID); err != nil {
		return err
	} else if !exists {
		return nil
	}
	return os.RemoveAll(dir)
}

func (d *Driver) GuestToken(m compute.Machine) (string, error) {
	st, err := d.load(m)
	return st.Guest.Token, err
}

func (d *Driver) DialGuest(ctx context.Context, m compute.Machine) (net.Conn, error) {
	if _, err := d.load(m); err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(d.jail(m), "vsock.sock"))
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = fmt.Fprintf(conn, "CONNECT %d\n", guestproto.Port); err != nil {
		conn.Close()
		return nil, err
	}
	line, err := bufio.NewReader(io.LimitReader(conn, 128)).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "OK ") {
		conn.Close()
		return nil, errors.New("guest vsock connection failed")
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
