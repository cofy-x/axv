// Package doctor performs read-only environment, service and hygiene checks.
package doctor

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

	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

const (
	StatusOK   = "ok"
	StatusInfo = "info"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// Check represents a single read-only diagnostic check result.
type Check struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix,omitempty"`
}

// Report is the structured output of `axv doctor`.
type Report struct {
	Platform    string  `json:"platform"`
	Arch        string  `json:"arch"`
	Version     string  `json:"version"`
	Executable  string  `json:"executable,omitempty"`
	ProfileHome string  `json:"profileHome"`
	Checks      []Check `json:"checks"`
	OK          int     `json:"ok"`
	Info        int     `json:"info"`
	Warn        int     `json:"warn"`
	Fail        int     `json:"fail"`
}

func (r *Report) add(c Check) {
	r.Checks = append(r.Checks, c)
	switch c.Status {
	case StatusOK:
		r.OK++
	case StatusInfo:
		r.Info++
	case StatusWarn:
		r.Warn++
	case StatusFail:
		r.Fail++
	}
}

// Run collects read-only diagnostics without creating or mutating profile state.
func Run(ctx context.Context, home, version string) Report {
	exe, _ := os.Executable()
	report := Report{
		Platform:    runtime.GOOS,
		Arch:        runtime.GOARCH,
		Version:     version,
		Executable:  exe,
		ProfileHome: home,
		Checks:      []Check{},
	}
	if ctx.Err() != nil {
		return report
	}

	checkProfile(&report, home)
	checkDesktop(ctx, &report, home)
	checkAutostart(ctx, &report, home)
	checkHostTools(&report)
	checkPlatform(ctx, &report, home, version)
	checkHygiene(ctx, &report, home)
	return report
}

func checkProfile(r *Report, home string) {
	cfgPath := filepath.Join(home, "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		r.add(Check{
			ID:       "profile",
			Category: "profile",
			Label:    "Profile configuration",
			Status:   StatusInfo,
			Detail:   fmt.Sprintf("No config.json yet at %s (will initialize on first launch).", cfgPath),
			Fix:      "axv init",
		})
		return
	}
	p, err := profile.ReadDesktop(home)
	if err != nil {
		r.add(Check{
			ID:       "profile",
			Category: "profile",
			Label:    "Profile configuration",
			Status:   StatusFail,
			Detail:   err.Error(),
		})
		return
	}
	if p.ComputeError != "" {
		r.add(Check{
			ID:       "profile",
			Category: "profile",
			Label:    "Profile configuration",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Desktop config is valid, but compute config has an error: %s", p.ComputeError),
			Fix:      "axv setup",
		})
		return
	}
	r.add(Check{
		ID:       "profile",
		Category: "profile",
		Label:    "Profile configuration",
		Status:   StatusOK,
		Detail:   fmt.Sprintf("%s (compute: %s)", cfgPath, p.Compute.Mode),
	})
}

func checkDesktop(ctx context.Context, r *Report, home string) {
	state, err := desktoplaunch.ReadState(home)
	if os.IsNotExist(err) {
		r.add(Check{
			ID:       "desktop",
			Category: "services",
			Label:    "Desktop service",
			Status:   StatusInfo,
			Detail:   "Stopped.",
			Fix:      "axv daemon start",
		})
		return
	}
	if err != nil {
		r.add(Check{
			ID:       "desktop",
			Category: "services",
			Label:    "Desktop service",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Unreadable desktop state: %v", err),
		})
		return
	}
	if verifyErr := desktoplaunch.Verify(ctx, state); verifyErr != nil {
		r.add(Check{
			ID:       "desktop",
			Category: "services",
			Label:    "Desktop service",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Recorded desktop at %s is unreachable (%v).", state.URL, verifyErr),
			Fix:      "axv daemon restart",
		})
		return
	}
	r.add(Check{
		ID:       "desktop",
		Category: "services",
		Label:    "Desktop service",
		Status:   StatusOK,
		Detail:   fmt.Sprintf("Ready at %s (PID %d).", state.URL, state.PID),
	})
}

func checkAutostart(ctx context.Context, r *Report, home string) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return
	}
	unitPath := autostartPath(home)
	if unitPath == "" {
		return
	}
	if _, err := os.Stat(unitPath); err != nil {
		r.add(Check{
			ID:       "autostart",
			Category: "services",
			Label:    "Desktop autostart",
			Status:   StatusInfo,
			Detail:   "Not enabled for boot/login startup.",
			Fix:      "axv daemon enable",
		})
		return
	}
	if runtime.GOOS == "linux" {
		if checked, enabled := linuxLingerEnabled(ctx); checked && !enabled {
			r.add(Check{
				ID:       "autostart",
				Category: "services",
				Label:    "Desktop autostart",
				Status:   StatusWarn,
				Detail:   fmt.Sprintf("Enabled (%s), but systemd user lingering is disabled; services may stop after SSH logout.", unitPath),
				Fix:      "sudo loginctl enable-linger $USER",
			})
			return
		}
	}
	r.add(Check{
		ID:       "autostart",
		Category: "services",
		Label:    "Desktop autostart",
		Status:   StatusOK,
		Detail:   fmt.Sprintf("Enabled (%s).", unitPath),
	})
}

func autostartPath(home string) string {
	id := profile.Identity(home)
	if runtime.GOOS == "darwin" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(userHome, "Library", "LaunchAgents", "com.axv."+id+".desktop.autostart.plist")
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "systemd", "user", "axv-desktop-"+id+".service")
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(userHome, ".config", "systemd", "user", "axv-desktop-"+id+".service")
}

func linuxLingerEnabled(ctx context.Context) (checked, enabled bool) {
	username := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		username = u.Username
	}
	if username == "" {
		return false, false
	}
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

func checkHostTools(r *Report) {
	for _, tool := range []struct {
		name     string
		label    string
		required bool
		fix      string
	}{
		{"tmux", "tmux (persistent Codex runs)", true, "Install tmux with your system package manager."},
		{"codex", "Codex CLI (host coding agent)", false, "Install the official OpenAI Codex CLI (`npm i -g @openai/codex`)."},
		{"ssh", "OpenSSH client (VM SSH access)", true, "Install openssh-client."},
	} {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			status := StatusInfo
			if tool.required {
				status = StatusWarn
			}
			r.add(Check{
				ID:       "tool_" + tool.name,
				Category: "tools",
				Label:    tool.label,
				Status:   status,
				Detail:   fmt.Sprintf("%s not found on PATH.", tool.name),
				Fix:      tool.fix,
			})
			continue
		}
		r.add(Check{
			ID:       "tool_" + tool.name,
			Category: "tools",
			Label:    tool.label,
			Status:   StatusOK,
			Detail:   path,
		})
	}
}

func checkHygiene(_ context.Context, r *Report, home string) {
	binDir := filepath.Join(home, "bin")
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return
	}
	activeExe := ""
	if state, stateErr := desktoplaunch.ReadState(home); stateErr == nil {
		activeExe = filepath.Clean(state.Executable)
	}
	var count int
	var totalBytes int64
	cutoff := time.Now().Add(-60 * time.Second)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "axv-") {
			continue
		}
		full := filepath.Join(binDir, entry.Name())
		if full == activeExe {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		count++
		totalBytes += info.Size()
	}
	if count == 0 {
		return
	}
	r.add(Check{
		ID:       "hygiene",
		Category: "storage",
		Label:    "Stale runtime artifacts",
		Status:   StatusInfo,
		Detail:   fmt.Sprintf("%d older pinned executable(s) under %s (~%.1f MiB; run `axv gc --dry-run` to inspect).", count, binDir, float64(totalBytes)/(1<<20)),
		Fix:      "axv gc",
	})
}

// WriteText formats a doctor report for terminal output.
func WriteText(out io.Writer, r Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "AXV doctor (%s/%s, version %s)\n", r.Platform, r.Arch, r.Version)
	fmt.Fprintf(&b, "Profile: %s\n\n", r.ProfileHome)
	for _, c := range r.Checks {
		tag := strings.ToUpper(c.Status)
		fmt.Fprintf(&b, "  [%-4s] %s: %s\n", tag, c.Label, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(&b, "         Fix: %s\n", c.Fix)
		}
	}
	fmt.Fprintf(&b, "\nSummary: %d ok, %d info, %d warn, %d fail\n", r.OK, r.Info, r.Warn, r.Fail)
	_, err := io.WriteString(out, b.String())
	return err
}
