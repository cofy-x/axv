//go:build linux

package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute/hostinstall"
)

func checkPlatform(ctx context.Context, r *Report, _ string, version string) {
	// 1. KVM and TUN devices
	for _, dev := range []struct {
		id, path, label string
	}{
		{"linux_kvm", "/dev/kvm", "KVM virtualization (/dev/kvm)"},
		{"linux_tun", "/dev/net/tun", "TUN/TAP networking (/dev/net/tun)"},
	} {
		if _, err := os.Stat(dev.path); err != nil {
			r.add(Check{
				ID:       dev.id,
				Category: "vm",
				Label:    dev.label,
				Status:   StatusFail,
				Detail:   fmt.Sprintf("Missing or inaccessible: %v", err),
				Fix:      "Enable hardware virtualization (KVM) and load tun kernel module.",
			})
		} else {
			r.add(Check{
				ID:       dev.id,
				Category: "vm",
				Label:    dev.label,
				Status:   StatusOK,
				Detail:   "Present.",
			})
		}
	}

	// 2. cgroup v2
	if controllers, err := os.ReadFile("/sys/fs/cgroup/system.slice/cgroup.controllers"); err != nil {
		r.add(Check{
			ID:       "linux_cgroup",
			Category: "vm",
			Label:    "cgroup v2 delegation",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Could not read /sys/fs/cgroup/system.slice/cgroup.controllers: %v", err),
		})
	} else {
		fields := strings.Fields(string(controllers))
		var missing []string
		for _, req := range []string{"cpu", "memory", "pids"} {
			if !slices.Contains(fields, req) {
				missing = append(missing, req)
			}
		}
		if _, err := os.Stat("/sys/fs/cgroup/system.slice/cgroup.kill"); err != nil {
			missing = append(missing, "cgroup.kill")
		}
		if len(missing) > 0 {
			r.add(Check{
				ID:       "linux_cgroup",
				Category: "vm",
				Label:    "cgroup v2 delegation",
				Status:   StatusWarn,
				Detail:   fmt.Sprintf("Missing cgroup v2 features on system.slice: %s.", strings.Join(missing, ", ")),
			})
		} else {
			r.add(Check{
				ID:       "linux_cgroup",
				Category: "vm",
				Label:    "cgroup v2 delegation",
				Status:   StatusOK,
				Detail:   "cpu, memory, pids and cgroup.kill available.",
			})
		}
	}

	// 3. IPv4 forwarding
	if forward, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward"); err == nil {
		if strings.TrimSpace(string(forward)) == "1" {
			r.add(Check{
				ID:       "linux_ip_forward",
				Category: "vm",
				Label:    "IPv4 forwarding",
				Status:   StatusOK,
				Detail:   "net.ipv4.ip_forward = 1.",
			})
		} else {
			r.add(Check{
				ID:       "linux_ip_forward",
				Category: "vm",
				Label:    "IPv4 forwarding",
				Status:   StatusWarn,
				Detail:   "net.ipv4.ip_forward is 0; VMs will not have outbound network access.",
				Fix:      "sudo axv host install-service --apply-sysctl   (or: sudo sysctl -w net.ipv4.ip_forward=1)",
			})
		}
	}

	// 4. Required host tools for Firecracker
	var missingTools []string
	for _, tool := range []string{"ip", "nft", "cp", "mkfs.ext4", "resize2fs"} {
		found := false
		for _, dir := range filepath.SplitList("/usr/sbin:/usr/bin:/sbin:/bin") {
			if info, err := os.Stat(filepath.Join(dir, tool)); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				found = true
				break
			}
		}
		if !found {
			missingTools = append(missingTools, tool)
		}
	}
	if len(missingTools) > 0 {
		r.add(Check{
			ID:       "linux_vm_tools",
			Category: "vm",
			Label:    "Firecracker host tools",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Missing tools: %s.", strings.Join(missingTools, ", ")),
			Fix:      "Install iproute2, nftables and e2fsprogs.",
		})
	} else {
		r.add(Check{
			ID:       "linux_vm_tools",
			Category: "vm",
			Label:    "Firecracker host tools",
			Status:   StatusOK,
			Detail:   "ip, nft, cp, mkfs.ext4, resize2fs present.",
		})
	}

	// 5. Linux VM host service (axv-host.service)
	sys := hostinstall.Status(ctx, version)
	switch {
	case !sys.Installed:
		r.add(Check{
			ID:       "linux_vm_service",
			Category: "vm",
			Label:    "Linux VM service (" + hostinstall.UnitName + ")",
			Status:   StatusInfo,
			Detail:   "Not installed.",
			Fix:      "sudo axv host install-service --apply-sysctl",
		})
	case !sys.Active:
		r.add(Check{
			ID:       "linux_vm_service",
			Category: "vm",
			Label:    "Linux VM service (" + hostinstall.UnitName + ")",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Installed but %s.", sys.State),
			Fix:      "sudo axv daemon start --only vm",
		})
	case !sys.SocketReachable:
		r.add(Check{
			ID:       "linux_vm_service",
			Category: "vm",
			Label:    "Linux VM service (" + hostinstall.UnitName + ")",
			Status:   StatusFail,
			Detail:   fmt.Sprintf("Service is active, but socket %s is unreachable by the current user.", sys.Socket),
			Fix:      "Check socket group membership or run `axv daemon logs --vm`.",
		})
	case sys.VersionDrift:
		r.add(Check{
			ID:       "linux_vm_service",
			Category: "vm",
			Label:    "Linux VM service (" + hostinstall.UnitName + ")",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Running AXV %s while this CLI is %s.", sys.InstalledVersion, sys.CurrentVersion),
			Fix:      "sudo axv daemon upgrade",
		})
	case !sys.Enabled:
		r.add(Check{
			ID:       "linux_vm_service",
			Category: "vm",
			Label:    "Linux VM service (" + hostinstall.UnitName + ")",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Running on %s, but boot autostart is disabled.", sys.Socket),
			Fix:      "sudo systemctl enable " + hostinstall.UnitName,
		})
	default:
		r.add(Check{
			ID:       "linux_vm_service",
			Category: "vm",
			Label:    "Linux VM service (" + hostinstall.UnitName + ")",
			Status:   StatusOK,
			Detail:   fmt.Sprintf("Active and enabled on %s (version %s).", sys.Socket, sys.InstalledVersion),
		})
	}

	// 6. Docker host firewall coexistence check
	if _, err := os.Stat("/sys/class/net/docker0"); err == nil {
		detail := "Docker bridge detected (docker0). Docker's iptables FORWARD policy can drop Firecracker guest outbound packets unless allowed in DOCKER-USER."
		fix := "Allow your AXV guest subnet in DOCKER-USER; see docs/vm-platform.md#host-firewall-integration."
		if subnet, ok := hostinstall.HostNetworkInfo(); ok {
			detail = fmt.Sprintf("Docker bridge detected (docker0); Firecracker guest subnet is %s.", subnet)
			fix = fmt.Sprintf("sudo iptables -I DOCKER-USER -s %s -j ACCEPT && sudo iptables -I DOCKER-USER -d %s -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT", subnet, subnet)
		}
		r.add(Check{
			ID:       "linux_docker_firewall",
			Category: "vm",
			Label:    "Docker firewall coexistence",
			Status:   StatusInfo,
			Detail:   detail,
			Fix:      fix,
		})
	}
}
