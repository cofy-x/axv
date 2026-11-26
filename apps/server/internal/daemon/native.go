// Package daemon supervises independent AXV services through the operating system.
package daemon

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
)

type Job struct {
	Role        string            `json:"role"`
	Label       string            `json:"label"`
	Args        []string          `json:"args"`
	Environment map[string]string `json:"environment"`
	Definition  string            `json:"definition"`
	Log         string            `json:"log"`
	Marker      string            `json:"marker"`
}

type supervisor interface {
	Loaded(context.Context, Job) (bool, error)
	Start(context.Context, Job) error
	Stop(context.Context, Job) error
}

type native struct {
	platform    string
	userUnitDir string
	run         func(context.Context, string, ...string) (string, error)
}

func systemdUserUnitDir() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func nativeSupervisor() (supervisor, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("background services currently require macOS launchd or Linux systemd; use axv serve")
	}
	unitDir := ""
	if runtime.GOOS == "linux" {
		var err error
		unitDir, err = systemdUserUnitDir()
		if err != nil {
			return nil, err
		}
	}
	return &native{platform: runtime.GOOS, userUnitDir: unitDir, run: func(ctx context.Context, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			if ctx.Err() != nil {
				return string(b), ctx.Err()
			}
			return string(b), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(b)))
		}
		return string(b), nil
	}}, nil
}

func target(j Job) string { return fmt.Sprintf("gui/%d/%s", os.Getuid(), j.Label) }

func (n *native) linuxUnitPath(j Job) string {
	return filepath.Join(n.userUnitDir, j.Label+".service")
}

func (n *native) Loaded(ctx context.Context, j Job) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var out string
	var err error
	if n.platform == "darwin" {
		out, err = n.run(ctx, "launchctl", "print", target(j))
	} else {
		out, err = n.run(ctx, "systemctl", "--user", "show", j.Label+".service", "--property=LoadState,ActiveState,ExecStart")
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, err
	}
	if n.platform == "darwin" && err != nil && (strings.Contains(out, "Could not find service") || strings.Contains(out, "Could not find specified service")) {
		return false, nil
	}
	if n.platform == "darwin" && err != nil && strings.Contains(out, "Could not find domain") {
		return false, fmt.Errorf("macOS GUI launchd session is unavailable (for example over headless SSH); use `axv serve` or `axv daemon enable`: %w", err)
	}
	if n.platform != "darwin" && (strings.Contains(out, "LoadState=not-found") || err == nil && (strings.Contains(out, "ActiveState=inactive") || strings.Contains(out, "ActiveState=failed"))) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !strings.Contains(out, j.Marker) || !strings.Contains(out, j.Args[0]) {
		return false, fmt.Errorf("service %s does not match its AXV ownership record; refusing to change it", j.Label)
	}
	return true, nil
}

func (n *native) Start(ctx context.Context, j Job) error {
	if loaded, err := n.Loaded(ctx, j); err != nil {
		return err
	} else if loaded {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n.platform == "darwin" {
		if err := atomicfile.Write(j.Definition, []byte(plist(j)), 0600); err != nil {
			return err
		}
		_, err := n.run(ctx, "launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), j.Definition)
		return err
	}
	if n.userUnitDir == "" {
		return fmt.Errorf("systemd user unit directory is unavailable")
	}
	if err := os.MkdirAll(n.userUnitDir, 0700); err != nil {
		return err
	}
	unitPath := n.linuxUnitPath(j)
	if err := atomicfile.Write(unitPath, []byte(systemdUnit(j, false)), 0600); err != nil {
		return err
	}
	if _, err := n.run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		_ = os.Remove(unitPath)
		return err
	}
	if _, err := n.run(ctx, "systemctl", "--user", "start", j.Label+".service"); err != nil {
		_ = os.Remove(unitPath)
		_, _ = n.run(ctx, "systemctl", "--user", "daemon-reload")
		return err
	}
	return nil
}

func (n *native) Stop(ctx context.Context, j Job) error {
	loaded, err := n.Loaded(ctx, j)
	if err != nil || !loaded {
		if err == nil && n.platform == "linux" && n.userUnitDir != "" {
			_ = os.Remove(n.linuxUnitPath(j))
		}
		return err
	}
	if n.platform == "darwin" {
		_, err = n.run(ctx, "launchctl", "bootout", target(j))
	} else {
		_, err = n.run(ctx, "systemctl", "--user", "stop", j.Label+".service")
	}
	if err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err = n.Loaded(ctx, j)
		if err != nil {
			return err
		}
		if !loaded {
			if n.platform == "linux" && n.userUnitDir != "" {
				_ = os.Remove(n.linuxUnitPath(j))
				_, _ = n.run(ctx, "systemctl", "--user", "daemon-reload")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("service %s did not stop", j.Label)
}

func systemdEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`$`, `$$`,
		`%`, `%%`,
		"\n", `\n`,
		"\r", `\r`,
	)
	return `"` + r.Replace(s) + `"`
}

func systemdUnit(j Job, installable bool) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=AXV %s service\n", j.Role)
	b.WriteString("After=network-online.target\n\n[Service]\n")
	b.WriteString("Type=exec\n")
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=2s\n")
	b.WriteString("TimeoutStopSec=15s\n")
	// The desktop reaps ephemeral shells itself; persistent tmux/Codex/VMs must
	// outlive service stop/restart.
	b.WriteString("KillMode=process\n")
	if j.Log != "" {
		fmt.Fprintf(&b, "StandardOutput=append:%s\n", j.Log)
		fmt.Fprintf(&b, "StandardError=append:%s\n", j.Log)
	}
	keys := make([]string, 0, len(j.Environment))
	for k := range j.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "Environment=%s\n", systemdEscape(k+"="+j.Environment[k]))
	}
	quotedArgs := make([]string, len(j.Args))
	for i, arg := range j.Args {
		quotedArgs[i] = systemdEscape(arg)
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(quotedArgs, " "))
	if installable {
		b.WriteString("\n[Install]\nWantedBy=default.target\n")
	}
	return b.String()
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func plist(j Job) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n")
	pair := func(k, v string) { fmt.Fprintf(&b, "<key>%s</key><string>%s</string>\n", xmlText(k), xmlText(v)) }
	pair("Label", j.Label)
	b.WriteString("<key>ProgramArguments</key><array>")
	for _, arg := range j.Args {
		fmt.Fprintf(&b, "<string>%s</string>", xmlText(arg))
	}
	b.WriteString("</array>\n")
	b.WriteString("<key>EnvironmentVariables</key><dict>")
	keys := make([]string, 0, len(j.Environment))
	for k := range j.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pair(k, j.Environment[k])
	}
	b.WriteString("</dict>\n")
	pair("WorkingDirectory", filepath.Dir(j.Definition))
	pair("StandardOutPath", j.Log)
	pair("StandardErrorPath", j.Log)
	b.WriteString("<key>Umask</key><integer>63</integer><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>3</integer>\n")
	// Provider processes and VMs outlive host control service restarts.
	if j.Role == "host" {
		b.WriteString("<key>AbandonProcessGroup</key><true/>\n")
	}
	b.WriteString("</dict></plist>\n")
	return b.String()
}
