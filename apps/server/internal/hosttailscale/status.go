// Package hosttailscale reads the host's Tailscale status without configuring it.
// The host daemon is independent of AXV's embedded remote-access node.
package hosttailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type State string

const (
	StateNotInstalled State = "not-installed"
	StateUnavailable  State = "unavailable"
	StateConnected    State = "connected"
	StateDisconnected State = "disconnected"
	StateNeedsLogin   State = "needs-login"
)

// Status contains only host connection information. Installed means that the
// Tailscale command was found; a missing command cannot rule out a GUI install.
// DaemonAvailable is present only after a successful daemon status query.
type Status struct {
	State           State    `json:"state"`
	Installed       bool     `json:"installed"`
	DaemonAvailable *bool    `json:"daemonAvailable,omitempty"`
	BackendState    string   `json:"backendState,omitempty"`
	Hostname        string   `json:"hostname,omitempty"`
	IPs             []string `json:"ips,omitempty"`
	Message         string   `json:"message,omitempty"`
}

// Detector coalesces readers and caches a bounded, read-only status query.
// Each query belongs to the desktop lifecycle, not its first HTTP requester.
type Detector struct {
	ctx     context.Context
	cancel  context.CancelFunc
	find    func() (string, error)
	run     func(context.Context, string) ([]byte, error)
	now     func() time.Time
	timeout time.Duration
	ttl     time.Duration

	mu       sync.Mutex
	cached   bool
	checked  time.Time
	status   Status
	inflight chan struct{}
}

func New(ctx context.Context) *Detector {
	ctx, cancel := context.WithCancel(ctx)
	return &Detector{ctx: ctx, cancel: cancel, find: findCommand, run: runStatus, now: time.Now, timeout: 2 * time.Second, ttl: 15 * time.Second}
}

// Close cancels and joins any active command without changing the host daemon.
func (d *Detector) Close() {
	d.cancel()
	d.mu.Lock()
	inflight := d.inflight
	d.mu.Unlock()
	if inflight != nil {
		<-inflight
	}
}

func unavailable(installed bool, message string) Status {
	return Status{State: StateUnavailable, Installed: installed, Message: message}
}

func clone(status Status) Status {
	status.IPs = slices.Clone(status.IPs)
	if status.DaemonAvailable != nil {
		value := *status.DaemonAvailable
		status.DaemonAvailable = &value
	}
	return status
}

func (d *Detector) Status(ctx context.Context) Status {
	for {
		if ctx.Err() != nil || d.ctx.Err() != nil {
			return unavailable(false, "Tailscale status is unavailable.")
		}
		d.mu.Lock()
		if ctx.Err() != nil || d.ctx.Err() != nil {
			d.mu.Unlock()
			return unavailable(false, "Tailscale status is unavailable.")
		}
		if d.cached && d.now().Sub(d.checked) < d.ttl {
			status := clone(d.status)
			d.mu.Unlock()
			return status
		}
		if d.inflight == nil {
			d.inflight = make(chan struct{})
			go d.refresh(d.inflight)
		}
		inflight := d.inflight
		d.mu.Unlock()
		select {
		case <-inflight:
		case <-ctx.Done():
			return unavailable(false, "Tailscale status is unavailable.")
		case <-d.ctx.Done():
			return unavailable(false, "Tailscale status is unavailable.")
		}
	}
}

func (d *Detector) refresh(done chan struct{}) {
	ctx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()
	status := d.inspect(ctx)
	d.mu.Lock()
	d.status, d.checked, d.cached = status, d.now(), true
	d.inflight = nil
	close(done)
	d.mu.Unlock()
}

func (d *Detector) inspect(ctx context.Context) Status {
	command, err := d.find()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return Status{State: StateNotInstalled, Message: "Tailscale command was not found. A desktop app may still be installed."}
		}
		return unavailable(false, "The Tailscale command could not be checked.")
	}
	data, err := d.run(ctx, command)
	if err != nil {
		return unavailable(true, "The host Tailscale daemon could not be queried. It may be stopped or inaccessible.")
	}
	return decodeStatus(data)
}

func decodeStatus(data []byte) Status {
	// Deliberately decode an allowlist rather than ipnstate.Status: AuthURL,
	// users, peers and private daemon diagnostics never enter this projection.
	var input struct {
		BackendState string
		TailscaleIPs []string
		Self         *struct {
			HostName     string
			Online       bool
			TailscaleIPs []string
		}
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return unavailable(true, "The host Tailscale command returned an invalid status response.")
	}
	status := Status{Installed: true, BackendState: input.BackendState}
	available := true
	status.DaemonAvailable = &available
	switch input.BackendState {
	case "Running":
		status.State = StateConnected
		if input.Self == nil || !input.Self.Online {
			status.State = StateDisconnected
			status.Message = "The host Tailscale node is offline."
		}
	case "Stopped":
		status.State = StateDisconnected
	case "NeedsLogin":
		status.State = StateNeedsLogin
		status.Message = "Sign in using Tailscale on the host computer."
	case "NeedsMachineAuth":
		status.State = StateNeedsLogin
		status.Message = "The host Tailscale device is waiting for administrator approval."
	case "Starting", "NoState":
		status.State = StateDisconnected
		status.Message = "The host Tailscale connection is starting."
	default:
		return unavailable(true, "The host Tailscale command returned an unsupported status response.")
	}
	ips := input.TailscaleIPs
	if input.Self != nil {
		name := input.Self.HostName
		if len(name) <= 253 && utf8.ValidString(name) && strings.IndexFunc(name, unicode.IsControl) == -1 {
			status.Hostname = name
		}
		if len(input.Self.TailscaleIPs) != 0 {
			ips = input.Self.TailscaleIPs
		}
	}
	for _, value := range ips {
		if len(status.IPs) == 16 {
			break
		}
		if ip, err := netip.ParseAddr(value); err == nil && ip.Zone() == "" && !slices.Contains(status.IPs, ip.String()) {
			status.IPs = append(status.IPs, ip.String())
		}
	}
	return status
}

func findCommand() (string, error) {
	return findExecutable(runtime.GOOS, exec.LookPath, os.Stat, os.UserHomeDir, os.Getenv)
}

func findExecutable(platform string, lookPath func(string) (string, error), stat func(string) (os.FileInfo, error), home func() (string, error), getenv func(string) string) (string, error) {
	path, err := lookPath("tailscale")
	if err == nil {
		return filepath.Abs(path)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		return "", err
	}
	var candidates []string
	switch platform {
	case "darwin":
		candidates = []string{"/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/opt/homebrew/bin/tailscale", "/usr/local/bin/tailscale"}
		if directory, err := home(); err == nil && filepath.IsAbs(directory) {
			candidates = append(candidates, filepath.Join(directory, "Applications/Tailscale.app/Contents/MacOS/Tailscale"))
		}
	case "windows":
		for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
			if directory := getenv(key); directory != "" && filepath.IsAbs(directory) {
				candidates = append(candidates, filepath.Join(directory, "Tailscale", "tailscale.exe"))
			}
		}
	default:
		candidates = []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale", "/snap/bin/tailscale"}
	}
	var failure error
	for _, candidate := range candidates {
		info, err := stat(candidate)
		if err == nil && info.Mode().IsRegular() && (platform == "windows" || info.Mode().Perm()&0111 != 0) {
			return candidate, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failure = err
		}
	}
	if failure != nil {
		return "", failure
	}
	return "", exec.ErrNotFound
}
