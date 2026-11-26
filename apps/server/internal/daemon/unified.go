package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/compute/hostinstall"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
)

// StatusOptions configures unified daemon status reporting.
type StatusOptions struct {
	JSON           bool
	CurrentVersion string
}

// DesktopStatus summarizes the desktop service state.
type DesktopStatus struct {
	Ready      bool   `json:"ready"`
	URL        string `json:"url,omitempty"`
	PID        int    `json:"pid,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	Registered *bool  `json:"registered,omitempty"`
}

// HostStatusSummary summarizes the VM host state across macOS and Linux.
type HostStatusSummary struct {
	Mode          string                     `json:"mode"`
	Ready         bool                       `json:"ready"`
	HostID        string                     `json:"hostId,omitempty"`
	Socket        string                     `json:"socket,omitempty"`
	Registered    *bool                      `json:"registered,omitempty"`
	Error         string                     `json:"error,omitempty"`
	ProviderError string                     `json:"providerError,omitempty"`
	Providers     []compute.Capability       `json:"providers,omitempty"`
	SystemUnit    *hostinstall.ServiceStatus `json:"systemService,omitempty"`
}

// StatusReport is the structured payload emitted by `axv daemon status --json`.
type StatusReport struct {
	Home      string            `json:"home"`
	Platform  string            `json:"platform"`
	Version   string            `json:"version,omitempty"`
	Desktop   DesktopStatus     `json:"desktop"`
	Autostart AutostartInfo     `json:"autostart"`
	RemoteURL string            `json:"remoteUrl,omitempty"`
	VMHost    HostStatusSummary `json:"vmHost"`
}

// StatusWithOptions prints a unified human or JSON status report covering
// Desktop, Autostart/Linger, Remote Access, and macOS/Linux VM Host services.
func StatusWithOptions(ctx context.Context, home string, opts StatusOptions, output io.Writer) error {
	return statusWithOptions(ctx, home, opts, output, nativeSupervisor)
}

func status(ctx context.Context, home string, output io.Writer, supervisorFor func() (supervisor, error)) error {
	return statusWithOptions(ctx, home, StatusOptions{}, output, supervisorFor)
}

func statusWithOptions(ctx context.Context, home string, opts StatusOptions, output io.Writer, supervisorFor func() (supervisor, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	report, err := collectStatus(ctx, home, opts.CurrentVersion, supervisorFor)
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return printStatusReport(report, output)
}

// CollectStatus gathers read-only status across Desktop, Autostart and VM Host.
func CollectStatus(ctx context.Context, home, currentVersion string) (StatusReport, error) {
	return collectStatus(ctx, home, currentVersion, nativeSupervisor)
}

func collectStatus(ctx context.Context, home, currentVersion string, supervisorFor func() (supervisor, error)) (StatusReport, error) {
	if err := ctx.Err(); err != nil {
		return StatusReport{}, err
	}
	report := StatusReport{
		Home:      home,
		Platform:  runtime.GOOS,
		Version:   currentVersion,
		Autostart: AutostartStatus(ctx, home),
	}
	if state, err := desktoplaunch.ReadState(home); err == nil && desktoplaunch.Verify(ctx, state) == nil {
		report.Desktop.Ready = true
		report.Desktop.URL = state.URL
		report.Desktop.PID = state.PID
		report.Desktop.InstanceID = state.InstanceID
		report.RemoteURL = readyRemoteURL(ctx, home)
	}
	m, mErr := readManifest(home)
	if mErr != nil && !os.IsNotExist(mErr) {
		return report, mErr
	}
	var live app.Settings
	var liveErr error
	if mErr == nil {
		sup, supErr := supervisorFor()
		if supErr == nil {
			for _, j := range m.Jobs {
				active, loadErr := sup.Loaded(ctx, j)
				if loadErr == nil {
					v := active
					if j.Role == "desktop" {
						report.Desktop.Registered = &v
					} else if j.Role == "host" {
						report.VMHost.Registered = &v
					}
				}
			}
		}
		if m.OwnedHost != nil {
			live = app.Settings{
				Inputs: desktoplaunch.Inputs{Home: home},
				ComputeSettings: app.ComputeSettings{
					Host:    m.OwnedHost,
					Compute: profile.Compute{Mode: "managed", Socket: m.OwnedHost.Socket},
				},
			}
		} else {
			live, liveErr = app.ResolveCompute(home)
		}
	} else {
		live, liveErr = app.ResolveCompute(home)
	}

	if runtime.GOOS == "linux" {
		sys := hostinstall.Status(ctx, currentVersion)
		report.VMHost.SystemUnit = &sys
	}

	if liveErr != nil {
		report.VMHost.Mode = "error"
		report.VMHost.Error = liveErr.Error()
		return report, ctx.Err()
	}
	hostSummary, err := collectHostStatus(ctx, live)
	hostSummary.Registered = report.VMHost.Registered
	hostSummary.SystemUnit = report.VMHost.SystemUnit
	report.VMHost = hostSummary
	return report, err
}

func collectHostStatus(ctx context.Context, live app.Settings) (HostStatusSummary, error) {
	summary := HostStatusSummary{
		Mode:   live.Compute.Mode,
		Socket: live.Compute.Socket,
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	if live.Compute.Mode == "" || live.Compute.Mode == "disabled" {
		summary.Mode = "disabled"
		return summary, nil
	}
	info, err := app.HostStatus(ctx, live)
	if err != nil {
		summary.Error = err.Error()
		return summary, ctx.Err()
	}
	summary.Ready = true
	summary.HostID = info.HostID
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := computeclient.Connect(probeCtx, live.Compute.Socket, computeclient.Binding{HostID: info.HostID, ConfigFingerprint: info.ConfigFingerprint})
	if err != nil {
		summary.ProviderError = err.Error()
		return summary, ctx.Err()
	}
	defer client.Close()
	inventory, err := client.Info(probeCtx)
	if err != nil {
		summary.ProviderError = err.Error()
		return summary, ctx.Err()
	}
	summary.Providers = inventory.Providers
	return summary, ctx.Err()
}

func printHost(ctx context.Context, s app.Settings, output io.Writer) error {
	summary, err := collectHostStatus(ctx, s)
	printHostStatus(summary, output)
	return err
}

func printHostStatus(h HostStatusSummary, output io.Writer) {
	switch {
	case h.Mode == "disabled" || h.Mode == "":
		if h.SystemUnit != nil && h.SystemUnit.Installed {
			fmt.Fprintf(output, "VM backend: not connected to this profile (run `axv setup --socket %s`)\n", h.SystemUnit.Socket)
		} else {
			fmt.Fprintln(output, "VM backend: not configured (run `axv setup` or open Virtual Machines)")
		}
	case h.Error != "":
		fmt.Fprintf(output, "VM backend: unavailable (%s)\n", h.Error)
	case h.Ready:
		fmt.Fprintf(output, "VM controller: ready (%s)\n", h.HostID)
		if h.ProviderError != "" {
			fmt.Fprintf(output, "VM provider status: unavailable (%s)\n", h.ProviderError)
			return
		}
		for _, p := range h.Providers {
			if p.Available {
				fmt.Fprintf(output, "  %s: available\n", p.Provider)
			} else {
				fmt.Fprintf(output, "  %s: unavailable (%s)\n", p.Provider, p.Reason)
			}
		}
	}
}

func printStatusReport(r StatusReport, output io.Writer) error {
	if r.Desktop.Ready {
		fmt.Fprintf(output, "Desktop: ready at %s (PID %d)\n", r.Desktop.URL, r.Desktop.PID)
	} else {
		fmt.Fprintln(output, "Desktop: stopped or unavailable")
	}
	if r.Desktop.Registered != nil {
		fmt.Fprintf(output, "desktop service registered: %t\n", *r.Desktop.Registered)
	}
	if r.RemoteURL != "" {
		fmt.Fprintf(output, "Remote HTTPS: %s\n", r.RemoteURL)
	}
	if r.Autostart.Supported {
		if r.Autostart.Enabled {
			lingerNote := ""
			if r.Autostart.LingerChecked && !r.Autostart.LingerEnabled {
				lingerNote = " (linger disabled; run `sudo loginctl enable-linger`)"
			}
			fmt.Fprintf(output, "Autostart: enabled%s\n", lingerNote)
		} else {
			fmt.Fprintln(output, "Autostart: disabled")
		}
	}
	if r.VMHost.Registered != nil {
		fmt.Fprintf(output, "host service registered: %t\n", *r.VMHost.Registered)
	}
	if r.VMHost.SystemUnit != nil && r.VMHost.SystemUnit.Installed {
		sys := r.VMHost.SystemUnit
		enabledText := "disabled"
		if sys.Enabled {
			enabledText = "enabled"
		}
		fmt.Fprintf(output, "Linux VM service (%s): %s (boot autostart: %s, socket: %s)\n", hostinstall.UnitName, sys.State, enabledText, sys.Socket)
		if sys.VersionDrift {
			fmt.Fprintf(output, "  Warning: VM service runs AXV %s while this CLI is %s. Run `sudo axv daemon upgrade` to sync.\n", sys.InstalledVersion, sys.CurrentVersion)
		}
	}
	printHostStatus(r.VMHost, output)
	return nil
}

// LogsOptions configures daemon log inspection and live tailing.
type LogsOptions struct {
	Follow  bool
	Lines   int
	Desktop bool
	VM      bool
}

// LogsWithOptions prints or follows Desktop and/or VM Host logs across platforms.
func LogsWithOptions(ctx context.Context, home string, opts LogsOptions, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	showDesktop := opts.Desktop || !opts.VM
	showVM := opts.VM || !opts.Desktop

	if opts.Follow {
		if opts.VM && !opts.Desktop && runtime.GOOS == "linux" {
			return hostinstall.StreamLogs(ctx, true, opts.Lines, output)
		}
		target := filepath.Join(home, "logs", "daemon.log")
		if opts.VM && !opts.Desktop {
			target = filepath.Join(home, "logs", "host.log")
		}
		return followFile(ctx, target, opts.Lines, output)
	}

	if showDesktop {
		if b, err := logs.Tail(home, 64<<10); err == nil {
			fmt.Fprintf(output, "--- %s ---\n", filepath.Join(home, "logs", "daemon.log"))
			if _, err = output.Write(limitTailLines(b, opts.Lines)); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if b, err := logs.TailRole(home, "desktop", 64<<10); err == nil {
			fmt.Fprintf(output, "--- %s ---\n", filepath.Join(home, "logs", "desktop.log"))
			if _, err = output.Write(limitTailLines(b, opts.Lines)); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	if showVM {
		if b, err := logs.TailRole(home, "host", 64<<10); err == nil {
			fmt.Fprintf(output, "--- %s ---\n", filepath.Join(home, "logs", "host.log"))
			if _, err = output.Write(limitTailLines(b, opts.Lines)); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if runtime.GOOS == "linux" {
			sys := hostinstall.Status(ctx, "")
			if sys.Installed {
				fmt.Fprintf(output, "--- systemd: %s ---\n", hostinstall.UnitName)
				lines := opts.Lines
				if lines <= 0 {
					lines = 80
				}
				if err := hostinstall.StreamLogs(ctx, false, lines, output); err != nil && opts.VM {
					return err
				}
			}
		}
	}
	return nil
}

func limitTailLines(data []byte, lines int) []byte {
	if lines <= 0 || len(data) == 0 {
		return data
	}
	trimmed := bytes.TrimRight(data, "\n")
	parts := bytes.Split(trimmed, []byte("\n"))
	if len(parts) <= lines {
		return data
	}
	out := bytes.Join(parts[len(parts)-lines:], []byte("\n"))
	return append(out, '\n')
}

func followFile(ctx context.Context, path string, lines int, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open log %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	start := info.Size() - (64 << 10)
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	initial, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if start > 0 {
		if idx := bytes.IndexByte(initial, '\n'); idx >= 0 {
			initial = initial[idx+1:]
		}
	}
	if len(initial) > 0 {
		if _, err := out.Write(limitTailLines(initial, lines)); err != nil {
			return err
		}
	}
	offset := info.Size()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			currentInfo, statErr := os.Stat(path)
			if statErr != nil {
				continue
			}
			// Handle log rotation or truncation.
			if !os.SameFile(info, currentInfo) || currentInfo.Size() < offset {
				_ = f.Close()
				reopened, openErr := os.Open(path)
				if openErr != nil {
					continue
				}
				f = reopened
				info = currentInfo
				offset = 0
			}
			if currentInfo.Size() == offset {
				continue
			}
			if _, seekErr := f.Seek(offset, io.SeekStart); seekErr != nil {
				continue
			}
			reader := bufio.NewReader(f)
			n, copyErr := io.Copy(out, reader)
			offset += n
			if copyErr != nil && copyErr != io.EOF {
				return copyErr
			}
		}
	}
}

// StartVMHost starts the configured VM host service on macOS (managed Lima) or
// Linux (installed axv-host.service).
func StartVMHost(ctx context.Context, home string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		return hostinstall.StartService(ctx, output)
	}
	s, err := app.ResolveCompute(home)
	if err != nil {
		return err
	}
	if s.Compute.Mode != "managed" || s.Host == nil {
		return fmt.Errorf("VM hosting is not configured in managed mode; run `axv setup` first")
	}
	if _, err := EnsureHost(ctx, s, output); err != nil {
		return err
	}
	fmt.Fprintf(output, "Managed VM host is ready on %s.\n", s.Compute.Socket)
	return nil
}

// StopVMHost stops only the VM host service on macOS (managed host job) or
// Linux (axv-host.service), preserving running VMs and project volumes.
func StopVMHost(ctx context.Context, home string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		return hostinstall.StopService(ctx, output)
	}
	sup, err := nativeSupervisor()
	if err != nil {
		return err
	}
	lock, err := commandLock(ctx, home)
	if err != nil {
		return err
	}
	defer lock.Close()
	m, err := readManifest(home)
	if os.IsNotExist(err) {
		fmt.Fprintln(output, "No managed VM host service is registered for this profile.")
		return nil
	}
	if err != nil {
		return err
	}
	job := m.job("host")
	if job == nil {
		fmt.Fprintln(output, "No managed VM host service is registered for this profile.")
		return nil
	}
	if err := sup.Stop(ctx, *job); err != nil {
		return err
	}
	fmt.Fprintln(output, "Managed VM host service stopped. Running VMs and project volumes are preserved.")
	return nil
}

// RestartVMHost restarts only the VM host service while preserving running VMs.
func RestartVMHost(ctx context.Context, home string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		return hostinstall.RestartService(ctx, output)
	}
	if err := StopVMHost(ctx, home, output); err != nil {
		return err
	}
	return StartVMHost(ctx, home, output)
}

// StopDesktopOnly stops only the desktop service while leaving any managed VM
// host running.
func StopDesktopOnly(ctx context.Context, home string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sup, err := nativeSupervisor()
	if err != nil {
		return err
	}
	lock, err := commandLock(ctx, home)
	if err != nil {
		return err
	}
	defer lock.Close()
	m, err := readManifest(home)
	if os.IsNotExist(err) {
		fmt.Fprintln(output, "No AXV desktop service registered for this profile.")
		return nil
	}
	if err != nil {
		return err
	}
	job := m.job("desktop")
	if job == nil {
		fmt.Fprintln(output, "No AXV desktop service registered for this profile.")
		return nil
	}
	if err := sup.Stop(ctx, *job); err != nil {
		return err
	}
	fmt.Fprintln(output, "AXV desktop service stopped. Persistent Codex sessions, VM host and VMs are retained.")
	return nil
}

// Upgrade applies the currently running axv binary to installed services
// (Linux axv-host.service and/or the running desktop daemon).
func Upgrade(ctx context.Context, settings app.Settings, vmOnly, desktopOnly bool, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !desktopOnly && runtime.GOOS == "linux" {
		sys := hostinstall.Status(ctx, "")
		if sys.Installed {
			if err := hostinstall.UpgradeService(ctx, output); err != nil {
				return err
			}
		} else if vmOnly {
			return fmt.Errorf("Linux VM service (%s) is not installed; run `sudo axv host install-service` first", hostinstall.UnitName)
		}
	}
	if vmOnly {
		if runtime.GOOS != "linux" {
			return RestartVMHost(ctx, settings.Home, output)
		}
		return nil
	}
	// If a desktop daemon is currently registered or running, restart it to adopt
	// the current executable.
	if state, err := desktoplaunch.ReadState(settings.Home); err == nil && desktoplaunch.Verify(ctx, state) == nil {
		if state.LaunchID == "" {
			fmt.Fprintln(output, "Desktop is running in foreground (`axv serve`); restart it in its terminal to apply this executable.")
			return nil
		}
		return Restart(ctx, settings, output, false)
	}
	if !strings.EqualFold(runtime.GOOS, "linux") {
		fmt.Fprintln(output, "No running background desktop found. The next `axv` or `axv daemon start` will use this executable.")
	}
	return nil
}
