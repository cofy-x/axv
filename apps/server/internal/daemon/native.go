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
	platform string
	run      func(context.Context, string, ...string) (string, error)
}

func nativeSupervisor() (supervisor, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("background services currently require macOS launchd or Linux systemd; use axv serve")
	}
	return &native{platform: runtime.GOOS, run: func(ctx context.Context, name string, args ...string) (string, error) {
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
	args := []string{"--user", "--unit=" + j.Label, "--collect", "--expand-environment=no", "--property=Type=exec", "--property=Restart=on-failure", "--property=RestartSec=2s", "--property=TimeoutStopSec=15s", "--property=StandardOutput=append:" + j.Log, "--property=StandardError=append:" + j.Log}
	// The desktop reaps ephemeral shells and attachment clients itself.
	// A tmux server started by it must outlive stop/restart, just like VMs.
	args = append(args, "--property=KillMode=process")
	keys := make([]string, 0, len(j.Environment))
	for k := range j.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--setenv="+k+"="+j.Environment[k])
	}
	args = append(args, "--")
	args = append(args, j.Args...)
	_, err := n.run(ctx, "systemd-run", args...)
	return err
}

func (n *native) Stop(ctx context.Context, j Job) error {
	loaded, err := n.Loaded(ctx, j)
	if err != nil || !loaded {
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
