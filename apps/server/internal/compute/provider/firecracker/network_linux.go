//go:build linux

package firecracker

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"golang.org/x/sys/unix"
)

type networkState struct {
	Version     int    `json:"version"`
	Subnet      string `json:"subnet"`
	Phase       string `json:"phase"`
	Nonce       string `json:"nonce"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func (d *Driver) readNetworkState() (networkState, error) {
	var saved networkState
	path := filepath.Join(d.root, "network.json")
	info, err := os.Lstat(path)
	if err != nil {
		return saved, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return saved, fmt.Errorf("Firecracker network baseline must be a regular file of at most 64 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return saved, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&saved); err != nil {
		return saved, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return saved, fmt.Errorf("unexpected data after Firecracker network baseline")
	}
	nonce, nonceErr := hex.DecodeString(strings.TrimPrefix(saved.Nonce, "network-"))
	fingerprint, fingerprintErr := hex.DecodeString(saved.Fingerprint)
	if saved.Version != 1 || !strings.HasPrefix(saved.Nonce, "network-") || nonceErr != nil || len(nonce) != 16 || strings.ToLower(saved.Nonce) != saved.Nonce ||
		(saved.Phase != "creating" && saved.Phase != "active") ||
		fingerprintErr != nil || len(fingerprint) != 32 {
		return saved, fmt.Errorf("unsupported Firecracker network baseline %d; stop affected VMs before explicitly resetting their AXV-owned network state", saved.Version)
	}
	if saved.Subnet != d.subnet.String() {
		return saved, fmt.Errorf("host subnet changed; retain the original subnet while resources exist")
	}
	wanted, err := fingerprintRules(d.firewallObjects(saved.Nonce))
	if err != nil {
		return saved, err
	}
	if wanted != saved.Fingerprint {
		return saved, fmt.Errorf("Firecracker network baseline differs from this provider; stop affected VMs before explicitly resetting their AXV-owned network state")
	}
	return saved, nil
}

func (d *Driver) addresses(m compute.Machine) (netip.Addr, netip.Addr) {
	base := d.subnet.Addr().As4()
	n := binary.BigEndian.Uint32(base[:]) + uint32(m.Slot*4)
	var gateway, guest [4]byte
	binary.BigEndian.PutUint32(gateway[:], n+1)
	binary.BigEndian.PutUint32(guest[:], n+2)
	return netip.AddrFrom4(gateway), netip.AddrFrom4(guest)
}

func (d *Driver) GuestReachability(machine compute.Machine, address netip.Addr) guestproto.Reachability {
	result := guestproto.Reachability{From: "deployment-host", State: "unknown", Reason: "not_verified"}
	_, guest := d.addresses(machine)
	if address == guest {
		result.State, result.Reason = "unreachable", "provider_firewall"
	}
	return result
}

type link struct {
	Name  string `json:"ifname"`
	Alias string `json:"ifalias"`
}

func (d *Driver) tapSlot(name string) (int, error) {
	text := strings.TrimPrefix(name, d.prefix)
	slot, err := strconv.Atoi(text)
	if !strings.HasPrefix(name, d.prefix) || err != nil || slot < 0 || slot > 100 || fmt.Sprintf("%03d", slot) != text {
		return 0, fmt.Errorf("interface %q occupies the AXV TAP namespace with an unknown name", name)
	}
	return slot, nil
}

func (d *Driver) ownedLinks(ctx context.Context) (map[string]link, error) {
	out, err := providerutil.Command(ctx, "ip", "-j", "link", "show")
	if err != nil {
		return nil, err
	}
	var links []link
	if err = json.Unmarshal(out, &links); err != nil {
		return nil, err
	}
	owned := make(map[string]link)
	for _, item := range links {
		if !strings.HasPrefix(item.Name, d.prefix) {
			continue
		}
		if _, err = d.tapSlot(item.Name); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(item.Alias, d.owner+":") || item.Alias == d.owner+":" {
			return nil, fmt.Errorf("interface %q occupies the AXV TAP namespace with a different owner", item.Name)
		}
		owned[item.Name] = item
	}
	return owned, nil
}

// Keep the nonpersistent TAP's descriptor open until its ownership alias and
// jailer UID are established. Failure or process exit before persistence closes
// the descriptor and lets the kernel remove this invocation's new TAP.
func (d *Driver) createTAP(ctx context.Context, m compute.Machine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tun, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer tun.Close()
	request, err := unix.NewIfreq(d.tap(m))
	if err != nil {
		return err
	}
	request.SetUint16(unix.IFF_TAP | unix.IFF_NO_PI | unix.IFF_TUN_EXCL)
	if err = unix.IoctlIfreq(int(tun.Fd()), unix.TUNSETIFF, request); err != nil {
		return fmt.Errorf("create exclusive TAP: %w", err)
	}
	if err = unix.IoctlSetInt(int(tun.Fd()), unix.TUNSETOWNER, d.config.UID+m.Slot); err != nil {
		return fmt.Errorf("assign TAP jailer UID: %w", err)
	}
	if _, err = providerutil.Command(ctx, "ip", "link", "set", "dev", d.tap(m), "alias", d.owner+":"+m.ID); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = unix.IoctlSetInt(int(tun.Fd()), unix.TUNSETPERSIST, 1); err != nil {
		return fmt.Errorf("persist owned TAP: %w", err)
	}
	return nil
}

func (d *Driver) findLink(ctx context.Context, m compute.Machine) (bool, error) {
	out, err := providerutil.Command(ctx, "ip", "-j", "link", "show")
	if err != nil {
		return false, err
	}
	var links []link
	if err = json.Unmarshal(out, &links); err != nil {
		return false, err
	}
	for _, item := range links {
		if item.Name == d.tap(m) {
			if item.Alias != d.owner+":"+m.ID {
				return false, fmt.Errorf("TAP name is occupied by a resource not owned by this machine")
			}
			return true, nil
		}
	}
	return false, nil
}

func (d *Driver) ensureNetwork(ctx context.Context, m compute.Machine) error {
	d.networkMu.Lock()
	defer d.networkMu.Unlock()
	if err := d.checkRoutes(ctx); err != nil {
		return err
	}
	exists, err := d.findLink(ctx, m)
	if err != nil {
		return err
	}
	if err = d.firewall(ctx); err != nil {
		return err
	}
	if !exists {
		if err = d.createTAP(ctx, m); err != nil {
			return err
		}
	}
	gateway, _ := d.addresses(m)
	if _, err = providerutil.Command(ctx, "ip", "addr", "replace", gateway.String()+"/30", "dev", d.tap(m)); err != nil {
		return err
	}
	if err = d.setSource(ctx, m, true); err != nil {
		return err
	}
	_, err = providerutil.Command(ctx, "ip", "link", "set", "dev", d.tap(m), "up")
	return err
}

func (d *Driver) removeNetwork(ctx context.Context, m compute.Machine) error {
	d.networkMu.Lock()
	defer d.networkMu.Unlock()
	exists, err := d.findLink(ctx, m)
	if err != nil {
		return err
	}
	table, err := d.ownedFirewall(ctx)
	if err != nil {
		return err
	}
	if table {
		saved, err := d.readNetworkState()
		if err != nil {
			return err
		}
		links, err := d.ownedLinks(ctx)
		if err != nil {
			return err
		}
		if saved.Phase == "creating" {
			// A crash can leave the committed empty table before publication of
			// the active marker. No VM network exists to remove in this exact
			// state; leave both the intent and the table available for retry.
			if exists || len(links) != 0 {
				return fmt.Errorf("unfinished firewall initialization already has TAPs")
			}
			fingerprint, sources, err := d.firewallSnapshot(ctx, saved.Nonce)
			if err != nil {
				return err
			}
			if fingerprint != saved.Fingerprint || len(sources) != 0 {
				return fmt.Errorf("unfinished Firecracker firewall differs from its empty recorded intent")
			}
			return nil
		}
		if err = d.checkFirewall(ctx, saved, links, false); err != nil {
			return err
		}
	}
	if exists {
		if _, err = providerutil.Command(ctx, "ip", "link", "delete", "dev", d.tap(m)); err != nil {
			return err
		}
	}
	if !table {
		return nil
	}
	return d.setSource(ctx, m, false)
}

// Reject overlapping host routes before allocating TAP networks.
func (d *Driver) checkRoutes(ctx context.Context) error {
	links, err := d.ownedLinks(ctx)
	if err != nil {
		return err
	}
	out, err := providerutil.Command(ctx, "ip", "-j", "-4", "route", "show", "table", "all")
	if err != nil {
		return err
	}
	var routes []struct{ Dst, Dev string }
	if err = json.Unmarshal(out, &routes); err != nil {
		return err
	}
	for _, route := range routes {
		if route.Dst == "default" || route.Dst == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(route.Dst)
		if err != nil {
			if ip, e := netip.ParseAddr(route.Dst); e == nil {
				prefix = netip.PrefixFrom(ip, 32)
			} else {
				return fmt.Errorf("unrecognized host route %q", route.Dst)
			}
		}
		if _, owned := links[route.Dev]; owned {
			slot, _ := d.tapSlot(route.Dev)
			gateway, _ := d.addresses(compute.Machine{Slot: slot})
			connected := netip.PrefixFrom(gateway, 30).Masked()
			if prefix.Bits() >= connected.Bits() && connected.Contains(prefix.Addr()) {
				continue
			}
		}
		if prefix.Overlaps(d.subnet) {
			return fmt.Errorf("configured VM subnet overlaps host route %s on %s", route.Dst, route.Dev)
		}
	}
	return nil
}
