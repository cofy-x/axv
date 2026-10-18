package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

func hostBinding(s Settings) (computeclient.Binding, error) {
	if s.ComputeError != "" {
		return computeclient.Binding{}, fmt.Errorf("%s", s.ComputeError)
	}
	if s.Compute.Mode == "disabled" || s.Compute.Mode == "" {
		return computeclient.Binding{}, fmt.Errorf("VM hosting is not configured")
	}
	var binding computeclient.Binding
	if s.Host != nil {
		binding.ConfigFingerprint = s.Host.Fingerprint()
	}
	return binding, nil
}

func HostStatus(ctx context.Context, s Settings) (compute.Runtime, error) {
	binding, err := hostBinding(s)
	if err != nil {
		return compute.Runtime{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return computeclient.Probe(ctx, s.Compute.Socket, binding)
}

func Machines(ctx context.Context, s Settings) ([]compute.Machine, error) {
	binding, err := hostBinding(s)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client, err := computeclient.Connect(ctx, s.Compute.Socket, binding)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Machines(ctx)
}

func startHost(ctx context.Context, s Settings, output io.Writer) (func(), error) {
	if _, err := HostStatus(ctx, s); err == nil {
		return nil, nil
	} else if occupiedHostSocket(s.Compute.Socket) {
		return nil, waitExistingHost(ctx, s)
	}
	path := filepath.Join(s.RunDir(), "foreground-host.json")
	if err := jsonfile.Write(path, s.Host, 0600); err != nil {
		return nil, err
	}
	if s.RunnerExecutable == "" {
		return nil, fmt.Errorf("AXV runtime executable is not pinned")
	}
	cmd := exec.Command(s.RunnerExecutable, "host", "--config", path)
	cmd.Stdout, cmd.Stderr = output, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		select {
		case err := <-done:
			stopped = true
			return nil, fmt.Errorf("compute host exited during startup: %v", err)
		case <-ctx.Done():
			stop()
			return nil, ctx.Err()
		case <-timer.C:
			stop()
			return nil, fmt.Errorf("compute host did not become ready: %w", last)
		case <-ticker.C:
			if _, last = HostStatus(ctx, s); last == nil {
				return stop, nil
			}
		}
	}
}

func waitExistingHost(ctx context.Context, s Settings) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var last error
	for ctx.Err() == nil {
		if _, last = HostStatus(ctx, s); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("existing compute host did not become ready: %w (%v)", ctx.Err(), last)
}
