package lima

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/ubuntusources"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func (d *Driver) GuestReachability(_ compute.Machine, address netip.Addr) guestproto.Reachability {
	result := guestproto.Reachability{From: "deployment-host", State: "unknown", Reason: "not_verified"}
	if address == netip.MustParseAddr("192.168.5.15") {
		result.State, result.Reason = "unreachable", "provider_user_network"
	}
	return result
}

// PrepareGuest initializes only per-machine identity using preinstalled tools.
func (d *Driver) PrepareGuest(ctx context.Context, m compute.Machine, image compute.Image, v compute.Volume) error {
	st, err := d.load(m)
	if err != nil {
		return err
	}
	if image.Guest == nil {
		return fmt.Errorf("this image has no AXV guest contract")
	}
	if err = image.Guest.Compatible(); err != nil {
		return err
	}
	contract, _ := json.Marshal(image.Guest)
	if _, err = d.shell(ctx, strings.NewReader(verifyImageScript), m, "sudo", "-n", "sh", "-s", "--", image.Arch, string(contract)); err != nil {
		return fmt.Errorf("verify guest image: %w", err)
	}
	if health, healthErr := compute.Health(ctx, m, d); healthErr == nil && health.Ready {
		if !guestimage.Has(image.Guest.Capabilities, "ssh") {
			return nil
		}
		if !health.SSHAvailable {
			// Recover the image's preinstalled SSH service without interrupting
			// healthy guest terminals or rewriting machine configuration.
			return d.prepareSSH(ctx, m)
		}
		// Cloud-init can relock AXV's account on boot while both services remain
		// healthy. Repair its owned marker without restarting active sessions.
		if _, err = d.shell(ctx, nil, m, "sudo", "-n", "/usr/local/lib/axv/prepare-ssh", m.ID, "--repair-account"); err != nil {
			return fmt.Errorf("prepare guest SSH account: %w", err)
		}
		return nil
	}
	if guestimage.Has(image.Guest.Capabilities, "ssh") {
		if err = d.prepareSSH(ctx, m); err != nil {
			return err
		}
	}
	data, _ := json.Marshal(st.Guest)
	if _, err = d.shell(ctx, strings.NewReader(string(data)), m, "sudo", "-n", "sh", "-c", "umask 077; cat > /etc/axv/guest.json"); err != nil {
		return err
	}
	if _, err = d.shell(ctx, nil, m, "sudo", "-n", "chown", "axv:axv", "/etc/axv/guest.json"); err != nil {
		return err
	}
	if _, err = d.shell(ctx, nil, m, "sudo", "-n", "chmod", "0600", "/etc/axv/guest.json"); err != nil {
		return err
	}
	_, err = d.shell(ctx, nil, m, "sudo", "-n", "systemctl", "restart", "axv-guest.service")
	return err
}

//go:embed verify-image.sh
var verifyImageScript string

func (d *Driver) prepareSSH(ctx context.Context, m compute.Machine) error {
	// Executables and units are immutable image contents. Only machine identity
	// and profile-authorized keys are initialized at runtime.
	if _, err := d.shell(ctx, nil, m, "sudo", "-n", "/usr/local/lib/axv/prepare-ssh", m.ID); err != nil {
		return fmt.Errorf("initialize guest SSH: %w", err)
	}
	_, err := d.shell(ctx, nil, m, "sudo", "-n", "systemctl", "start", "axv-ssh.service")
	return err
}

func (d *Driver) shell(ctx context.Context, input io.Reader, m compute.Machine, args ...string) ([]byte, error) {
	instance, err := d.runtimeName(m)
	if err != nil {
		return nil, err
	}
	return d.command(ctx, input, append([]string{"shell", "--workdir=/", instance, "--"}, args...)...)
}

func (d *Driver) GuestToken(m compute.Machine) (string, error) {
	st, err := d.load(m)
	return st.Guest.Token, err
}

type pipeConn struct {
	io.ReadCloser
	writer io.WriteCloser
	cmd    *exec.Cmd
	done   chan struct{}
}

func (p *pipeConn) Write(b []byte) (int, error) { return p.writer.Write(b) }
func (p *pipeConn) Close() error {
	p.writer.Close()
	p.ReadCloser.Close()
	_ = p.cmd.Process.Kill()
	<-p.done
	return nil
}
func (p *pipeConn) LocalAddr() net.Addr  { return address("axv-host") }
func (p *pipeConn) RemoteAddr() net.Addr { return address("axv-guest") }
func (p *pipeConn) SetDeadline(t time.Time) error {
	_ = p.SetReadDeadline(t)
	return p.SetWriteDeadline(t)
}
func (p *pipeConn) SetReadDeadline(t time.Time) error {
	if f, ok := p.ReadCloser.(*os.File); ok {
		return f.SetReadDeadline(t)
	}
	return nil
}
func (p *pipeConn) SetWriteDeadline(t time.Time) error {
	if f, ok := p.writer.(*os.File); ok {
		return f.SetWriteDeadline(t)
	}
	return nil
}

type address string

func (a address) Network() string { return "lima-ssh" }
func (a address) String() string  { return string(a) }

func (d *Driver) DialGuest(ctx context.Context, m compute.Machine) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := d.load(m); err != nil {
		return nil, err
	}
	name, err := d.runtimeName(m)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(d.config.Binary, "--tty=false", "shell", "--workdir=/", name, "--", "socat", "STDIO", "UNIX-CONNECT:/run/axv/guest.sock")
	cmd.Dir = d.root
	cmd.Env = d.environment()
	// Wait must reap the relay without closing buffered guest responses. Unlike
	// StdoutPipe, this read end belongs to the connection until EOF or Close.
	reader, output, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer output.Close()
	cmd.Stdout = output
	writer, err := cmd.StdinPipe()
	if err != nil {
		reader.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		return nil, err
	}
	p := &pipeConn{ReadCloser: reader, writer: writer, cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	return p, nil
}

// ConfigurePackageSource modifies only this owned VM's writable disk via Lima's
// management transport. It does not require or install an AXV guest service.
func (d *Driver) ConfigurePackageSource(ctx context.Context, m compute.Machine, source string) error {
	if err := ubuntusources.Validate(source); err != nil {
		return err
	}
	if _, err := d.load(m); err != nil {
		return err
	}
	_, err := d.shell(ctx, strings.NewReader(ubuntusources.Script), m, "sudo", "-n", "sh", "-s", "--", "apply", source)
	return err
}
