//go:build linux

package firecracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/codex/vmproxy"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func (d *Driver) proxySocketPath(m compute.Machine) string {
	return filepath.Join(d.jail(m), fmt.Sprintf("vsock.sock_%d", guestproto.ProxyPort))
}

// startVMProxy exposes the host-side zero-credential LLM proxy on Firecracker's
// vsock port 1025 socket (<jail>/vsock.sock_1025) when an API key is configured
// on the compute host. Secrets never enter the VM.
func (d *Driver) startVMProxy(m compute.Machine) error {
	cfg := vmproxy.FromEnvironment()
	if !cfg.Enabled() {
		return nil
	}
	handler, err := vmproxy.NewHandler(cfg)
	if err != nil {
		return fmt.Errorf("configure VM Codex proxy: %w", err)
	}
	d.stopVMProxy(m.ID)
	sockPath := d.proxySocketPath(m)
	ctx, cancel := context.WithCancel(context.Background())
	stop, err := vmproxy.ServeUnix(ctx, sockPath, handler)
	if err != nil {
		cancel()
		return fmt.Errorf("bind VM Codex proxy socket: %w", err)
	}
	if err := os.Chmod(sockPath, 0600); err != nil {
		cancel()
		stop()
		return fmt.Errorf("chmod VM Codex proxy socket: %w", err)
	}
	if err := os.Chown(sockPath, d.config.UID+m.Slot, d.config.GID); err != nil {
		cancel()
		stop()
		return fmt.Errorf("chown VM Codex proxy socket: %w", err)
	}
	d.proxyMu.Lock()
	if d.proxies == nil {
		d.proxies = make(map[string]context.CancelFunc)
	}
	d.proxies[m.ID] = func() {
		cancel()
		stop()
	}
	d.proxyMu.Unlock()
	return nil
}

func (d *Driver) stopVMProxy(machineID string) {
	d.proxyMu.Lock()
	cancel := d.proxies[machineID]
	delete(d.proxies, machineID)
	d.proxyMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
