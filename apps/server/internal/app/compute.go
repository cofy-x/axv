package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// HostRunner ensures a managed host and returns cleanup only for a child owned by
// this desktop. Native supervisors return a no-op: their host outlives the UI.
type HostRunner func(context.Context, Settings, io.Writer) (func(), error)

func resolveCompute(s Settings) Settings {
	s.Host = nil
	if s.ComputeOverride != "" {
		s.Compute = profile.Compute{Mode: "external", Socket: s.ComputeOverride}
		s.ComputeError = ""
	}
	if s.Compute.Mode == "" {
		s.Compute.Mode = "disabled"
	}
	if s.ComputeError != "" {
		return s
	}
	if err := profile.ValidateCompute(s.Compute); err != nil {
		s.ComputeError = err.Error()
		return s
	}
	if s.Compute.Mode == "managed" {
		c, err := compute.LoadConfig(s.Compute.HostConfig)
		if err != nil {
			s.ComputeError = err.Error()
			return s
		}
		if c.Firecracker != nil || c.Lima == nil {
			s.ComputeError = "Managed VM hosting requires Lima. Connect to an independently administered Linux service using its local socket."
			return s
		}
		s.Host, s.Compute.Socket = &c, c.Socket
	}
	return s
}

// LiveCompute retains immutable desktop inputs and reads only current compute settings.
func (s Settings) LiveCompute() (Settings, error) {
	p, err := profile.ReadDesktop(s.Home)
	if err != nil {
		return s, err
	}
	s.Compute, s.ComputeError = p.Compute, p.ComputeError
	return resolveCompute(s), nil
}

type bindingRecord struct {
	Socket      string `json:"socket"`
	HostID      string `json:"hostId"`
	Fingerprint string `json:"fingerprint"`
}

// computeCoordinator owns activation and binding publication, independently of
// desktop readiness. The preparation engine calls activatePrepared only after commit.
type computeCoordinator struct {
	ctx           context.Context
	cancel        context.CancelFunc
	settings      Settings
	output        io.Writer
	gateway       *computegateway.Gateway
	runner        HostRunner
	activation    sync.Mutex
	mu            sync.Mutex
	status        computegateway.RuntimeStatus
	stops         []func()
	boundIdentity compute.Runtime
	bound         *Settings
	closed        bool
}

func newComputeCoordinator(ctx context.Context, s Settings, output io.Writer, gateway *computegateway.Gateway, runner HostRunner) *computeCoordinator {
	ctx, cancel := context.WithCancel(ctx)
	c := &computeCoordinator{ctx: ctx, cancel: cancel, settings: s, output: output, gateway: gateway, runner: runner}
	state := "starting"
	if s.Compute.Mode == "disabled" || s.Compute.Mode == "" {
		state = "not_configured"
	}
	c.status = computegateway.RuntimeStatus{State: state, Mode: s.Compute.Mode, ReadOnly: s.ComputeOverride != ""}
	gateway.SetRuntime(c.status)
	go c.monitor()
	return c
}

func (c *computeCoordinator) runtimeStatus() computegateway.RuntimeStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *computeCoordinator) publish(state string, s Settings, err error, info compute.Runtime) {
	c.mu.Lock()
	previous := c.status
	c.status.State, c.status.Mode, c.status.Error, c.status.Code = state, s.Compute.Mode, "", ""
	if err != nil {
		c.status.Error = err.Error()
		c.status.Code = compute.ErrorCode(err)
	}
	if info.HostID != "" {
		c.status.HostID = info.HostID
	}
	status := c.status
	c.mu.Unlock()
	c.gateway.SetRuntime(status)
	if previous.State != status.State || previous.Code != status.Code || previous.HostID != status.HostID || previous.Error != status.Error {
		if err != nil {
			c.settings.Logger.Error("compute.state_changed", err, "state", state, "code", status.Code, "hostId", status.HostID)
		} else {
			c.settings.Logger.Info("compute.state_changed", "state", state, "hostId", status.HostID)
		}
	}
}

func (c *computeCoordinator) bootstrap() {
	if _, err := c.activate(c.ctx, nil); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(c.output, "VM backend unavailable: %v\n", err)
	}
}

func (c *computeCoordinator) activatePrepared(ctx context.Context, activation prepare.Activation) (prepare.Binding, error) {
	return c.activate(ctx, &activation)
}

func (c *computeCoordinator) activate(ctx context.Context, activation *prepare.Activation) (result prepare.Binding, err error) {
	ctx, cancel := context.WithCancel(ctx)
	stopCancellation := context.AfterFunc(c.ctx, cancel)
	defer stopCancellation()
	defer cancel()
	// Only activation is serialized; status reads and desktop shutdown never wait
	// for a profile lock held by a launcher stopping this desktop.
	c.activation.Lock()
	defer c.activation.Unlock()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	s, err := c.settings.LiveCompute()
	if err != nil {
		c.publish("failed", c.settings, err, compute.Runtime{})
		return result, err
	}
	defer func() {
		if err != nil {
			c.publish("failed", s, err, compute.Runtime{})
		}
	}()
	if s.ComputeError != "" {
		return result, fmt.Errorf("%s", s.ComputeError)
	}
	if s.Compute.Mode == "disabled" {
		c.publish("not_configured", s, nil, compute.Runtime{})
		if activation != nil {
			return result, fmt.Errorf("VM hosting is not configured")
		}
		return result, nil
	}
	if activation != nil {
		actual, expected := s.Compute, activation.Compute
		// A managed profile stores the config path; its derived socket is not a profile field.
		if actual.Mode == "managed" {
			actual.Socket, expected.Socket = "", ""
		}
		if actual != expected || (s.Host == nil) != (activation.Host == nil) || s.Host != nil && s.Host.Fingerprint() != activation.Host.Fingerprint() {
			return result, fmt.Errorf("compute configuration changed before activation")
		}
	}
	c.publish("starting", s, nil, compute.Runtime{})
	if s.Compute.Mode == "managed" && c.runner != nil {
		stop, startErr := c.runner(ctx, s, c.output)
		if startErr != nil {
			return result, startErr
		}
		if stop != nil {
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				stop()
				return result, context.Canceled
			}
			c.stops = append(c.stops, stop)
			c.mu.Unlock()
		}
	}
	info, err := HostStatus(ctx, s)
	if err != nil {
		return result, err
	}
	if activation != nil && activation.ExpectedHostID != "" && activation.ExpectedHostID != info.HostID {
		return result, fmt.Errorf("the VM socket belongs to a different host")
	}
	previous := c.runtimeStatus()
	if previous.HostID != "" && previous.HostID != info.HostID {
		return result, fmt.Errorf("the active VM host identity changed; existing views remain bound to their original host")
	}
	var record bindingRecord
	path := filepath.Join(s.RunDir(), "compute-binding.json")
	if readErr := jsonfile.Read(path, &record); readErr != nil && !os.IsNotExist(readErr) {
		return result, readErr
	}
	if record.Socket == s.Compute.Socket && record.Fingerprint == info.ConfigFingerprint && record.HostID != "" && record.HostID != info.HostID {
		return result, fmt.Errorf("the configured VM host identity differs from the previously verified host")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = jsonfile.Write(path, bindingRecord{Socket: s.Compute.Socket, HostID: info.HostID, Fingerprint: info.ConfigFingerprint}, 0600); err != nil {
		return result, err
	}
	generation := c.gateway.Bind(s.Compute.Socket, info.HostID, info.ConfigFingerprint)
	c.mu.Lock()
	c.status.Generation = generation
	c.bound = &s
	c.boundIdentity = info
	c.mu.Unlock()
	c.publish("starting", s, nil, info)
	if err = verifyCapabilities(ctx, s, info); err != nil {
		return result, err
	}
	c.publish("ready", s, nil, info)
	return prepare.Binding{HostID: info.HostID, ProtocolVersion: info.ProtocolVersion, ConfigFingerprint: info.ConfigFingerprint}, nil
}

func (c *computeCoordinator) close() {
	c.cancel()
	c.activation.Lock()
	defer c.activation.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	stops := c.stops
	c.stops = nil
	c.mu.Unlock()
	for i := len(stops) - 1; i >= 0; i-- {
		stops[i]()
	}
}

// Reject a live but incompatible service rather than launching against its socket.
func occupiedHostSocket(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, 250*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Capability probes run once per activation/reconnection, never on the cheap
// identity heartbeat. A recovered controller is not necessarily a usable provider.
func verifyCapabilities(ctx context.Context, s Settings, identity compute.Runtime) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client, err := computeclient.Connect(ctx, s.Compute.Socket, computeclient.Binding{HostID: identity.HostID, ConfigFingerprint: identity.ConfigFingerprint})
	if err != nil {
		return err
	}
	defer client.Close()
	info, err := client.Info(ctx)
	if err != nil {
		return err
	}
	if info.HostID != identity.HostID || info.ConfigFingerprint != identity.ConfigFingerprint || info.ProtocolVersion != identity.ProtocolVersion {
		return compute.Problem("host_unavailable", "The VM service identity changed during activation.")
	}
	available := map[string]bool{}
	var reasons string
	for _, provider := range info.Providers {
		if provider.Available {
			available[provider.Provider] = true
		} else if provider.Reason != "" {
			reasons += provider.Provider + ": " + provider.Reason + " "
		}
	}
	if len(available) == 0 {
		return compute.Problem("provider_unavailable", "No VM provider is available. %s", reasons)
	}
	arch := info.HostArch
	if arch == "" {
		arch = runtime.GOARCH
	}
	for _, image := range info.Images {
		if available[image.Provider] && image.Arch == arch && (image.Status == "ready" || image.Status == "configured") {
			return nil
		}
	}
	return compute.Problem("image_unavailable", "No ready image matches an available provider and this host's native architecture.")
}

func (c *computeCoordinator) monitor() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.checkConnection()
		}
	}
}

func (c *computeCoordinator) checkConnection() {
	if !c.activation.TryLock() {
		return
	}
	defer c.activation.Unlock()
	c.mu.Lock()
	bound, status, identity := c.bound, c.status, c.boundIdentity
	c.mu.Unlock()
	if bound == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
	info, err := HostStatus(ctx, *bound)
	cancel()
	if err == nil && (info.HostID != identity.HostID || info.ConfigFingerprint != identity.ConfigFingerprint) {
		err = fmt.Errorf("the verified VM host identity changed")
	}
	if err != nil {
		c.publish("failed", *bound, compute.Problem("host_unavailable", "The VM service connection is unavailable: %v", err), compute.Runtime{})
		return
	}
	if status.Code == "host_unavailable" {
		if err := verifyCapabilities(c.ctx, *bound, info); err != nil {
			c.publish("failed", *bound, err, info)
			return
		}
		c.publish("ready", *bound, nil, info)
	}
}
