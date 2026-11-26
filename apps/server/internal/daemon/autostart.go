package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// AutostartInfo reports persistent boot/login autostart status for a profile.
type AutostartInfo struct {
	Supported     bool   `json:"supported"`
	Enabled       bool   `json:"enabled"`
	UnitPath      string `json:"unitPath,omitempty"`
	LingerChecked bool   `json:"lingerChecked,omitempty"`
	LingerEnabled bool   `json:"lingerEnabled,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

func autostartUnitName(home string) string {
	return "axv-desktop-" + profile.Identity(home) + ".service"
}

func autostartPlistLabel(home string) string {
	return "com.axv." + profile.Identity(home) + ".desktop.autostart"
}

func autostartUnitPath(home string) (string, error) {
	if runtime.GOOS == "darwin" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(userHome, "Library", "LaunchAgents", autostartPlistLabel(home)+".plist"), nil
	}
	if runtime.GOOS == "linux" {
		dir, err := systemdUserUnitDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, autostartUnitName(home)), nil
	}
	return "", fmt.Errorf("autostart is supported on macOS and Linux")
}

// AutostartStatus inspects whether boot/login autostart is configured for home.
// Read-only: never creates directories or modifies units.
func AutostartStatus(ctx context.Context, home string) AutostartInfo {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return AutostartInfo{Supported: false, Detail: "autostart requires macOS or Linux"}
	}
	unitPath, err := autostartUnitPath(home)
	if err != nil {
		return AutostartInfo{Supported: true, Detail: err.Error()}
	}
	info := AutostartInfo{Supported: true, UnitPath: unitPath}
	if _, statErr := os.Stat(unitPath); statErr == nil {
		info.Enabled = true
	}
	if runtime.GOOS == "linux" {
		if checked, enabled := checkLinuxLinger(ctx); checked {
			info.LingerChecked = true
			info.LingerEnabled = enabled
		}
	}
	return info
}

// EnableAutostart installs a persistent login/boot desktop service for home.
func EnableAutostart(ctx context.Context, home, executable string, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return fmt.Errorf("autostart is currently supported on macOS and Linux; use axv serve")
	}
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	executable, err := filepath.Abs(executable)
	if err != nil {
		return err
	}
	unitPath, err := autostartUnitPath(home)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0700); err != nil {
		return err
	}
	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	job := Job{
		Role:        "desktop",
		Label:       autostartPlistLabel(home),
		Args:        []string{executable, "serve"},
		Definition:  unitPath,
		Log:         filepath.Join(logDir, "desktop.log"),
		Environment: environment(home),
	}
	if runtime.GOOS == "darwin" {
		if err := atomicfile.Write(unitPath, []byte(plist(job)), 0600); err != nil {
			return err
		}
		fmt.Fprintf(out, "Enabled macOS login autostart: %s\n", unitPath)
		return nil
	}
	content := systemdUnit(job, true)
	if err := atomicfile.Write(unitPath, []byte(content), 0600); err != nil {
		return err
	}
	n, err := nativeSupervisor()
	if err != nil {
		return err
	}
	nativeMgr := n.(*native)
	if _, err := nativeMgr.run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd user units: %w", err)
	}
	unitName := autostartUnitName(home)
	if _, err := nativeMgr.run(ctx, "systemctl", "--user", "enable", unitName); err != nil {
		return fmt.Errorf("enable %s: %w", unitName, err)
	}
	fmt.Fprintf(out, "Enabled Linux autostart: %s\n", unitPath)
	if checked, enabled := checkLinuxLinger(ctx); checked && !enabled {
		username := currentUsername()
		fmt.Fprintf(out, "Note: systemd user lingering is disabled for %s; services may stop on SSH logout and will not start before login.\n", username)
		fmt.Fprintf(out, "Run `sudo loginctl enable-linger %s` to keep AXV running after logout and across reboots.\n", username)
	}
	return nil
}

// DisableAutostart removes the persistent login/boot desktop unit for home.
func DisableAutostart(ctx context.Context, home string, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return fmt.Errorf("autostart is currently supported on macOS and Linux")
	}
	unitPath, err := autostartUnitPath(home)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(unitPath); os.IsNotExist(statErr) {
		fmt.Fprintln(out, "Autostart is not enabled for this profile.")
		return nil
	}
	if runtime.GOOS == "linux" {
		if n, err := nativeSupervisor(); err == nil {
			nativeMgr := n.(*native)
			unitName := autostartUnitName(home)
			_, _ = nativeMgr.run(ctx, "systemctl", "--user", "disable", "--now", unitName)
			_ = os.Remove(unitPath)
			_, _ = nativeMgr.run(ctx, "systemctl", "--user", "daemon-reload")
		} else {
			_ = os.Remove(unitPath)
		}
		fmt.Fprintf(out, "Disabled Linux autostart (%s).\n", unitPath)
		return nil
	}
	if n, err := nativeSupervisor(); err == nil {
		nativeMgr := n.(*native)
		_, _ = nativeMgr.run(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), autostartPlistLabel(home)))
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintf(out, "Disabled macOS autostart (%s).\n", unitPath)
	return nil
}

func checkLinuxLinger(ctx context.Context) (checked, enabled bool) {
	if runtime.GOOS != "linux" {
		return false, false
	}
	username := currentUsername()
	if username == "" {
		return false, false
	}
	// Fast check without subprocess: systemd stores linger markers here.
	if _, err := os.Stat(filepath.Join("/var/lib/systemd/linger", username)); err == nil {
		return true, true
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "loginctl", "show-user", username, "-p", "Linger", "--value").Output()
	if err != nil {
		return false, false
	}
	return true, strings.EqualFold(strings.TrimSpace(string(out)), "yes")
}

func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}
