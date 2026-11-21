package prepare

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	defaultFirecrackerSocket = "/run/axv-host/control.sock"
	defaultFirecrackerConfig = "/etc/axv/host.json"
	localServiceProbeTimeout = 1500 * time.Millisecond
	localServiceProbeTTL     = 15 * time.Second

	localServiceMissing     = "missing"
	localServicePermission  = "permission_denied"
	localServiceUnavailable = "unavailable"
	localServiceReady       = "ready"
)

type linuxServiceDiagnostic struct {
	mu             sync.Mutex
	standard       localSocketProbe
	configured     localSocketProbe
	systemdChecked time.Time
	systemdState   string
}

type localSocketProbe struct {
	file      fs.FileInfo
	checkedAt time.Time
	result    LocalServiceCheck
}

func (d *linuxServiceDiagnostic) check(ctx context.Context, socket, config string) LocalServiceCheck {
	ctx, cancel := context.WithTimeout(ctx, localServiceProbeTimeout)
	defer cancel()
	d.mu.Lock()
	defer d.mu.Unlock()

	standard := d.standard.check(ctx, socket)
	if standard.Status != localServiceMissing {
		d.systemdChecked = time.Time{}
	}
	if standard.Status == localServiceReady || ctx.Err() != nil {
		return standard
	}
	return d.checkConfig(ctx, standard, config)
}

func (d *linuxServiceDiagnostic) checkConfig(ctx context.Context, standard LocalServiceCheck, config string) LocalServiceCheck {
	// An administrator may use a different socket in the default config.
	// Only read a regular file rather than waiting on a pipe or device.
	info, err := os.Stat(config)
	if errors.Is(err, fs.ErrNotExist) {
		return d.withSystemdState(ctx, standard)
	}
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("the local Firecracker configuration is not a regular file: %s", config)
	}
	var configuredSocket string
	if err == nil {
		configuredSocket, err = localServiceSocket(Request{HostConfig: config})
	}
	if err != nil {
		return d.configFailure(ctx, standard, config, info, err)
	}
	if configuredSocket == standard.Socket {
		return d.withSystemdState(ctx, standard)
	}
	configured := d.configured.check(ctx, configuredSocket)
	if configured.Status == localServiceReady || standard.Status == localServiceMissing {
		return configured
	}
	return standard
}

func (d *linuxServiceDiagnostic) configFailure(ctx context.Context, standard LocalServiceCheck, config string, info fs.FileInfo, err error) LocalServiceCheck {
	if standard.Status != localServiceMissing {
		return standard
	}
	failure := LocalServiceCheck{Status: localServiceUnavailable, Socket: standard.Socket, Detail: err.Error()}
	if errors.Is(err, fs.ErrPermission) {
		failure.Status = localServicePermission
		failure.Detail = "The AXV desktop account cannot read " + config + ". Ask the host administrator for the service socket path."
		// The host example installs mode 0600. The config error and a missing
		// default socket are independent observations for a desktop account.
		if info != nil && info.Mode().IsRegular() {
			failure.SystemdState = d.withSystemdState(ctx, standard).SystemdState
		}
	}
	return failure
}

// A missing default socket alone cannot distinguish an uninstalled service from
// a stopped one. Read the example unit only when the socket check needs that hint.
func (d *linuxServiceDiagnostic) withSystemdState(ctx context.Context, result LocalServiceCheck) LocalServiceCheck {
	if result.Socket != defaultFirecrackerSocket || result.Status != localServiceMissing {
		return result
	}
	if ctx.Err() != nil {
		return result
	}
	if time.Since(d.systemdChecked) >= localServiceProbeTTL {
		d.systemdState = readSystemdState(ctx)
		if !errors.Is(ctx.Err(), context.Canceled) {
			d.systemdChecked = time.Now()
		}
	}
	result.SystemdState = d.systemdState
	return result
}

func readSystemdState(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "systemctl", "--system", "show", "--property=LoadState,ActiveState", "--no-pager", "axv-host.service")
	cmd.WaitDelay = 200 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var load, active string
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "LoadState":
			load = value
		case "ActiveState":
			active = value
		}
	}
	if load == "not-found" && active == "inactive" {
		return "not_installed"
	}
	if load == "loaded" {
		switch active {
		case "inactive", "failed", "active":
			return active
		}
	}
	return ""
}

func (p *localSocketProbe) check(ctx context.Context, socket string) LocalServiceCheck {
	file, err := os.Stat(socket)
	if err != nil {
		*p = localSocketProbe{}
		return localServiceFailure(socket, err)
	}
	if file.Mode()&fs.ModeSocket == 0 {
		*p = localSocketProbe{}
		return localServiceFailure(socket, fmt.Errorf("the local Firecracker path is not a Unix socket: %s", socket))
	}
	// The preparation check is polled. Reuse the provider probe briefly, while
	// detecting immediately when the socket is removed or replaced.
	if p.file != nil && p.result.Socket == socket && os.SameFile(p.file, file) &&
		p.file.Mode() == file.Mode() && p.file.ModTime().Equal(file.ModTime()) &&
		time.Since(p.checkedAt) < localServiceProbeTTL {
		return p.result
	}
	result := LocalServiceCheck{Status: localServiceReady, Socket: socket, Detail: "The local Firecracker service is reachable and compatible."}
	if _, err = probeLocalService(ctx, socket, true); err != nil {
		result = localServiceFailure(socket, err)
	}
	if result.Status != localServiceMissing && result.Status != localServicePermission && !errors.Is(err, context.Canceled) {
		*p = localSocketProbe{file: file, checkedAt: time.Now(), result: result}
	}
	return result
}

func localServiceFailure(socket string, err error) LocalServiceCheck {
	result := LocalServiceCheck{Status: localServiceUnavailable, Socket: socket, Detail: err.Error()}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		result.Status = localServiceMissing
		result.Detail = "Ask the host administrator to set up or start the Firecracker service, then check again."
	case errors.Is(err, fs.ErrPermission):
		result.Status = localServicePermission
		result.Detail = "Ask the host administrator to grant the AXV desktop account access to the local service socket."
	}
	return result
}
