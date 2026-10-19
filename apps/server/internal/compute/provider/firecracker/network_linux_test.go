//go:build linux

package firecracker

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

func TestFirewallRefusesUnknownNetworkBaseline(t *testing.T) {
	for _, version := range []int{0, 1, 2} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			d := &Driver{root: t.TempDir(), owner: "host-test", prefix: "axvtest", subnet: netip.MustParsePrefix("172.30.0.0/22")}
			marker := filepath.Join(d.root, "network.json")
			if err := jsonfile.Write(marker, map[string]any{"version": version, "subnet": d.subnet.String()}, 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			if err = d.firewall(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported Firecracker network baseline") {
				t.Fatalf("unknown baseline accepted: %v", err)
			}
			if after, err := os.ReadFile(marker); err != nil || string(after) != string(before) {
				t.Fatalf("unknown baseline was changed: %s %v", after, err)
			}
		})
	}
}

func TestFirewallDoesNotAdoptExistingTableWithoutBaseline(t *testing.T) {
	d := &Driver{root: t.TempDir(), owner: "host-test", prefix: "axvtest", subnet: netip.MustParsePrefix("172.30.0.0/22")}
	tools := t.TempDir()
	script := `#!/bin/sh
if [ "$*" = '-j list tables' ]; then
  printf '%s\n' '{"nftables":[{"table":{"family":"inet","name":"axvtest","comment":"host-test"}}]}'
else
  echo 'unexpected firewall mutation' >&2
  exit 1
fi
`
	if err := os.WriteFile(filepath.Join(tools, "nft"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	if err := d.firewall(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing to adopt unknown rules") {
		t.Fatalf("existing unversioned rules were adopted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.root, "network.json")); !os.IsNotExist(err) {
		t.Fatalf("existing unversioned rules acquired a current baseline: %v", err)
	}
}

// This check uses real kernel forwarding and nftables in private network
// namespaces. It never changes the invoking host's routes, sysctls or firewall.
func TestNativeFirewallIPv4Egress(t *testing.T) {
	if os.Getenv("AXV_TEST_FIRECRACKER_NETWORK") != "1" {
		t.Skip("set AXV_TEST_FIRECRACKER_NETWORK=1 to verify real isolated network forwarding")
	}
	if os.Geteuid() != 0 {
		t.Fatal("native network verification requires root")
	}
	for _, tool := range []string{"unshare", "nsenter", "ip", "nft", "ping", "sleep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("AXV_FIRECRACKER_NETWORK_NAMESPACE") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		parentNamespace, err := os.Open("/proc/self/ns/net")
		if err != nil {
			t.Fatal(err)
		}
		defer parentNamespace.Close()
		cmd := exec.CommandContext(ctx, "unshare", "--net", binary, "-test.run=^TestNativeFirewallIPv4Egress$", "-test.v")
		cmd.Env = append(os.Environ(), "AXV_FIRECRACKER_NETWORK_NAMESPACE=1")
		cmd.ExtraFiles = []*os.File{parentNamespace}
		out, err := cmd.CombinedOutput()
		t.Logf("private network verification:\n%s", out)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	// Require proof that unshare succeeded before changing any network state.
	// The private parent namespace descriptor is never supplied by ordinary
	// test invocations, even when they inherit the helper environment flag.
	parentNamespace, err := os.Readlink("/proc/self/fd/3")
	currentNamespace, currentErr := os.Readlink("/proc/self/ns/net")
	if err != nil || currentErr != nil || !strings.HasPrefix(parentNamespace, "net:[") || currentNamespace == parentNamespace {
		t.Fatalf("refusing to change a network namespace without isolation proof: parent=%q current=%q errors=%v/%v", parentNamespace, currentNamespace, err, currentErr)
	}
	run := func(name string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v: %s", name, args, err, out)
		}
		return out
	}
	// Fail one real command boundary without replacing the kernel resources or
	// the remaining provider commands. Every wrapper stays under this test root.
	failNFT := func() func() {
		t.Helper()
		binary, err := exec.LookPath("nft")
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		body := "#!/bin/sh\nfor arg do if [ \"$arg\" = '-f' ]; then echo 'injected nft write failure' >&2; exit 42; fi; done\nexec '" + binary + "' \"$@\"\n"
		if err = os.WriteFile(filepath.Join(dir, "nft"), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
		original := os.Getenv("PATH")
		t.Setenv("PATH", dir+string(os.PathListSeparator)+original)
		return func() { t.Setenv("PATH", original) }
	}
	interrupted := &Driver{root: t.TempDir(), owner: "host-interrupted", prefix: "axvfail", subnet: netip.MustParsePrefix("172.29.0.0/22")}
	restore := failNFT()
	if err := interrupted.firewall(context.Background()); err == nil {
		t.Fatal("injected nft creation failure was hidden")
	}
	restore()
	intent, err := interrupted.readNetworkState()
	if err != nil || intent.Phase != "creating" {
		t.Fatalf("failed creation lost retry intent: %+v %v", intent, err)
	}
	if err = interrupted.firewall(context.Background()); err != nil {
		t.Logf("installed nft state: %s", run("nft", "-j", "list", "table", "inet", interrupted.prefix))
		t.Fatalf("failed nft creation could not retry: %v", err)
	}
	run("nft", "delete", "table", "inet", interrupted.prefix)
	// Simulate a foreign table appearing between list and commit. Exclusive
	// creation must abort the whole transaction, retaining every foreign byte.
	collision := &Driver{root: t.TempDir(), owner: "host-collision", prefix: "axvcollision", subnet: netip.MustParsePrefix("172.29.0.0/22")}
	run("nft", `table inet axvcollision { comment "foreign-owner"; }`)
	foreignTable := string(run("nft", "-j", "list", "table", "inet", collision.prefix))
	nftBinary, err := exec.LookPath("nft")
	if err != nil {
		t.Fatal(err)
	}
	listTools := t.TempDir()
	listScript := "#!/bin/sh\nif [ \"$*\" = '-j list tables' ]; then printf '%s\\n' '{\"nftables\":[]}'; exit 0; fi\nexec '" + nftBinary + "' \"$@\"\n"
	if err = os.WriteFile(filepath.Join(listTools, "nft"), []byte(listScript), 0700); err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", listTools+string(os.PathListSeparator)+originalPath)
	if err = collision.firewall(context.Background()); err == nil {
		t.Fatal("firewall creation merged into a concurrently occupied table")
	}
	t.Setenv("PATH", originalPath)
	if after := string(run("nft", "-j", "list", "table", "inet", collision.prefix)); after != foreignTable {
		t.Fatal("failed exclusive creation changed a foreign table")
	}
	run("nft", "delete", "table", "inet", collision.prefix)
	d := &Driver{root: t.TempDir(), owner: "host-test", prefix: "axvtest", subnet: netip.MustParsePrefix("172.30.0.0/22"), config: compute.FirecrackerConfig{UID: 60000}}
	if err := d.firewall(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Reproduce a crash after nft committed but before the active marker was
	// published. Recovery can replay only this nonce-matched empty intent.
	initial, err := d.readNetworkState()
	if err != nil {
		t.Fatal(err)
	}
	initial.Phase = "creating"
	if err = jsonfile.Write(filepath.Join(d.root, "network.json"), initial, 0600); err != nil {
		t.Fatal(err)
	}
	intentMarker, err := os.ReadFile(filepath.Join(d.root, "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	emptyIntent := string(run("nft", "-j", "list", "table", "inet", d.prefix))
	neverPrepared := compute.Machine{ID: "vm-never-prepared", Slot: 2}
	if err = d.removeNetwork(context.Background(), neverPrepared); err != nil {
		t.Fatalf("committed initialization blocked deletion of an unprepared VM: %v", err)
	}
	if after := string(run("nft", "-j", "list", "table", "inet", d.prefix)); after != emptyIntent {
		t.Fatal("unprepared VM deletion changed its unfinished firewall")
	}
	if after, err := os.ReadFile(filepath.Join(d.root, "network.json")); err != nil || string(after) != string(intentMarker) {
		t.Fatalf("unprepared VM deletion changed its creation intent: %v", err)
	}
	run("nft", "add", "element", "inet", d.prefix, "sources", `{ "axvtest002" . 172.30.0.10 }`)
	if err = d.removeNetwork(context.Background(), neverPrepared); err == nil {
		t.Fatal("unfinished firewall with a source binding allowed VM deletion")
	}
	run("nft", "delete", "element", "inet", d.prefix, "sources", `{ "axvtest002" . 172.30.0.10 }`)
	run("nft", "flush", "chain", "inet", d.prefix, "input")
	run("nft", "add", "rule", "inet", d.prefix, "input", "iifname", "axvtest*", "accept")
	modifiedIntent := string(run("nft", "-j", "list", "table", "inet", d.prefix))
	if err = d.firewall(context.Background()); err == nil {
		t.Fatal("modified initialization rules were accepted")
	}
	if err = d.removeNetwork(context.Background(), neverPrepared); err == nil {
		t.Fatal("modified initialization rules allowed VM deletion")
	}
	if after := string(run("nft", "-j", "list", "table", "inet", d.prefix)); after != modifiedIntent {
		t.Fatal("modified initialization rules were replaced")
	}
	run("nft", "flush", "chain", "inet", d.prefix, "input")
	run("nft", "add", "rule", "inet", d.prefix, "input", "iifname", "axvtest*", "drop")
	if err = d.firewall(context.Background()); err != nil {
		t.Fatalf("committed initialization could not recover: %v", err)
	}
	// A failed ownership alias never leaves a persistent, unowned TAP behind.
	ip, err := exec.LookPath("ip")
	if err != nil {
		t.Fatal(err)
	}
	failedAlias := compute.Machine{ID: "vm-failed-alias", Slot: 2}
	aliasTools := t.TempDir()
	if err = os.WriteFile(filepath.Join(aliasTools, "ip"), []byte("#!/bin/sh\necho 'injected alias failure' >&2\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	pathBeforeAlias := os.Getenv("PATH")
	t.Setenv("PATH", aliasTools+string(os.PathListSeparator)+pathBeforeAlias)
	if err = d.createTAP(context.Background(), failedAlias); err == nil {
		t.Fatal("injected TAP alias failure was hidden")
	}
	t.Setenv("PATH", pathBeforeAlias)
	if exists, err := d.findLink(context.Background(), failedAlias); err != nil || exists {
		t.Fatalf("failed alias left a TAP behind: exists=%t error=%v", exists, err)
	}
	// Exclusive creation cannot attach to a foreign persistent TAP in a race.
	run(ip, "tuntap", "add", "dev", d.tap(failedAlias), "mode", "tap")
	run(ip, "link", "set", "dev", d.tap(failedAlias), "alias", "foreign-owner")
	if err = d.createTAP(context.Background(), failedAlias); err == nil {
		t.Fatal("exclusive TAP creation adopted an existing interface")
	}
	foreign := string(run(ip, "-j", "link", "show", "dev", d.tap(failedAlias)))
	if !strings.Contains(foreign, "foreign-owner") {
		t.Fatalf("exclusive creation changed foreign TAP ownership: %s", foreign)
	}
	beforeForeign := string(run("nft", "-j", "list", "table", "inet", d.prefix))
	if err = d.ensureNetwork(context.Background(), failedAlias); err == nil {
		t.Fatal("network preparation accepted a foreign interface in its namespace")
	}
	if after := string(run("nft", "-j", "list", "table", "inet", d.prefix)); after != beforeForeign {
		t.Fatal("foreign TAP detection changed the firewall")
	}
	run(ip, "link", "delete", "dev", d.tap(failedAlias))
	// Exercise actual persistent TAP creation and a retry after source binding
	// failure, before the newly owned interface has been brought up.
	partial := compute.Machine{ID: "vm-partial", Slot: 3}
	if err = d.createTAP(context.Background(), partial); err != nil {
		t.Fatal(err)
	}
	restore = failNFT()
	if err = d.ensureNetwork(context.Background(), partial); err == nil {
		t.Fatal("injected source-binding failure was hidden")
	}
	restore()
	if err = d.ensureNetwork(context.Background(), partial); err != nil {
		t.Fatalf("partially prepared TAP could not resume: %v", err)
	}
	if err = d.removeNetwork(context.Background(), partial); err != nil {
		t.Fatal(err)
	}
	endpoint := func() string {
		t.Helper()
		cmd := exec.Command("unshare", "--net", "sleep", "120")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		pid := strconv.Itoa(cmd.Process.Pid)
		parent, err := os.Readlink("/proc/self/ns/net")
		if err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if ns, err := os.Readlink("/proc/" + pid + "/ns/net"); err == nil && ns != parent {
				return pid
			}
		}
		t.Fatal("endpoint did not enter its private network namespace")
		return ""
	}
	guest, remote := endpoint(), endpoint()
	ns := func(pid, command string, args ...string) []byte {
		t.Helper()
		return run("nsenter", append([]string{"--target", pid, "--net", command}, args...)...)
	}
	connect := func(local, peer, pid, localAddress, peerAddress string) {
		t.Helper()
		run("ip", "link", "add", local, "type", "veth", "peer", "name", peer)
		run("ip", "link", "set", peer, "netns", pid)
		run("ip", "link", "set", local, "addrgenmode", "none")
		ns(pid, "ip", "link", "set", peer, "addrgenmode", "none")
		run("ip", "addr", "add", localAddress, "dev", local)
		run("ip", "link", "set", local, "up")
		ns(pid, "ip", "addr", "add", peerAddress, "dev", peer)
		ns(pid, "ip", "link", "set", peer, "up")
		ns(pid, "ip", "link", "set", "lo", "up")
	}
	connect("guestwire", "guest0", guest, "172.30.0.1/30", "172.30.0.2/30")
	connect("wan0", "remote0", remote, "8.8.8.1/24", "8.8.8.2/24")
	connect("otherwire", "other0", remote, "172.30.0.5/30", "172.30.0.6/30")
	for _, address := range []struct{ pid, device, value string }{
		{"", "guestwire", "2001:db8:1::1/64"},
		{guest, "guest0", "2001:db8:1::2/64"},
		{"", "wan0", "2001:db8:2::1/64"},
		{remote, "remote0", "2001:db8:2::2/64"},
	} {
		args := []string{"-6", "addr", "add", address.value, "dev", address.device, "nodad"}
		if address.pid == "" {
			run("ip", args...)
		} else {
			ns(address.pid, "ip", args...)
		}
	}
	// Pin neighbors so IPv6 assertions do not depend on multicast discovery or
	// duplicate-address-detection timing in the verification environment.
	for _, peer := range []struct{ device, name, pid, address, gateway string }{
		{"guestwire", "guest0", guest, "2001:db8:1::2", "2001:db8:1::1"},
		{"wan0", "remote0", remote, "2001:db8:2::2", "2001:db8:2::1"},
	} {
		var localLink, remoteLink []struct{ Address string }
		if err := json.Unmarshal(run("ip", "-j", "link", "show", "dev", peer.device), &localLink); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(ns(peer.pid, "ip", "-j", "link", "show", "dev", peer.name), &remoteLink); err != nil {
			t.Fatal(err)
		}
		run("ip", "-6", "neigh", "replace", peer.address, "lladdr", remoteLink[0].Address, "dev", peer.device, "nud", "permanent")
		ns(peer.pid, "ip", "-6", "neigh", "replace", peer.gateway, "lladdr", localLink[0].Address, "dev", peer.name, "nud", "permanent")
	}
	ns(guest, "ip", "route", "add", "default", "via", "172.30.0.1")
	ns(guest, "ip", "-6", "route", "add", "default", "via", "2001:db8:1::1")
	ns(remote, "ip", "-6", "route", "add", "2001:db8:1::/64", "via", "2001:db8:2::1")
	ns(remote, "ip", "addr", "add", "10.1.2.3/32", "dev", "lo")
	run("ip", "route", "add", "10.1.2.3/32", "via", "8.8.8.2")
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/proc/sys/net/ipv6/conf/all/forwarding", []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ns(guest, "ping", "-n", "-c", "1", "-W", "1", "2001:db8:2::2")
	run("ip", "link", "set", "dev", "guestwire", "alias", d.owner+":vm-test")
	run("ip", "link", "set", "dev", "guestwire", "name", "axvtest000")
	run("ip", "link", "set", "dev", "otherwire", "alias", d.owner+":vm-other")
	run("ip", "link", "set", "dev", "otherwire", "name", "axvtest001")
	ping := func(address, source string, wantSuccess bool) {
		t.Helper()
		args := []string{"--target", guest, "--net", "ping", "-n", "-c", "1", "-W", "1"}
		if source != "" {
			args = append(args, "-I", source)
		}
		args = append(args, address)
		out, err := exec.Command("nsenter", args...).CombinedOutput()
		if (err == nil) != wantSuccess {
			t.Fatalf("guest ping %s (source %s): wanted success=%t, got %v: %s", address, source, wantSuccess, err, out)
		}
	}
	ping("8.8.8.2", "", false)
	machine := compute.Machine{ID: "vm-test", Slot: 0}
	neighbor := compute.Machine{ID: "vm-other", Slot: 1}
	for _, m := range []compute.Machine{machine, neighbor} {
		run("ip", "link", "set", "dev", d.tap(m), "alias", d.owner+":"+m.ID)
	}
	for _, m := range []compute.Machine{machine, machine, neighbor} {
		if err := d.ensureNetwork(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	// Losing only dynamic entries is recoverable from verified owned TAPs.
	run("nft", "flush", "set", "inet", d.prefix, "sources")
	wrongMachine := machine
	wrongMachine.ID = "vm-different"
	if err = d.ensureNetwork(context.Background(), wrongMachine); err == nil {
		t.Fatal("another machine's TAP alias was accepted")
	}
	if out := string(run("nft", "-j", "list", "set", "inet", d.prefix, "sources")); strings.Contains(out, "elem") {
		t.Fatal("a rejected machine changed another machine's source bindings")
	}
	if err = d.ensureNetwork(context.Background(), machine); err != nil {
		t.Fatal(err)
	}
	run("ip", "route", "add", "172.30.0.0/22", "dev", d.tap(machine))
	if err = d.checkRoutes(context.Background()); err == nil {
		t.Fatal("an unexpected broad route was trusted solely because its TAP is owned")
	}
	run("ip", "route", "delete", "172.30.0.0/22", "dev", d.tap(machine))
	if out := string(run("nft", "-j", "list", "set", "inet", d.prefix, "sources")); !strings.Contains(out, "axvtest000") || !strings.Contains(out, "axvtest001") {
		t.Fatalf("source repair did not retain every owned TAP: %s", out)
	}
	run("nft", "add", "element", "inet", d.prefix, "sources", `{ "axvtest000" . 172.30.0.6 }`)
	if err = d.ensureNetwork(context.Background(), machine); err == nil {
		t.Fatal("a second guest's spoofable source binding was accepted")
	}
	run("nft", "delete", "element", "inet", d.prefix, "sources", `{ "axvtest000" . 172.30.0.6 }`)
	ping("8.8.8.2", "", true)
	for _, address := range []string{"172.30.0.1", "10.1.2.3", "172.30.0.6", "2001:db8:2::2"} {
		ping(address, "", false)
	}
	for _, address := range []string{"100.64.0.53", "169.254.10.1", "192.0.0.8", "192.0.0.170", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1", "192.0.0.9", "192.0.0.10"} {
		ns(remote, "ip", "addr", "add", address+"/32", "dev", "lo")
		run("ip", "route", "add", address+"/32", "via", "8.8.8.2")
		// Confirm the synthetic remote endpoint really answers before checking
		// guest filtering, including the globally reachable anycast exceptions.
		run("ping", "-n", "-c", "1", "-W", "1", address)
		ping(address, "", compute.IsPublicIPv4(netip.MustParseAddr(address)))
	}
	// Give an otherwise routable spoofed source a valid return route. Only
	// AXV's source guard should prevent this packet from reaching the remote.
	ns(guest, "ip", "addr", "add", "8.8.8.9/32", "dev", "guest0")
	ns(remote, "ip", "route", "add", "8.8.8.9/32", "via", "8.8.8.1")
	run("ip", "route", "add", "8.8.8.9/32", "dev", "axvtest000")
	ping("8.8.8.2", "8.8.8.9", false)
	ns(remote, "nft", "table inet received {\n chain input { type filter hook input priority 0; policy accept; ip protocol icmp icmp type echo-request counter; }\n}")
	packets := func() int {
		t.Helper()
		var rules struct {
			NFTables []struct {
				Rule struct {
					Expr []struct {
						Counter *struct{ Packets int }
					}
				}
			}
		}
		if err := json.Unmarshal(ns(remote, "nft", "-j", "list", "table", "inet", "received"), &rules); err != nil {
			t.Fatal(err)
		}
		for _, row := range rules.NFTables {
			for _, expr := range row.Rule.Expr {
				if expr.Counter != nil {
					return expr.Counter.Packets
				}
			}
		}
		t.Fatal("remote ICMP counter was not found")
		return 0
	}
	ping("8.8.8.2", "", true)
	before := packets()
	if before != 1 {
		t.Fatalf("remote packet counter did not see legitimate traffic: %d", before)
	}
	ns(guest, "ip", "addr", "add", "172.30.0.6/32", "dev", "guest0")
	ping("8.8.8.2", "172.30.0.6", false)
	if after := packets(); after != before {
		t.Fatalf("spoofed neighbor source reached the remote server: before=%d after=%d", before, after)
	}
	// A later firewall can deny AXV's accepted traffic. Explicit administrator
	// integration restores egress while AXV's earlier isolation still applies.
	run("nft", "add", "table", "ip", "host_test")
	run("nft", "add", "chain", "ip", "host_test", "forward", "{ type filter hook forward priority 0; policy drop; }")
	ping("8.8.8.2", "", false)
	for _, direction := range []struct{ in, out, address, state string }{
		{"axvtest*", "wan0", "saddr", "new,established,related"},
		{"wan0", "axvtest*", "daddr", "established,related"},
	} {
		run("nft", "add", "rule", "ip", "host_test", "forward", "iifname", direction.in, "oifname", direction.out, "ip", direction.address, "172.30.0.0/22", "ct", "state", direction.state, "accept")
	}
	ping("8.8.8.2", "", true)
	ping("10.1.2.3", "", false)
	ping("8.8.8.2", "8.8.8.9", false)
	for range 2 {
		if err := d.setSource(context.Background(), machine, false); err != nil {
			t.Fatal(err)
		}
	}
	ping("8.8.8.2", "", false)
	if out := string(run("nft", "-j", "list", "set", "inet", d.prefix, "sources")); !strings.Contains(out, "axvtest001") || strings.Contains(out, "axvtest000") {
		t.Fatalf("removing one machine affected another source binding: %s", out)
	}
	if err := d.setSource(context.Background(), machine, true); err != nil {
		t.Fatal(err)
	}
	ping("8.8.8.2", "", true)
	run("ping", "-n", "-c", "1", "-W", "1", "10.1.2.3")
	// Delete may commit the TAP removal before an nft write fails. Retrying
	// must remove its leftover tuple and preserve the other guest's resources.
	restore = failNFT()
	if err = d.removeNetwork(context.Background(), machine); err == nil {
		t.Fatal("injected removal failure was hidden")
	}
	restore()
	if exists, err := d.findLink(context.Background(), machine); err != nil || exists {
		t.Fatalf("delete fault did not occur after TAP removal: exists=%t error=%v", exists, err)
	}
	for range 2 {
		if err := d.removeNetwork(context.Background(), machine); err != nil {
			t.Fatal(err)
		}
	}
	if exists, err := d.findLink(context.Background(), neighbor); err != nil || !exists {
		t.Fatalf("removing one machine affected another TAP: exists=%t error=%v", exists, err)
	}
	if out := string(run("nft", "-j", "list", "set", "inet", d.prefix, "sources")); !strings.Contains(out, "axvtest001") || strings.Contains(out, "axvtest000") {
		t.Fatalf("removing a TAP affected another source binding: %s", out)
	}
	// A changed static rule is retained for diagnosis instead of being accepted
	// or silently rewritten. Returning the original rule restores its hash even
	// though the kernel assigns a new handle.
	inputRule := func() string {
		t.Helper()
		var rules struct {
			NFTables []struct {
				Rule struct {
					Chain  string
					Handle int
				}
			}
		}
		if err := json.Unmarshal(run("nft", "-j", "list", "chain", "inet", d.prefix, "input"), &rules); err != nil {
			t.Fatal(err)
		}
		for _, row := range rules.NFTables {
			if row.Rule.Chain == "input" {
				return strconv.Itoa(row.Rule.Handle)
			}
		}
		t.Fatal("input rule was not found")
		return ""
	}
	run("nft", "replace", "rule", "inet", d.prefix, "input", "handle", inputRule(), "iifname", "axvtest*", "accept")
	changed := string(run("nft", "-j", "list", "table", "inet", d.prefix))
	if err = d.firewall(context.Background()); err == nil {
		t.Fatal("changed static firewall rules were accepted")
	}
	if after := string(run("nft", "-j", "list", "table", "inet", d.prefix)); after != changed {
		t.Fatal("unknown firewall rules were rewritten")
	}
	run("nft", "delete", "rule", "inet", d.prefix, "input", "handle", inputRule())
	run("nft", "add", "rule", "inet", d.prefix, "input", "iifname", "axvtest*", "drop")
	if err = d.firewall(context.Background()); err != nil {
		t.Fatalf("static fingerprint depends on kernel handles: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(d.root, "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	run("nft", "delete", "table", "inet", d.prefix)
	if err = d.firewall(context.Background()); err == nil {
		t.Fatal("missing active firewall was silently recreated")
	}
	if after, err := os.ReadFile(filepath.Join(d.root, "network.json")); err != nil || string(after) != string(marker) {
		t.Fatalf("missing active firewall changed its baseline: %s %v", after, err)
	}
	run("nft", "list", "table", "ip", "host_test")
	t.Log("Verified real IPv4 forwarding, host/private/guest/IPv6/source isolation, SNAT and explicit host firewall coexistence.")
}
