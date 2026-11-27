//go:build linux

package firecracker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
)

func (d *Driver) firewall(ctx context.Context) error {
	marker := filepath.Join(d.root, "network.json")
	saved, err := d.readNetworkState()
	missingMarker := false
	if os.IsNotExist(err) {
		missingMarker = true
	} else if err != nil {
		return err
	}
	exists, err := d.ownedFirewall(ctx)
	if err != nil {
		return err
	}
	if exists && missingMarker {
		return fmt.Errorf("owned firewall table has no network baseline marker; refusing to adopt unknown rules")
	}
	links, err := d.ownedLinks(ctx)
	if err != nil {
		return err
	}
	if missingMarker {
		if len(links) != 0 {
			return fmt.Errorf("owned TAPs have no network baseline marker; refusing to adopt unknown network state")
		}
		saved = networkState{Version: 1, Subnet: d.subnet.String(), Phase: "creating", Nonce: compute.NewID("network")}
		saved.Fingerprint, err = fingerprintRules(d.firewallObjects(saved.Nonce))
		if err != nil {
			return err
		}
		if err = jsonfile.Write(marker, saved, 0600); err != nil {
			return err
		}
	}
	if saved.Phase == "active" {
		if !exists {
			return fmt.Errorf("active Firecracker firewall table is missing; stop affected VMs before explicitly resetting their AXV-owned network state")
		}
		return d.checkFirewall(ctx, saved, links, true)
	}
	if len(links) != 0 {
		return fmt.Errorf("unfinished firewall initialization already has TAPs; refusing to replace network state")
	}
	if exists {
		fingerprint, sources, inspectErr := d.firewallSnapshot(ctx, saved.Nonce)
		if inspectErr != nil {
			return inspectErr
		}
		if len(sources) != 0 {
			return fmt.Errorf("unfinished firewall initialization has source bindings; refusing to replace unknown network state")
		}
		if fingerprint != saved.Fingerprint {
			return fmt.Errorf("unfinished Firecracker firewall rules differ from their recorded intent")
		}
	} else {
		// The same JSON objects define the installation and the fingerprint
		// recorded before I/O, making a committed creation exactly recognizable.
		objects := d.firewallObjects(saved.Nonce)
		wanted, err := fingerprintRules(objects)
		if err != nil {
			return err
		}
		if wanted != saved.Fingerprint {
			return fmt.Errorf("unfinished Firecracker firewall baseline no longer matches this provider")
		}
		var commands []map[string]any
		for _, object := range objects {
			action := "add"
			if _, table := object["table"]; table {
				// Reject a table created by another service after the ownership
				// check. The whole nft transaction must fail without merging.
				action = "create"
			}
			commands = append(commands, map[string]any{action: object})
		}
		body, err := json.Marshal(map[string]any{"nftables": commands})
		if err != nil {
			return err
		}
		if err = nftInput(ctx, "create owned firewall table", string(body), "-j", "-f", "-"); err != nil {
			return err
		}
		fingerprint, _, err := d.firewallSnapshot(ctx, saved.Nonce)
		if err != nil {
			return err
		}
		if fingerprint != saved.Fingerprint {
			return fmt.Errorf("installed Firecracker firewall differs from its recorded intent")
		}
	}
	saved.Phase = "active"
	return jsonfile.Write(marker, saved, 0600)
}

type nftObject = map[string]map[string]any

func (d *Driver) firewallObjects(nonce string) []nftObject {
	expr := func(key string, value any) map[string]any { return map[string]any{key: value} }
	meta := func(key string) map[string]any { return expr("meta", expr("key", key)) }
	payload := func(key string) map[string]any {
		return expr("payload", map[string]any{"protocol": "ip", "field": key})
	}
	match := func(op string, left, right any) map[string]any {
		return expr("match", map[string]any{"op": op, "left": left, "right": right})
	}
	prefix := func(value netip.Prefix) map[string]any {
		return expr("prefix", map[string]any{"addr": value.Addr().String(), "len": value.Bits()})
	}
	var denied, exceptions []any
	policy := compute.NonPublicIPv4Prefixes()
	sort.Slice(policy, func(i, j int) bool {
		if policy[i].Addr() == policy[j].Addr() {
			return policy[i].Bits() < policy[j].Bits()
		}
		return policy[i].Addr().Less(policy[j].Addr())
	})
	// nft canonicalizes adjacent address intervals. Merge exactly aligned
	// siblings before pinning the JSON, keeping the same address union.
	var merged []netip.Prefix
	for _, value := range policy {
		if len(merged) != 0 && merged[len(merged)-1].Contains(value.Addr()) {
			continue
		}
		merged = append(merged, value)
		for len(merged) >= 2 {
			a, b := merged[len(merged)-2], merged[len(merged)-1]
			if a.Bits() != b.Bits() || a.Bits() == 0 {
				break
			}
			parent := netip.PrefixFrom(a.Addr(), a.Bits()-1).Masked()
			if !parent.Contains(b.Addr()) {
				break
			}
			merged = append(merged[:len(merged)-2], parent)
		}
	}
	for _, value := range merged {
		denied = append(denied, prefix(value))
	}
	for _, value := range compute.PublicIPv4Exceptions() {
		exceptions = append(exceptions, value.String())
	}
	objects := []nftObject{
		{"table": {"family": "inet", "name": d.prefix, "comment": d.owner}},
		{"set": {"family": "inet", "table": d.prefix, "name": "sources", "type": []string{"ifname", "ipv4_addr"}}},
	}
	for _, chain := range []struct {
		name, kind string
		priority   int
	}{{"input", "filter", -5}, {"forward", "filter", -5}, {"postrouting", "nat", 100}} {
		object := map[string]any{"family": "inet", "table": d.prefix, "name": chain.name, "type": chain.kind, "hook": chain.name, "prio": chain.priority, "policy": "accept"}
		if chain.name == "input" {
			object["comment"] = "axv-network-v1:" + nonce
		}
		objects = append(objects, nftObject{"chain": object})
	}
	rule := func(chain string, expressions ...map[string]any) {
		objects = append(objects, nftObject{"rule": {"family": "inet", "table": d.prefix, "chain": chain, "expr": expressions}})
	}
	in := match("==", meta("iifname"), d.prefix+"*")
	out := match("==", meta("oifname"), d.prefix+"*")
	drop, accept := expr("drop", nil), expr("accept", nil)
	rule("input", in, drop)
	rule("forward", in, out, drop)
	rule("forward", in, match("==", meta("nfproto"), "ipv6"), drop)
	rule("forward", in, match("!=", expr("concat", []any{meta("iifname"), payload("saddr")}), "@sources"), drop)
	rule("forward", in, match("==", payload("daddr"), expr("set", denied)), match("!=", payload("daddr"), expr("set", exceptions)), drop)
	rule("forward", in, match("in", expr("ct", expr("key", "state")), []string{"established", "related", "new"}), accept)
	rule("forward", out, match("in", expr("ct", expr("key", "state")), []string{"established", "related"}), accept)
	rule("forward", out, drop)
	rule("postrouting", in, match("==", payload("saddr"), prefix(d.subnet)), match("!=", meta("oifname"), d.prefix+"*"), expr("masquerade", nil))
	return objects
}

func fingerprintRules(rows []nftObject) (string, error) {
	key := func(row nftObject) string {
		for kind, object := range row {
			switch kind {
			case "table":
				return "0"
			case "set":
				return "1:" + fmt.Sprint(object["name"])
			case "chain":
				return "2:" + fmt.Sprint(object["name"])
			case "rule":
				return "3:" + fmt.Sprint(object["chain"])
			}
		}
		return "4"
	}
	// Object declaration order has no effect; order within each chain does.
	sort.SliceStable(rows, func(i, j int) bool { return key(rows[i]) < key(rows[j]) })
	canonical, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(canonical)), nil
}

// Snapshot static rules independently of kernel handles and dynamic bindings.
// Counters and all other objects remain part of the baseline, so changes fail
// visibly instead of silently weakening the active firewall.
func (d *Driver) firewallSnapshot(ctx context.Context, nonce string) (string, [][2]string, error) {
	out, err := providerutil.Command(ctx, "nft", "-j", "list", "table", "inet", d.prefix)
	if err != nil {
		return "", nil, err
	}
	var parsed struct {
		NFTables []map[string]map[string]any `json:"nftables"`
	}
	if err = json.Unmarshal(out, &parsed); err != nil {
		return "", nil, err
	}
	var rows []map[string]map[string]any
	var sources [][2]string
	sets := 0
	chains := make(map[string]bool)
	rules := make(map[string]int)
	for _, row := range parsed.NFTables {
		if _, ok := row["metainfo"]; ok {
			continue
		}
		for kind, object := range row {
			delete(object, "handle")
			switch kind {
			case "table":
				if object["family"] != "inet" || object["name"] != d.prefix || object["comment"] != d.owner {
					return "", nil, fmt.Errorf("firewall table identity changed")
				}
			case "set":
				encoded, _ := json.Marshal(object["type"])
				if object["name"] != "sources" || string(encoded) != `["ifname","ipv4_addr"]` {
					return "", nil, fmt.Errorf("unknown Firecracker firewall set baseline")
				}
				sets++
				if elements, ok := object["elem"]; ok {
					encoded, _ = json.Marshal(elements)
					var values []struct{ Concat []string }
					if err = json.Unmarshal(encoded, &values); err != nil {
						return "", nil, fmt.Errorf("invalid TAP source bindings: %w", err)
					}
					for _, value := range values {
						if len(value.Concat) != 2 {
							return "", nil, fmt.Errorf("invalid TAP source binding")
						}
						sources = append(sources, [2]string{value.Concat[0], value.Concat[1]})
					}
					delete(object, "elem")
				}
			case "chain":
				name, _ := object["name"].(string)
				hook, kind, priority := name, "filter", float64(-5)
				if name == "postrouting" {
					kind, priority = "nat", 100
				}
				if (name != "input" && name != "forward" && name != "postrouting") || object["hook"] != hook || object["type"] != kind || object["prio"] != priority || object["policy"] != "accept" {
					return "", nil, fmt.Errorf("unknown Firecracker firewall chain baseline")
				}
				if name == "input" && object["comment"] != "axv-network-v1:"+nonce {
					return "", nil, fmt.Errorf("Firecracker firewall initialization identity changed")
				}
				chains[name] = true
			case "rule":
				name, _ := object["chain"].(string)
				rules[name]++
			default:
				return "", nil, fmt.Errorf("unknown Firecracker firewall object %q", kind)
			}
		}
		rows = append(rows, row)
	}
	if sets != 1 || len(chains) != 3 || len(rules) != 3 || rules["input"] != 1 || rules["forward"] != 7 || rules["postrouting"] != 1 {
		return "", nil, fmt.Errorf("incomplete Firecracker firewall baseline")
	}
	fingerprint, err := fingerprintRules(rows)
	return fingerprint, sources, err
}

func (d *Driver) checkFirewall(ctx context.Context, saved networkState, links map[string]link, restore bool) error {
	fingerprint, sources, err := d.firewallSnapshot(ctx, saved.Nonce)
	if err != nil {
		return err
	}
	if saved.Fingerprint == "" || fingerprint != saved.Fingerprint {
		return fmt.Errorf("active Firecracker firewall rules changed; refusing to replace unknown rules")
	}
	bound := make(map[string]bool)
	for _, source := range sources {
		slot, err := d.tapSlot(source[0])
		if err != nil {
			return err
		}
		_, address := d.addresses(compute.Machine{Slot: slot})
		if source[1] != address.String() {
			return fmt.Errorf("TAP %s has an unexpected source binding", source[0])
		}
		bound[source[0]] = true
	}
	// A successful TAP deletion can precede binding cleanup. Retain these stale
	// exact tuples for removeNetwork's retry, but repair missing bindings for
	// every verified owned TAP after a lost dynamic-set update.
	var add strings.Builder
	for name := range links {
		if restore && !bound[name] {
			slot, _ := d.tapSlot(name)
			_, address := d.addresses(compute.Machine{Slot: slot})
			fmt.Fprintf(&add, "add element inet %s sources { %q . %s }\n", d.prefix, name, address)
		}
	}
	if add.Len() != 0 {
		return nftRules(ctx, "restore owned TAP source bindings", add.String())
	}
	return nil
}

func (d *Driver) ownedFirewall(ctx context.Context) (bool, error) {
	out, err := providerutil.Command(ctx, "nft", "-j", "list", "tables")
	if err != nil {
		return false, err
	}
	var tables struct {
		NFTables []struct {
			Table struct{ Family, Name, Comment string } `json:"table"`
		} `json:"nftables"`
	}
	if err = json.Unmarshal(out, &tables); err != nil {
		return false, err
	}
	for _, row := range tables.NFTables {
		if row.Table.Family == "inet" && row.Table.Name == d.prefix {
			if row.Table.Comment != d.owner {
				return false, fmt.Errorf("firewall table name is occupied by a different owner")
			}
			return true, nil
		}
	}
	return false, nil
}

func (d *Driver) setSource(ctx context.Context, m compute.Machine, enabled bool) error {
	_, guest := d.addresses(m)
	element := fmt.Sprintf("inet %s sources { %q . %s }", d.prefix, d.tap(m), guest)
	// add is idempotent. Adding and deleting in one atomic transaction also
	// makes removal idempotent without requiring newer nft destroy support.
	rules := "add element " + element + "\n"
	if !enabled {
		rules += "delete element " + element + "\n"
	}
	return nftRules(ctx, "update owned TAP source binding", rules)
}

func nftRules(ctx context.Context, operation, rules string) error {
	return nftInput(ctx, operation, rules, "-f", "-")
}

func nftInput(ctx context.Context, operation, rules string, args ...string) error {
	cmd := exec.CommandContext(ctx, "nft", args...)
	cmd.Stdin = strings.NewReader(rules)
	var diagnostic limitedbuffer.Buffer
	diagnostic.Limit = 32 << 10
	cmd.Stdout, cmd.Stderr = &diagnostic, &diagnostic
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", operation, err, diagnostic.Data)
	}
	return nil
}
