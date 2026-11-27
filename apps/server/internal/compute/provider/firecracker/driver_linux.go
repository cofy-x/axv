//go:build linux

// Package firecracker manages owned, jailed Firecracker processes on Linux.
package firecracker

import (
	"bufio"
	"context"
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
	c := compute.Capability{Provider: "firecracker", PersistentVolumes: true, GuestExecution: true, VolumeCopy: true}
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
