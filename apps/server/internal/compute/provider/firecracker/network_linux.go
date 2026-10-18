//go:build linux

package firecracker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
)

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

func (d *Driver) firewall(ctx context.Context) error {
	marker := filepath.Join(d.root, "network.json")
	var saved struct {
		Subnet string `json:"subnet"`
	}
	if err := jsonfile.Read(marker, &saved); err == nil {
		if saved.Subnet != d.subnet.String() {
			return fmt.Errorf("host subnet changed; retain the original subnet while resources exist")
		}
	} else if os.IsNotExist(err) {
		if err = jsonfile.Write(marker, map[string]string{"subnet": d.subnet.String()}, 0600); err != nil {
			return err
		}
	} else {
		return err
	}
	out, err := providerutil.Command(ctx, "nft", "-j", "list", "tables")
	if err != nil {
		return err
	}
	var tables struct {
		NFTables []struct {
			Table struct{ Family, Name, Comment string } `json:"table"`
		} `json:"nftables"`
	}
	if err = json.Unmarshal(out, &tables); err != nil {
		return err
	}
	for _, row := range tables.NFTables {
		if row.Table.Family == "inet" && row.Table.Name == d.prefix {
			if row.Table.Comment != d.owner {
				return fmt.Errorf("firewall table name is occupied by a different owner")
			}
			return nil
		}
	}
	// Rules apply only to this namespace's TAPs. Never flush another service's rules.
	rules := fmt.Sprintf(`table inet %s {
 comment "%s";
 chain input { type filter hook input priority -5; policy accept; iifname "%s*" drop; }
 chain forward { type filter hook forward priority -5; policy accept;
  iifname "%s*" oifname "%s*" drop;
  iifname "%s*" meta nfproto ipv6 drop;
  iifname "%s*" ip daddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16, 224.0.0.0/4 } drop;
  iifname "%s*" ct state new,established,related accept;
  oifname "%s*" ct state established,related accept;
  oifname "%s*" drop;
 }
 chain snat { type nat hook postrouting priority srcnat; policy accept;
  ip saddr %s oifname != "%s*" masquerade;
 }
}
`, d.prefix, d.owner, d.prefix, d.prefix, d.prefix, d.prefix, d.prefix, d.prefix, d.prefix, d.prefix, d.subnet.String(), d.prefix)
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	var diagnostic limitedbuffer.Buffer
	diagnostic.Limit = 32 << 10
	cmd.Stdout, cmd.Stderr = &diagnostic, &diagnostic
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("create owned firewall table: %w: %s", err, diagnostic.Data)
	}
	return nil
}

type link struct {
	Name  string `json:"ifname"`
	Alias string `json:"ifalias"`
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
	if err := d.firewall(ctx); err != nil {
		return err
	}
	exists, err := d.findLink(ctx, m)
	if err != nil {
		return err
	}
	if !exists {
		if _, err = providerutil.Command(ctx, "ip", "tuntap", "add", "dev", d.tap(m), "mode", "tap", "user", fmt.Sprint(d.config.UID+m.Slot)); err != nil {
			return err
		}
		if _, err = providerutil.Command(ctx, "ip", "link", "set", "dev", d.tap(m), "alias", d.owner+":"+m.ID); err != nil {
			return err
		}
	}
	gateway, _ := d.addresses(m)
	if _, err = providerutil.Command(ctx, "ip", "addr", "replace", gateway.String()+"/30", "dev", d.tap(m)); err != nil {
		return err
	}
	_, err = providerutil.Command(ctx, "ip", "link", "set", "dev", d.tap(m), "up")
	return err
}

func (d *Driver) removeNetwork(ctx context.Context, m compute.Machine) error {
	d.networkMu.Lock()
	defer d.networkMu.Unlock()
	exists, err := d.findLink(ctx, m)
	if err != nil || !exists {
		return err
	}
	_, err = providerutil.Command(ctx, "ip", "link", "delete", "dev", d.tap(m))
	return err
}

// Reject overlapping host routes before allocating TAP networks.
func (d *Driver) checkRoutes(ctx context.Context) error {
	out, err := providerutil.Command(ctx, "ip", "-j", "-4", "route", "show", "table", "all")
	if err != nil {
		return err
	}
	var routes []struct{ Dst, Dev string }
	if err = json.Unmarshal(out, &routes); err != nil {
		return err
	}
	for _, route := range routes {
		if route.Dst == "default" || route.Dst == "" || strings.HasPrefix(route.Dev, d.prefix) {
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
		if prefix.Overlaps(d.subnet) {
			return fmt.Errorf("configured VM subnet overlaps host route %s on %s", route.Dst, route.Dev)
		}
	}
	return nil
}
