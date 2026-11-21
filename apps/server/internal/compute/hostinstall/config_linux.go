//go:build linux && amd64

package hostinstall

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"golang.org/x/sys/unix"
)

const systemPath = "/usr/sbin:/usr/bin:/sbin:/bin"

func systemTool(name string) (string, error) {
	for _, dir := range filepath.SplitList(systemPath) {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("install the system tool %s, then run the installer again", name)
}

func inspectSystem(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, err := systemTool(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=" + systemPath, "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func initialConfig(ctx context.Context, socketGID int) (compute.Config, error) {
	var config compute.Config
	if err := checkPrerequisites(ctx); err != nil {
		return config, err
	}
	routes, err := inspectSystem(ctx, "ip", "-j", "-4", "route", "show", "table", "all")
	if err != nil {
		return config, err
	}
	subnet, err := guestSubnet(routes)
	if err != nil {
		return config, err
	}
	jailID, err := freeJailIDs(ctx, socketGID)
	if err != nil {
		return config, err
	}
	var memory unix.Sysinfo_t
	var disk unix.Statfs_t
	if err := unix.Sysinfo(&memory); err != nil {
		return config, fmt.Errorf("read host memory: %w", err)
	}
	if err := unix.Statfs("/var/lib", &disk); err != nil {
		return config, fmt.Errorf("read free space for VM data: %w", err)
	}
	ramMiB := int64(memory.Totalram * uint64(memory.Unit) >> 20)
	freeGiB := int64(disk.Bavail * uint64(disk.Bsize) >> 30)
	// Reserve room for at least the starter image's 2 GiB root disk and
	// the smallest supported 1 GiB project volume within the half-disk budget.
	if ramMiB < 512 || freeGiB < 6 {
		return config, fmt.Errorf("the host needs at least 512 MiB of memory and 6 GiB free on /var/lib for the initial VM capacity")
	}
	return compute.Config{
		DataDir: "/var/lib/axv", Socket: standardSocket, SocketGroup: &socketGID,
		Capacity: compute.Capacity{
			Resources: compute.Resources{CPUs: min(32, max(1, runtime.NumCPU()/2)), MemoryMiB: min(16384, ramMiB/2), DiskGiB: min(128, freeGiB/2)},
			Machines:  4, Workers: 1,
		},
		Firecracker: &compute.FirecrackerConfig{
			Binary: "/opt/axv/bin/firecracker", Jailer: "/opt/axv/bin/jailer", Version: firecrackerVersion,
			CgroupParent: "system.slice/axv-host.service", UID: jailID, GID: jailID, Subnet: subnet.String(), DNS: "1.1.1.1",
		},
		Images: []compute.Image{},
	}, nil
}

func checkPrerequisites(ctx context.Context) error {
	version, err := inspectSystem(ctx, "systemctl", "--system", "show", "--property=Version", "--value")
	if err != nil {
		return err
	}
	var major int
	if _, err := fmt.Sscanf(string(version), "%d", &major); err != nil || major < 254 {
		return fmt.Errorf("this service requires systemd 254 or newer for cgroup delegation (found %q)", strings.TrimSpace(string(version)))
	}
	for _, device := range []string{"/dev/kvm", "/dev/net/tun"} {
		f, err := os.OpenFile(device, os.O_RDWR, 0)
		if err != nil {
			return fmt.Errorf("enable KVM and TUN/TAP on this Linux host: %w", err)
		}
		f.Close()
	}
	controllers, err := os.ReadFile("/sys/fs/cgroup/system.slice/cgroup.controllers")
	if err != nil {
		return fmt.Errorf("enable cgroup v2 on the system slice: %w", err)
	}
	for _, controller := range []string{"cpu", "memory", "pids"} {
		if !slices.Contains(strings.Fields(string(controllers)), controller) {
			return fmt.Errorf("enable the %s cgroup v2 controller on system.slice", controller)
		}
	}
	if _, err := os.Stat("/sys/fs/cgroup/system.slice/cgroup.kill"); err != nil {
		return fmt.Errorf("the host kernel must support cgroup.kill: %w", err)
	}
	forward, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(forward)) != "1" {
		return fmt.Errorf("enable IPv4 forwarding on the host (sudo sysctl -w net.ipv4.ip_forward=1), then run the installer again")
	}
	for _, name := range []string{"ip", "nft", "cp", "mkfs.ext4", "resize2fs"} {
		if _, err := systemTool(name); err != nil {
			return err
		}
	}
	return nil
}

func guestSubnet(routes []byte) (netip.Prefix, error) {
	var table []struct{ Dst string }
	if err := json.Unmarshal(routes, &table); err != nil {
		return netip.Prefix{}, fmt.Errorf("read IPv4 routes: %w", err)
	}
	var used []netip.Prefix
	for _, route := range table {
		if route.Dst == "" || route.Dst == "default" || route.Dst == "0.0.0.0/0" {
			continue
		}
		prefix, err := netip.ParsePrefix(route.Dst)
		if err != nil {
			addr, err := netip.ParseAddr(route.Dst)
			if err != nil || !addr.Is4() {
				return netip.Prefix{}, fmt.Errorf("unrecognized IPv4 route %q", route.Dst)
			}
			prefix = netip.PrefixFrom(addr, 32)
		}
		used = append(used, prefix)
	}
	// Prefer the example network, then look across the other private ranges.
	for _, network := range []string{"172.30.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "10.0.0.0/8"} {
		pool := netip.MustParsePrefix(network)
		ip := pool.Addr().As4()
		start := binary.BigEndian.Uint32(ip[:])
		for block := uint32(0); block < 1<<(22-pool.Bits()); block++ {
			binary.BigEndian.PutUint32(ip[:], start+block*1024)
			candidate := netip.PrefixFrom(netip.AddrFrom4(ip), 22)
			if !slices.ContainsFunc(used, candidate.Overlaps) {
				return candidate, nil
			}
		}
	}
	return netip.Prefix{}, fmt.Errorf("no private IPv4 /22 is free in the host routing tables")
}

type idRange struct{ first, last int }

func subordinateIDRange(entry string) (idRange, bool) {
	parts := strings.Split(entry, ":")
	if len(parts) != 3 {
		return idRange{}, false
	}
	first, firstErr := strconv.Atoi(parts[1])
	count, countErr := strconv.Atoi(parts[2])
	// Linux reserves the all-ones UID and GID. Check the upper end before
	// calculating it so the range fits both the kernel and the Go type.
	const maxID = 1<<32 - 2
	if firstErr != nil || countErr != nil || first < 0 || first > maxID || count <= 0 || count-1 > maxID-first {
		return idRange{}, false
	}
	return idRange{first, first + count - 1}, true
}

func freeJailIDs(ctx context.Context, socketGID int) (int, error) {
	used := []idRange{{socketGID, socketGID}}
	for _, db := range []string{"passwd", "group"} {
		entries, err := inspectSystem(ctx, "getent", db)
		if err != nil {
			return 0, err
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(entries)), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) < 4 {
				return 0, fmt.Errorf("invalid %s entry while selecting jail identities", db)
			}
			ids := fields[2:3]
			if db == "passwd" {
				ids = fields[2:4] // User ID and primary GID.
			}
			for _, value := range ids {
				id, err := strconv.Atoi(value)
				if err != nil {
					return 0, err
				}
				used = append(used, idRange{id, id})
			}
		}
	}
	for _, path := range []string{"/etc/subuid", "/etc/subgid"} {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			ids, ok := subordinateIDRange(line)
			if !ok {
				return 0, fmt.Errorf("invalid subordinate ID range in %s", path)
			}
			used = append(used, ids)
		}
	}
	// Running jails and containers can use IDs with no passwd or group entry.
	processes, err := filepath.Glob("/proc/[0-9]*/status")
	if err != nil {
		return 0, err
	}
	for _, path := range processes {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) || errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			key, values, _ := strings.Cut(line, ":")
			if key == "Uid" || key == "Gid" || key == "Groups" {
				for _, value := range strings.Fields(values) {
					id, err := strconv.Atoi(value)
					if err != nil {
						return 0, err
					}
					used = append(used, idRange{id, id})
				}
			}
		}
	}
	return jailIDBase(used)
}

func jailIDBase(used []idRange) (int, error) {
	// The provider assigns base+slot (1..100) and a shared GID. Stay below
	// systemd's dynamic user pool and clear of local accounts and delegations.
	for base := 60000; base+100 < 61184; base += 101 {
		if !slices.ContainsFunc(used, func(r idRange) bool { return r.first <= base+100 && r.last >= base }) {
			return base, nil
		}
	}
	return 0, fmt.Errorf("no free jail UID/GID range between 60000 and 61183; choose a range in an administrator-managed configuration")
}
