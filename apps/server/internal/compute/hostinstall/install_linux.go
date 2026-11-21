//go:build linux && amd64

// Package hostinstall provisions the administrator-managed Linux VM service.
package hostinstall

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/artifacts"
	"github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/hostconn"
	"golang.org/x/sys/unix"
)

const (
	unitName   = "axv-host.service"
	unitFile   = "/etc/systemd/system/" + unitName
	binDir     = "/opt/axv/bin"
	configFile = "/etc/axv/host.json"
	// The private bootstrap config is published atomically with all executables.
	// An interrupted installer can finish without adopting an existing config.
	receipt   = binDir + "/.axv-host-install-v1.json"
	intent    = binDir + "/.axv-host-start-requested"
	completed = binDir + "/.axv-host-install-complete"
)

//go:embed axv-host.service
var unit []byte

type installation struct {
	installFS
	config     func(context.Context, int) (compute.Config, error)
	stage      func(context.Context, string) error
	stageGuest func(context.Context, io.Writer) error
	systemctl  func(context.Context, ...string) ([]byte, error)
	ready      func(context.Context, compute.Config) error
	readHost   func(context.Context, string, client.Binding) (compute.Info, error)
	waitHost   func(context.Context, compute.Config, string) (compute.Info, error)
	source     string
	image      compute.Image
}

func Run(ctx context.Context, check bool, groupName string, out io.Writer) error {
	if !check && os.Geteuid() != 0 {
		return fmt.Errorf("run host install-service with sudo on the AXV host")
	}
	group, err := socketGroup(ctx, groupName, os.Getuid(), os.Getenv("SUDO_UID"), inspectSystem)
	if err != nil {
		return err
	}
	return systemInstallation().run(ctx, group, check, out)
}

func systemInstallation() installation {
	fs := installFS{root: "/", uid: 0}
	release := artifacts.DefaultFirecrackerGuest()
	return installation{
		installFS: fs, config: initialConfig, stage: stageRuntime,
		source: "/proc/self/exe", ready: waitReady, image: defaultGuestImage(release),
		readHost: readHost, waitHost: waitForHost,
		stageGuest: func(ctx context.Context, out io.Writer) error {
			return fs.prepareGuest(ctx, release, out)
		},
		systemctl: func(ctx context.Context, args ...string) ([]byte, error) {
			return inspectSystem(ctx, "systemctl", args...)
		},
	}
}

type inspectCommand func(context.Context, string, ...string) ([]byte, error)

func socketGroup(ctx context.Context, override string, uid int, sudoUID string, inspect inspectCommand) (int, error) {
	if override != "" {
		raw, err := inspect(ctx, "getent", "--", "group", override)
		if err != nil {
			return 0, fmt.Errorf("resolve socket group %q: %w", override, err)
		}
		row := strings.Split(strings.TrimSpace(string(raw)), ":")
		if len(row) == 4 && (row[0] == override || row[2] == override) {
			gid, err := strconv.Atoi(row[2])
			if err == nil && gid > 0 {
				return gid, nil
			}
		}
		return 0, fmt.Errorf("--socket-group must identify an existing non-root group")
	}
	if uid == 0 && sudoUID != "" {
		var err error
		uid, err = strconv.Atoi(sudoUID)
		if err != nil || uid < 0 {
			return 0, fmt.Errorf("invalid sudo account identity")
		}
	}
	if uid == 0 {
		return 0, nil
	}
	// The default group must grant access only to the desktop user. An
	// administrator can explicitly authorize other members with --socket-group.
	passwd, err := inspect(ctx, "getent", "passwd")
	if err != nil {
		return 0, err
	}
	var username, gid string
	lines := strings.Split(strings.TrimSpace(string(passwd)), "\n")
	for _, line := range lines {
		row := strings.Split(line, ":")
		if len(row) == 7 && row[2] == strconv.Itoa(uid) {
			username, gid = row[0], row[3]
			break
		}
	}
	group, err := strconv.Atoi(gid)
	if err != nil || username == "" || group <= 0 {
		return 0, fmt.Errorf("cannot find a private primary group for desktop user %d; choose a trusted group with --socket-group GROUP", uid)
	}
	shared := fmt.Errorf("desktop user %s shares its primary group; use --socket-group GROUP to grant VM control to all members of a trusted group", username)
	for _, line := range lines {
		row := strings.Split(line, ":")
		if len(row) == 7 && row[3] == gid && row[0] != username {
			return 0, shared
		}
	}
	raw, err := inspect(ctx, "getent", "--", "group", gid)
	if err != nil {
		return 0, err
	}
	row := strings.Split(strings.TrimSpace(string(raw)), ":")
	if len(row) != 4 || row[2] != gid || (row[3] != "" && row[3] != username) {
		return 0, shared
	}
	return group, nil
}

type installPlan struct {
	config                  compute.Config
	configJSON              []byte
	fresh, active, complete bool
}

func (i installation) run(ctx context.Context, group int, check bool, out io.Writer) error {
	if !check {
		lock, err := i.lock()
		if err != nil {
			return err
		}
		defer lock.Close()
	}
	plan, err := i.plan(ctx, group)
	if err != nil {
		return err
	}
	if plan.complete {
		fmt.Fprintln(out, "The VM service is already installed. Check its status with systemctl status axv-host.service --no-pager.")
		return nil
	}
	if check {
		fmt.Fprintf(out, "Host installation check passed. Socket %s, configuration %s, systemd unit %s.\n", plan.config.Socket, configFile, unitFile)
		return nil
	}
	if plan.fresh {
		if err := i.stageGuest(ctx, out); err != nil {
			return err
		}
		fmt.Fprintln(out, "Preparing the pinned Firecracker runtime and AXV executable…")
		if err := i.stageFiles(ctx, plan.configJSON); err != nil {
			return err
		}
	}
	if err := i.publish(configFile, plan.configJSON, 0600); err != nil {
		return err
	}
	if err := i.publish(unitFile, unit, 0644); err != nil {
		return err
	}
	if !plan.active {
		fmt.Fprintln(out, "Loading and starting axv-host.service…")
		if _, err := i.systemctl(ctx, "--system", "daemon-reload"); err != nil {
			return err
		}
		loaded, _, err := i.unitState(ctx)
		if err != nil {
			return err
		}
		if !loaded {
			return fmt.Errorf("systemd did not load %s", unitFile)
		}
		if err := i.publish(intent, []byte(unitName+"\n"), 0600); err != nil {
			return err
		}
		if _, err := i.systemctl(ctx, "--system", "start", unitName); err != nil {
			return fmt.Errorf("start %s: %w; inspect systemctl status %s --no-pager", unitName, err, unitName)
		}
	}
	if err := i.ready(ctx, plan.config); err != nil {
		return fmt.Errorf("verify VM service readiness: %w; inspect systemctl status %s --no-pager", err, unitName)
	}
	if err := i.publish(completed, []byte(unitName+"\n"), 0600); err != nil {
		return err
	}
	fmt.Fprintln(out, "AXV VM service is ready. Choose Connect in Virtual Machines. Use Check connection if AXV has yet to detect the service.")
	return nil
}

func (i installation) plan(ctx context.Context, group int) (installPlan, error) {
	var p installPlan
	for _, dir := range []string{binDir, "/etc/axv", "/etc/systemd/system", "/var/lib/axv", "/run/axv-host"} {
		if err := i.directories(dir, false); err != nil {
			return p, err
		}
	}
	cfg, err := i.read(receipt, 0600)
	p.fresh = os.IsNotExist(err)
	if err != nil && !p.fresh {
		return p, err
	}
	loaded, active, err := i.unitState(ctx)
	if err != nil {
		return p, err
	}
	p.active = active
	if p.fresh {
		if loaded {
			return p, fmt.Errorf("systemd already has %s; preserving the existing service", unitName)
		}
		if err := i.requireAbsent(binDir, configFile, unitFile, "/var/lib/axv", "/run/axv-host"); err != nil {
			return p, err
		}
		config, err := i.config(ctx, group)
		if err != nil {
			return p, err
		}
		if group == 0 {
			config.SocketGroup = nil
		}
		config.Images = []compute.Image{i.image}
		cfg, err = json.MarshalIndent(config, "", "  ")
		if err != nil {
			return p, err
		}
		p.config, p.configJSON = config, append(cfg, '\n')
		return p, nil
	}
	p.configJSON = cfg
	if err := json.Unmarshal(cfg, &p.config); err != nil {
		return p, fmt.Errorf("invalid installer receipt: %w", err)
	}
	gid := p.config.SocketGroup
	if (group != 0 && gid == nil) || (gid != nil && *gid != group) {
		return p, fmt.Errorf("this installation belongs to another desktop group")
	}
	for _, file := range []string{"axv", "firecracker", "jailer"} {
		path := i.path(binDir + "/" + file)
		info, err := os.Lstat(path)
		if err != nil {
			return p, err
		}
		if err = i.trusted(path, info); err != nil {
			return p, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
			return p, fmt.Errorf("unexpected installed executable %s", path)
		}
	}
	started, err := i.marker(intent)
	if err != nil {
		return p, err
	}
	p.complete, err = i.marker(completed)
	if err != nil {
		return p, err
	}
	if p.complete && !started {
		return p, fmt.Errorf("incomplete installation records in %s", binDir)
	}
	if !started {
		if p.active {
			return p, fmt.Errorf("an existing %s is already active", unitName)
		}
		if err := i.requireAbsent("/run/axv-host", "/var/lib/axv"); err != nil {
			return p, err
		}
	}
	if p.complete {
		current, err := i.read(configFile, 0600)
		if err != nil {
			return p, err
		}
		installed, err := compute.DecodeConfig(bytes.NewReader(current))
		if err != nil {
			return p, fmt.Errorf("installed host configuration: %w", err)
		}
		// An administrator can add image records after setup. Other changes
		// still differ from the original install and must be left alone.
		installed.Images = p.config.Images
		if installed.Fingerprint() != p.config.Fingerprint() {
			return p, fmt.Errorf("existing %s differs from the installer receipt; preserving it", configFile)
		}
	} else if err := i.agrees(configFile, cfg, 0600, started || loaded); err != nil {
		return p, err
	}
	if err := i.agrees(unitFile, unit, 0644, started || loaded); err != nil {
		return p, err
	}
	return p, nil
}

func (i installation) requireAbsent(names ...string) error {
	for _, name := range names {
		if _, err := os.Lstat(i.path(name)); err == nil {
			return fmt.Errorf("%s already exists; preserving the existing installation", name)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (i installation) marker(name string) (bool, error) {
	data, err := i.read(name, 0600)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if string(data) != unitName+"\n" {
		return false, fmt.Errorf("invalid installer record %s", name)
	}
	return true, nil
}

func (i installation) agrees(name string, want []byte, mode os.FileMode, required bool) error {
	current, err := i.read(name, mode)
	if os.IsNotExist(err) && !required {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(current, want) {
		return fmt.Errorf("existing %s differs from the installer receipt; preserving it", name)
	}
	return nil
}

func (i installation) unitState(ctx context.Context) (loaded, active bool, err error) {
	output, err := i.systemctl(ctx, "--system", "show", "--property=LoadState,ActiveState,FragmentPath,DropInPaths,NeedDaemonReload", "--no-pager", unitName)
	if err != nil {
		return false, false, err
	}
	fields := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		key, value, _ := strings.Cut(line, "=")
		fields[key] = value
	}
	if fields["DropInPaths"] == "" {
		if fields["LoadState"] == "not-found" && fields["ActiveState"] == "inactive" && fields["FragmentPath"] == "" {
			return false, false, nil
		}
		if fields["LoadState"] == "loaded" && fields["FragmentPath"] == unitFile && fields["NeedDaemonReload"] == "no" {
			switch fields["ActiveState"] {
			case "active":
				return true, true, nil
			case "inactive", "failed":
				return true, false, nil
			}
		}
	}
	return false, false, fmt.Errorf("systemd reports an existing, overridden, changing or stale %s; inspect systemctl status %s --no-pager", unitName, unitName)
}

func (i installation) stageFiles(ctx context.Context, cfg []byte) error {
	parent := filepath.Dir(binDir)
	if err := i.directories(parent, true); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(i.path(parent), ".axv-host-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := i.stage(ctx, stage); err != nil {
		return err
	}
	src, err := os.Open(i.source)
	if err != nil {
		return err
	}
	defer src.Close()
	file, err := os.OpenFile(filepath.Join(stage, "axv"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	if err = writeFile(file, src, 0755); err != nil {
		return err
	}
	file, err = os.OpenFile(filepath.Join(stage, filepath.Base(receipt)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = writeFile(file, bytes.NewReader(cfg), 0600); err != nil {
		return err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	// An administrator may have prepared this host while the download ran.
	if err := i.requireAbsent(configFile, unitFile, "/var/lib/axv", "/run/axv-host"); err != nil {
		return err
	}
	loaded, _, err := i.unitState(ctx)
	if err != nil {
		return err
	}
	if loaded {
		return fmt.Errorf("systemd registered %s during installation", unitName)
	}
	if err := syncDirectory(stage); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, i.path(binDir), unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return syncDirectory(i.path(parent))
}

func waitReady(ctx context.Context, config compute.Config) error {
	info, err := waitForHost(ctx, config, "")
	if err != nil {
		return err
	}
	for _, image := range config.Images {
		if !hasImage(info, image) {
			return fmt.Errorf("the host did not register image %s", image.ID)
		}
	}
	return nil
}

func readHost(ctx context.Context, socket string, expected client.Binding) (compute.Info, error) {
	vm, err := client.Connect(ctx, socket, expected)
	if err != nil {
		return compute.Info{}, err
	}
	defer vm.Close()
	info, err := vm.Info(ctx)
	if err == nil && (info.HostID != vm.Binding.HostID ||
		info.ConfigFingerprint != vm.Binding.ConfigFingerprint ||
		info.ProtocolVersion != compute.ProtocolVersion) {
		return compute.Info{}, hostconn.ErrChanged
	}
	return info, err
}

func firecrackerReady(info compute.Info) error {
	for _, p := range info.Providers {
		if p.Provider == "firecracker" {
			if p.Available {
				return nil
			}
			return fmt.Errorf("Firecracker unavailable: %s", p.Reason)
		}
	}
	return errors.New("host does not advertise Firecracker")
}

func waitForHost(ctx context.Context, config compute.Config, hostID string) (compute.Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var last error
	for {
		info, err := readHost(ctx, config.Socket, client.Binding{HostID: hostID, ConfigFingerprint: config.Fingerprint()})
		if errors.Is(err, hostconn.ErrChanged) {
			return info, err
		}
		if err == nil {
			return info, firecrackerReady(info)
		}
		last = err
		select {
		case <-ctx.Done():
			return info, fmt.Errorf("%w: %v", ctx.Err(), last)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
