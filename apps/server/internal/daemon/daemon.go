package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
)

type manifest struct {
	Version   int                     `json:"version"`
	Home      string                  `json:"home"`
	Desktop   *desktoplaunch.Snapshot `json:"desktop,omitempty"`
	Jobs      []Job                   `json:"jobs"`
	OwnedHost *compute.Config         `json:"ownedHost,omitempty"`
}

func manifestPath(home string) string { return filepath.Join(home, "run", "daemon.json") }
func readManifest(home string) (manifest, error) {
	var m manifest
	err := jsonfile.Read(manifestPath(home), &m)
	if err == nil && (m.Version != 4 || m.Home != home || len(m.Jobs) == 0 || len(m.Jobs) > 2) {
		return m, fmt.Errorf("invalid or unsupported daemon ownership record")
	}

	if err != nil {
		return m, err
	}
	roles := map[string]bool{}
	for _, j := range m.Jobs {
		label := jobLabel(home, j.Role, j.Marker)
		command, flag := "_desktop", "--snapshot"
		if j.Role == "host" {
			command, flag = "host", "--config"
			if m.OwnedHost == nil || !filepath.IsAbs(m.OwnedHost.Socket) {
				return m, fmt.Errorf("invalid managed host ownership record")
			}
		}
		if (j.Role != "desktop" && j.Role != "host") || roles[j.Role] || j.Label != label || len(j.Args) != 4 || j.Args[1] != command || j.Args[2] != flag || j.Args[3] != j.Marker || filepath.Dir(j.Args[0]) != filepath.Join(home, "bin") || !strings.HasPrefix(filepath.Base(j.Args[0]), "axv-") || filepath.Dir(j.Marker) != filepath.Join(home, "run") || j.Definition != filepath.Join(home, "run", label+".plist") || j.Log != filepath.Join(home, "logs", j.Role+".log") || j.Environment["AXV_HOME"] != home {
			return m, fmt.Errorf("invalid daemon job ownership record")
		}
		roles[j.Role] = true
	}
	if roles["desktop"] != (m.Desktop != nil) || roles["host"] != (m.OwnedHost != nil) {
		return m, fmt.Errorf("daemon ownership settings do not match registered roles")
	}
	if m.Desktop != nil && (m.Desktop.Home != home || m.Desktop.LaunchID == "") {
		return m, fmt.Errorf("invalid desktop launch identity")
	}
	return m, nil
}

func (m manifest) job(role string) *Job {
	for i := range m.Jobs {
		if m.Jobs[i].Role == role {
			return &m.Jobs[i]
		}
	}
	return nil
}

func (m manifest) retainHost(previous manifest) manifest {
	if job := previous.job("host"); job != nil {
		m.OwnedHost = previous.OwnedHost
		m.Jobs = append([]Job{*job}, m.Jobs...)
	}
	return m
}

func Start(ctx context.Context, s app.Settings, output io.Writer, open bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	supervisor, err := nativeSupervisor()
	if err != nil {
		return err
	}
	return start(ctx, s, output, open, supervisor)
}

func start(ctx context.Context, s app.Settings, output io.Writer, open bool, supervisor supervisor) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := commandLock(ctx, s.Home)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := s.Prepare(); err != nil {
		return err
	}
	if state, err := desktoplaunch.ReadState(s.Home); err == nil && desktoplaunch.Verify(ctx, state) == nil {
		if state.Fingerprint != s.Fingerprint() {
			return fmt.Errorf("AXV is running with different settings; stop it before changing the profile")
		}
		fmt.Fprintf(output, "AXV already running: %s\n", state.URL)
		if open {
			return OpenBrowser(ctx, state.URL)
		}
		return nil
	}
	m, err := readManifest(s.Home)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	reuse := false
	if err == nil {
		if job := m.job("desktop"); job != nil {
			active, err := supervisor.Loaded(ctx, *job)
			if err != nil {
				return err
			}
			reuse = active
		}
		if reuse && m.Desktop.Fingerprint() != s.Fingerprint() {
			return fmt.Errorf("background services use different settings; run axv daemon stop before starting this profile")
		}
	}
	if !reuse {
		listener, err := net.Listen("tcp", s.Desktop.Addr)
		if err != nil {
			return fmt.Errorf("desktop address %s is unavailable: %w", s.Desktop.Addr, err)
		}
		_ = listener.Close()
		replacement, err := newManifest(s)
		if err != nil {
			return err
		}
		m = replacement.retainHost(m)
		if err = jsonfile.Write(manifestPath(s.Home), m, 0600); err != nil {
			return err
		}
	}
	var started []Job
	success := false
	defer func() {
		if !success {
			for i := len(started) - 1; i >= 0; i-- {
				if err := stopLaunchJob(ctx, supervisor, started[i]); err != nil {
					result = errors.Join(result, err)
					fmt.Fprintf(output, "Startup cleanup: %v\n", err)
				}
			}
		}
	}()
	for _, j := range m.Jobs {
		if j.Role != "desktop" {
			continue
		}
		loaded, err := supervisor.Loaded(ctx, j)
		if err != nil {
			return err
		}
		if !loaded {
			if err = supervisor.Start(ctx, j); err != nil {
				return errors.Join(err, cleanupFailedLaunch(ctx, supervisor, j))
			}
			started = append(started, j)
		}

	}
	// Activation may register a host after desktop readiness; never hold its
	// command lock while waiting for that desktop.
	lock.Close()
	s.LaunchID = m.Desktop.LaunchID
	state, err := waitDesktop(ctx, s)
	if err != nil {
		return fmt.Errorf("%w; run axv daemon logs", err)
	}
	success = true
	fmt.Fprintf(output, "AXV running: %s\nManaged for this login session; automatic login startup is not enabled.\n", state.URL)
	fmt.Fprintln(output, "VM preparation continues in Virtual Machines.")
	if open {
		return OpenBrowser(ctx, state.URL)
	}
	return nil
}

func newManifest(s app.Settings) (manifest, error) {
	s.LaunchID = compute.NewID("launch")
	snapshot := s.Snapshot()
	m := manifest{Version: 4, Home: s.Home, Desktop: &snapshot}
	executable, err := os.Executable()
	if err != nil {
		return m, err
	}
	binary, err := profile.PinExecutable(s.Home, executable)
	if err != nil {
		return m, err
	}
	logDir := filepath.Join(s.Home, "logs")
	if err = os.MkdirAll(logDir, 0700); err != nil {
		return m, err
	}
	roles := []string{"desktop"}
	for _, role := range roles {
		marker := filepath.Join(s.RunDir(), compute.NewID(role)+".json")
		args := []string{binary, "_desktop", "--snapshot", marker}
		if err = jsonfile.Write(marker, snapshot, 0600); err != nil {
			return m, err
		}
		label := jobLabel(s.Home, role, marker)
		j := Job{Role: role, Label: label, Args: args, Marker: marker, Definition: filepath.Join(s.RunDir(), label+".plist"), Log: filepath.Join(logDir, role+".log"), Environment: environment(s.Home)}
		if err = logs.PrepareLegacy(s.Home, role); err != nil {
			return m, err
		}
		m.Jobs = append(m.Jobs, j)
	}
	return m, nil
}

// A stopped systemd unit may remain loaded while its persistent tmux server is
// alive. Give each service incarnation its own name so restart can replace the
// executable and snapshot without reusing that still-owned unit.
func jobLabel(home, role, marker string) string {
	return "com.axv." + profile.Identity(home) + "." + role + "." + profile.Identity(marker)
}

func environment(home string) map[string]string {
	env := map[string]string{"AXV_HOME": home}
	for _, key := range []string{"AXV_CACHE_HOME", "AXV_IMAGE_BUNDLE", "PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "TMPDIR"} {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
	return env
}

func waitDesktop(ctx context.Context, s app.Settings) (desktoplaunch.State, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		state, err := desktoplaunch.ReadState(s.Home)
		if err == nil && state.Fingerprint == s.Fingerprint() && state.LaunchID == s.LaunchID && desktoplaunch.Verify(ctx, state) == nil {
			return state, nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return desktoplaunch.State{}, fmt.Errorf("desktop did not become ready: %w", ctx.Err())
}

func Stop(ctx context.Context, home string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	supervisor, err := nativeSupervisor()
	if err != nil {
		return err
	}
	return stop(ctx, home, output, supervisor)
}

func stop(ctx context.Context, home string, output io.Writer, supervisor supervisor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := commandLock(ctx, home)
	if err != nil {
		return err
	}
	defer lock.Close()
	m, err := readManifest(home)
	if os.IsNotExist(err) {
		fmt.Fprintln(output, "No AXV background services registered for this profile.")
		return nil
	}
	if err != nil {
		return err
	}
	hostSettings := app.Settings{Inputs: desktoplaunch.Inputs{Home: home}}
	if m.Desktop != nil {
		hostSettings = app.SettingsFromSnapshot(*m.Desktop)
	}
	if m.OwnedHost != nil {
		hostSettings.Host = m.OwnedHost
		hostSettings.Compute = profile.Compute{Mode: "managed", Socket: m.OwnedHost.Socket}
	}
	machines, listErr := app.Machines(ctx, hostSettings)
	for i := len(m.Jobs) - 1; i >= 0; i-- {
		if err = supervisor.Stop(ctx, m.Jobs[i]); err != nil {
			return err
		}
	}
	fmt.Fprintln(output, "AXV background services stopped. Persistent Codex sessions, VMs and project volumes are retained.")
	ownsHost := false
	for _, j := range m.Jobs {
		if j.Role == "host" {
			ownsHost = true
		}
	}
	if ownsHost {
		if listErr == nil {
			running := 0
			for _, machine := range machines {
				if machine.Status == "running" {
					running++
				}
			}
			fmt.Fprintf(output, "Running VMs before shutdown: %d. Task expiry enforcement resumes when the host service starts.\n", running)
		} else {
			fmt.Fprintln(output, "VM state could not be queried. Task expiry enforcement resumes when the host service starts.")
		}
	}
	return nil
}

// Restart replaces only the desktop job. The independent host and guest sessions remain running.
func Restart(ctx context.Context, s app.Settings, output io.Writer, open bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	supervisor, err := nativeSupervisor()
	if err != nil {
		return err
	}
	return restart(ctx, s, output, open, supervisor)
}

func restart(ctx context.Context, s app.Settings, output io.Writer, open bool, supervisor supervisor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := commandLock(ctx, s.Home)
	if err != nil {
		return err
	}
	defer lock.Close()
	m, err := readManifest(s.Home)
	if os.IsNotExist(err) {
		lock.Close()
		return start(ctx, s, output, open, supervisor)
	}
	if err != nil {
		return err
	}
	if m.Desktop == nil {
		return fmt.Errorf("this profile has only a VM host; daemon restart replaces the desktop only; run axv or axv daemon start to add a desktop")
	}
	replacement, err := newManifest(s)
	if err != nil {
		return err
	}
	for i, j := range m.Jobs {
		if j.Role != "desktop" {
			continue
		}
		if err = supervisor.Stop(ctx, j); err != nil {
			return err
		}
		if err = waitDesktopStopped(ctx, s.Home); err != nil {
			return err
		}
		for _, newJob := range replacement.Jobs {
			if newJob.Role == "desktop" {
				m.Jobs[i] = newJob
			}
		}
	}
	m.Desktop = replacement.Desktop
	if err = jsonfile.Write(manifestPath(s.Home), m, 0600); err != nil {
		return err
	}
	lock.Close()
	return start(ctx, s, output, open, supervisor)
}

func Status(ctx context.Context, home string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return status(ctx, home, output, nativeSupervisor)
}

func status(ctx context.Context, home string, output io.Writer, supervisorFor func() (supervisor, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := desktoplaunch.ReadState(home)
	if err == nil && desktoplaunch.Verify(ctx, state) == nil {
		fmt.Fprintf(output, "Desktop: ready at %s (PID %d)\n", state.URL, state.PID)
	} else {
		fmt.Fprintln(output, "Desktop: stopped or unavailable")
	}
	m, err := readManifest(home)
	if os.IsNotExist(err) {
		s, readErr := app.ResolveCompute(home)
		if readErr != nil {
			return readErr
		}
		return printHost(ctx, s, output)
	}
	if err != nil {
		return err
	}
	supervisor, err := supervisorFor()
	if err != nil {
		return err
	}
	for _, j := range m.Jobs {
		active, err := supervisor.Loaded(ctx, j)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "%s service registered: %t\n", j.Role, active)
	}
	live, liveErr := app.ResolveCompute(home)
	if m.OwnedHost != nil {
		// Status follows recorded ownership even if configuration was edited.
		live, liveErr = app.Settings{Inputs: desktoplaunch.Inputs{Home: home}, ComputeSettings: app.ComputeSettings{Host: m.OwnedHost, Compute: profile.Compute{Mode: "managed", Socket: m.OwnedHost.Socket}}}, nil
	}
	if liveErr != nil {
		fmt.Fprintf(output, "VM backend: unavailable (%v)\n", liveErr)
	} else {
		return printHost(ctx, live, output)
	}
	return nil
}

func printHost(ctx context.Context, s app.Settings, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Compute.Mode == "disabled" {
		fmt.Fprintln(output, "VM backend: not configured (open Virtual Machines to prepare)")
		return ctx.Err()
	}
	info, err := app.HostStatus(ctx, s)
	if err != nil {
		fmt.Fprintf(output, "VM backend: unavailable (%v)\n", err)
		return ctx.Err()
	}
	fmt.Fprintf(output, "VM controller: ready (%s)\n", info.HostID)
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := computeclient.Connect(probeCtx, s.Compute.Socket, computeclient.Binding{HostID: info.HostID, ConfigFingerprint: info.ConfigFingerprint})
	if err != nil {
		fmt.Fprintf(output, "VM provider status: unavailable (%v)\n", err)
		return ctx.Err()
	}
	defer client.Close()
	inventory, err := client.Info(probeCtx)
	if err != nil {
		fmt.Fprintf(output, "VM provider status: unavailable (%v)\n", err)
		return ctx.Err()
	}
	for _, p := range inventory.Providers {
		if p.Available {
			fmt.Fprintf(output, "  %s: available\n", p.Provider)
		} else {
			fmt.Fprintf(output, "  %s: unavailable (%s)\n", p.Provider, p.Reason)
		}
	}
	return ctx.Err()
}

func Logs(ctx context.Context, home string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := logs.Tail(home, 64<<10)
	if err == nil {
		fmt.Fprintf(output, "--- %s ---\n", filepath.Join(home, "logs", "daemon.log"))
		if _, err = output.Write(b); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// Keep bounded role output available for already-running older hosts and native startup failures.
	for _, role := range []string{"desktop", "host"} {
		data, readErr := logs.TailLegacy(home, role, 64<<10)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		fmt.Fprintf(output, "--- %s ---\n", filepath.Join(home, "logs", role+".log"))
		if _, err := output.Write(data); err != nil {
			return err
		}
	}

	return nil
}

func OpenBrowser(ctx context.Context, url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("open %s in your browser", url)
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("AXV is running; open %s in your browser: %w", url, err)
	}
	return nil
}

func waitDesktopStopped(ctx context.Context, home string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		state, err := desktoplaunch.ReadState(home)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if desktoplaunch.Verify(ctx, state) != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("desktop has not stopped; check axv daemon logs: %w", ctx.Err())
}
