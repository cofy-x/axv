//go:build linux

package hostinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

const (
	lifecycleSystemPath = "/usr/sbin:/usr/bin:/sbin:/bin"
	sysctlDropIn        = "/etc/sysctl.d/99-axv.conf"
)

type lifecycleEnv struct {
	fs        installFS
	source    string
	systemctl func(context.Context, ...string) ([]byte, error)
	runCmd    func(context.Context, string, ...string) ([]byte, error)
	probe     func(context.Context, string) error
}

func defaultLifecycleEnv() lifecycleEnv {
	return lifecycleEnv{
		fs:     installFS{root: "/", uid: 0},
		source: "/proc/self/exe",
		systemctl: func(ctx context.Context, args ...string) ([]byte, error) {
			return runLifecycleTool(ctx, "systemctl", args...)
		},
		runCmd: runLifecycleTool,
		probe:  probeSocketReady,
	}
}

func runLifecycleTool(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, err := findLifecycleTool(name)
	if err != nil {
		return nil, err
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, path, args...)
	cmd.Env = []string{"PATH=" + lifecycleSystemPath, "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func findLifecycleTool(name string) (string, error) {
	for _, dir := range filepath.SplitList(lifecycleSystemPath) {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("required system tool %s was not found", name)
}

// Status inspects the Linux axv-host.service without mutating state.
func Status(ctx context.Context, currentVersion string) ServiceStatus {
	return defaultLifecycleEnv().status(ctx, currentVersion)
}

func (e lifecycleEnv) status(ctx context.Context, currentVersion string) ServiceStatus {
	st := ServiceStatus{
		Supported:       true,
		State:           "not_installed",
		Socket:          StandardSocket,
		InstalledBinary: filepath.Join(BinDir, "axv"),
		CurrentVersion:  strings.TrimSpace(currentVersion),
		UnitFile:        UnitFile,
		ConfigFile:      ConfigFile,
	}
	if ctx.Err() != nil {
		st.State = "unknown"
		st.Detail = ctx.Err().Error()
		return st
	}
	out, err := e.systemctl(ctx, "--system", "show",
		"--property=LoadState,ActiveState,UnitFileState,FragmentPath",
		"--no-pager", UnitName)
	if err != nil {
		st.State = "unknown"
		st.Detail = err.Error()
	} else {
		fields := parseSystemdFields(out)
		load := fields["LoadState"]
		active := fields["ActiveState"]
		unitState := fields["UnitFileState"]
		if load == "loaded" {
			st.Installed = true
			st.State = active
			st.Active = active == "active"
			st.Enabled = unitState == "enabled" || unitState == "enabled-runtime"
		} else if load == "not-found" {
			st.Installed = false
			st.State = "not_installed"
		} else {
			st.State = load
		}
	}
	// If unit was not found via systemctl, check whether binary/unit exist on disk.
	if !st.Installed {
		if _, statErr := os.Lstat(e.fs.path(UnitFile)); statErr == nil {
			st.Installed = true
			if st.State == "not_installed" {
				st.State = "inactive"
			}
		}
	}
	if cfg, cfgErr := e.readInstalledConfig(); cfgErr == nil && cfg.Socket != "" {
		st.Socket = cfg.Socket
	}
	if conn, dialErr := net.DialTimeout("unix", st.Socket, 250*time.Millisecond); dialErr == nil {
		_ = conn.Close()
		st.SocketReachable = true
	}
	binPath := e.fs.path(st.InstalledBinary)
	if info, statErr := os.Stat(binPath); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
		if ver, verErr := readBinaryVersion(ctx, binPath); verErr == nil {
			st.InstalledVersion = ver
			if st.CurrentVersion != "" && ver != "" && ver != st.CurrentVersion {
				st.VersionDrift = true
			}
		}
	}
	return st
}

func readBinaryVersion(ctx context.Context, binaryPath string) (string, error) {
	vctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(vctx, binaryPath, "version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(out))
	text = strings.TrimPrefix(text, "axv ")
	return strings.TrimSpace(text), nil
}

func parseSystemdFields(output []byte) map[string]string {
	fields := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = value
		}
	}
	return fields
}

func (e lifecycleEnv) readInstalledConfig() (compute.Config, error) {
	raw, err := e.fs.read(ConfigFile, 0600)
	if err != nil {
		return compute.Config{}, err
	}
	return compute.DecodeConfig(bytes.NewReader(raw))
}

func probeSocketReady(ctx context.Context, socket string) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var last error
	for {
		probeCtx, probeCancel := context.WithTimeout(deadlineCtx, 2*time.Second)
		_, err := client.Probe(probeCtx, socket, client.Binding{})
		probeCancel()
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("VM host socket %s did not become ready: %w (%v)", socket, deadlineCtx.Err(), last)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func requireRoot(action string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("%s requires root privileges; rerun with sudo", action)
	}
	return nil
}

// StartService starts the installed Linux axv-host.service and waits for readiness.
func StartService(ctx context.Context, out io.Writer) error {
	if err := requireRoot("starting the Linux VM service"); err != nil {
		return err
	}
	return defaultLifecycleEnv().startService(ctx, out)
}

func (e lifecycleEnv) startService(ctx context.Context, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	st := e.status(ctx, "")
	if !st.Installed {
		return fmt.Errorf("%s is not installed; run `sudo axv host install-service` first", UnitName)
	}
	if _, err := e.systemctl(ctx, "--system", "start", UnitName); err != nil {
		return fmt.Errorf("start %s: %w", UnitName, err)
	}
	if err := e.probe(ctx, st.Socket); err != nil {
		return err
	}
	fmt.Fprintf(out, "Linux VM service (%s) is running on %s.\n", UnitName, st.Socket)
	return nil
}

// StopService stops the Linux axv-host.service while preserving running VMs.
func StopService(ctx context.Context, out io.Writer) error {
	if err := requireRoot("stopping the Linux VM service"); err != nil {
		return err
	}
	return defaultLifecycleEnv().stopService(ctx, out)
}

func (e lifecycleEnv) stopService(ctx context.Context, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	st := e.status(ctx, "")
	if !st.Installed {
		fmt.Fprintf(out, "Linux VM service (%s) is not installed.\n", UnitName)
		return nil
	}
	if _, err := e.systemctl(ctx, "--system", "stop", UnitName); err != nil {
		return fmt.Errorf("stop %s: %w", UnitName, err)
	}
	fmt.Fprintf(out, "Linux VM service (%s) stopped. Running VMs and project volumes are preserved.\n", UnitName)
	return nil
}

// RestartService restarts axv-host.service while preserving running VMs.
func RestartService(ctx context.Context, out io.Writer) error {
	if err := requireRoot("restarting the Linux VM service"); err != nil {
		return err
	}
	return defaultLifecycleEnv().restartService(ctx, out)
}

func (e lifecycleEnv) restartService(ctx context.Context, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	st := e.status(ctx, "")
	if !st.Installed {
		return fmt.Errorf("%s is not installed; run `sudo axv host install-service` first", UnitName)
	}
	if _, err := e.systemctl(ctx, "--system", "restart", UnitName); err != nil {
		return fmt.Errorf("restart %s: %w", UnitName, err)
	}
	if err := e.probe(ctx, st.Socket); err != nil {
		return err
	}
	fmt.Fprintf(out, "Linux VM service (%s) restarted on %s. Running VMs were preserved.\n", UnitName, st.Socket)
	return nil
}

// UpgradeService atomically updates /opt/axv/bin/axv from the currently running
// executable and restarts axv-host.service if active (preserving running VMs).
func UpgradeService(ctx context.Context, out io.Writer) error {
	if err := requireRoot("upgrading the Linux VM service"); err != nil {
		return err
	}
	return defaultLifecycleEnv().upgradeService(ctx, out)
}

func (e lifecycleEnv) upgradeService(ctx context.Context, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := e.fs.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	st := e.status(ctx, "")
	if !st.Installed {
		return fmt.Errorf("%s is not installed; run `sudo axv host install-service` first", UnitName)
	}
	if err := e.fs.directories(BinDir, false); err != nil {
		return err
	}
	dest := e.fs.path(filepath.Join(BinDir, "axv"))
	info, err := os.Lstat(dest)
	if err != nil {
		return fmt.Errorf("inspect installed host binary %s: %w", dest, err)
	}
	if err := e.fs.trusted(dest, info); err != nil {
		return err
	}
	src, err := os.Open(e.source)
	if err != nil {
		return fmt.Errorf("open current axv executable: %w", err)
	}
	defer src.Close()
	srcInfo, err := src.Stat()
	if err != nil || !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("current executable is not a regular file")
	}
	sameFile := os.SameFile(info, srcInfo)
	if !sameFile {
		tmp, err := os.CreateTemp(filepath.Dir(dest), ".axv-upgrade-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		if err := writeFile(tmp, src, 0755); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		if err := os.Rename(tmpName, dest); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		if err := syncDirectory(filepath.Dir(dest)); err != nil {
			return err
		}
	}
	newVersion, _ := readBinaryVersion(ctx, dest)
	if st.Active {
		fmt.Fprintf(out, "Restarting %s to apply updated AXV host binary…\n", UnitName)
		if _, err := e.systemctl(ctx, "--system", "restart", UnitName); err != nil {
			return fmt.Errorf("restart %s after upgrade: %w", UnitName, err)
		}
		if err := e.probe(ctx, st.Socket); err != nil {
			return err
		}
	}
	if st.InstalledVersion != "" && newVersion != "" && st.InstalledVersion != newVersion {
		fmt.Fprintf(out, "Upgraded %s: %s -> %s.\n", UnitName, st.InstalledVersion, newVersion)
	} else if sameFile {
		fmt.Fprintf(out, "%s already uses this AXV binary (%s).\n", UnitName, newVersion)
	} else {
		fmt.Fprintf(out, "Updated %s binary (%s).\n", UnitName, newVersion)
	}
	return nil
}

// UninstallService stops, disables and removes the Linux axv-host.service unit
// and installed binaries. VM storage under /var/lib/axv is retained unless
// opts.PurgeData is explicitly set.
func UninstallService(ctx context.Context, opts UninstallOptions, out io.Writer) error {
	if err := requireRoot("uninstalling the Linux VM service"); err != nil {
		return err
	}
	return defaultLifecycleEnv().uninstallService(ctx, opts, out)
}

func (e lifecycleEnv) uninstallService(ctx context.Context, opts UninstallOptions, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := e.fs.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	st := e.status(ctx, "")
	if st.Active {
		_, _ = e.systemctl(ctx, "--system", "stop", UnitName)
	}
	if st.Enabled {
		_, _ = e.systemctl(ctx, "--system", "disable", UnitName)
	}
	removedUnit := false
	if err := os.Remove(e.fs.path(UnitFile)); err == nil {
		removedUnit = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if removedUnit {
		_, _ = e.systemctl(ctx, "--system", "daemon-reload")
	}
	if err := os.RemoveAll(e.fs.path(BinDir)); err != nil {
		return err
	}
	if opts.PurgeConfig {
		_ = os.Remove(e.fs.path(ConfigFile))
		_ = os.RemoveAll(e.fs.path("/opt/axv/images"))
		_ = os.Remove(e.fs.path(sysctlDropIn))
	}
	if opts.PurgeData {
		_ = os.RemoveAll(e.fs.path("/var/lib/axv"))
		_ = os.RemoveAll(e.fs.path("/run/axv-host"))
	}
	fmt.Fprintf(out, "Uninstalled %s and removed %s.\n", UnitName, BinDir)
	if !opts.PurgeConfig {
		fmt.Fprintf(out, "Retained configuration (%s) and images (/opt/axv/images). Pass --purge to remove them.\n", ConfigFile)
	}
	if !opts.PurgeData {
		fmt.Fprintln(out, "Retained VM storage under /var/lib/axv. Pass --purge-data after stopping VMs to delete it.")
	}
	return nil
}

// StreamLogs writes recent or live journald logs for axv-host.service.
func StreamLogs(ctx context.Context, follow bool, lines int, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lines <= 0 {
		lines = 100
	}
	path, err := findLifecycleTool("journalctl")
	if err != nil {
		return err
	}
	args := []string{"-u", UnitName, "--no-pager", "-n", strconv.Itoa(lines)}
	if follow {
		args = append(args, "-f")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	err = cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// applyIPv4Forwarding enables net.ipv4.ip_forward immediately and persists it
// in /etc/sysctl.d/99-axv.conf.
func (e lifecycleEnv) applyIPv4Forwarding(ctx context.Context, out io.Writer) error {
	const content = "# Enabled by AXV installer for Firecracker VM outbound networking.\nnet.ipv4.ip_forward = 1\n"
	if err := e.fs.directories(filepath.Dir(sysctlDropIn), true); err != nil {
		return err
	}
	dest := e.fs.path(sysctlDropIn)
	if err := os.WriteFile(dest, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if _, err := e.runCmd(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	fmt.Fprintf(out, "Enabled IPv4 forwarding and persisted %s.\n", sysctlDropIn)
	return nil
}

// connectInvokingProfile links the desktop user's AXV profile to socket when
// compute is currently disabled, preserving existing custom compute settings
// and file ownership.
func connectInvokingProfile(socket, explicitHome string) (string, bool, error) {
	home, uid, gid, err := resolveInvokingProfile(explicitHome)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return "", false, err
	}
	_ = chownIfRoot(home, uid, gid)

	current, err := profile.ReadCompute(home)
	if err != nil {
		return home, false, err
	}
	if current.Compute.Mode == "external" && current.Compute.Socket == socket {
		return home, false, nil
	}
	if current.Compute.Mode != "" && current.Compute.Mode != "disabled" {
		return home, false, nil
	}
	lock, err := profile.Lock(home)
	if err != nil {
		return home, false, err
	}
	defer func() {
		_ = lock.Close()
		_ = chownIfRoot(filepath.Join(home, "run"), uid, gid)
		_ = chownIfRoot(filepath.Join(home, "run", "command.lock"), uid, gid)
	}()
	// Initialize starter profile if config.json does not exist yet.
	cfgPath := filepath.Join(home, "config.json")
	if _, statErr := os.Stat(cfgPath); errors.Is(statErr, os.ErrNotExist) {
		if _, err := profile.Initialize(home); err != nil {
			return home, false, err
		}
	}
	if err := profile.SaveCompute(home, profile.Compute{Mode: "external", Socket: socket}); err != nil {
		return home, false, err
	}
	_ = chownIfRoot(cfgPath, uid, gid)
	return home, true, nil
}

func resolveInvokingProfile(explicitHome string) (string, int, int, error) {
	uid, gid := os.Getuid(), os.Getgid()
	var userHome string
	if uid == 0 && os.Getenv("SUDO_UID") != "" {
		if parsedUID, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil && parsedUID >= 0 {
			uid = parsedUID
		}
		if parsedGID, err := strconv.Atoi(os.Getenv("SUDO_GID")); err == nil && parsedGID >= 0 {
			gid = parsedGID
		}
		if u, err := user.LookupId(strconv.Itoa(uid)); err == nil && u.HomeDir != "" {
			userHome = u.HomeDir
		}
	}
	if explicitHome != "" {
		return explicitHome, uid, gid, nil
	}
	if envHome := strings.TrimSpace(os.Getenv("AXV_HOME")); envHome != "" {
		return envHome, uid, gid, nil
	}
	if userHome != "" {
		return filepath.Join(userHome, ".axv"), uid, gid, nil
	}
	home, err := profile.Home()
	return home, uid, gid, err
}

func chownIfRoot(path string, uid, gid int) error {
	if os.Geteuid() != 0 || (uid == 0 && gid == 0) {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) == uid && int(st.Gid) == gid {
		return nil
	}
	return os.Chown(path, uid, gid)
}

// HostNetworkInfo reads the configured Firecracker subnet and host ID from
// /etc/axv/host.json when readable, for doctor firewall guidance.
func HostNetworkInfo() (subnet string, ok bool) {
	raw, err := os.ReadFile(ConfigFile)
	if err != nil {
		return "", false
	}
	var cfg struct {
		Firecracker *struct {
			Subnet string `json:"subnet"`
		} `json:"firecracker"`
	}
	if json.Unmarshal(raw, &cfg) != nil || cfg.Firecracker == nil || cfg.Firecracker.Subnet == "" {
		return "", false
	}
	return cfg.Firecracker.Subnet, true
}
