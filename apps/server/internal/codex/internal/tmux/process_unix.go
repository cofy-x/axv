//go:build darwin || linux

package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/nativepty"
	"golang.org/x/sys/unix"
)

type attachment struct {
	*os.File
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	once sync.Once
}

func (b Backend) Attach(ctx context.Context, path string, target Target, cols, rows int) (Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(path, Arguments(target.Socket, "attach-session", "-E", "-t", target.SessionTarget())...)
	cmd.Env = Environment(filepath.Dir(path))
	file, err := nativepty.Start(cmd, uint16(cols), uint16(rows))
	if err != nil {
		return nil, fmt.Errorf("start attachment PTY: %w", err)
	}
	p := &attachment{File: file, cmd: cmd, done: make(chan struct{})}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	if err := b.waitAttachment(ctx, path, target, p); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// Starting the client process does not mean tmux has attached it yet. Do not
// report readiness while an initial resize would still include client startup.
func (b Backend) waitAttachment(parent context.Context, path string, target Target, p *attachment) error {
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	pid := strconv.Itoa(p.cmd.Process.Pid)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for attachment client: %w", ctx.Err())
		case <-p.Done():
			return fmt.Errorf("attachment PTY exited before joining the persistent session: %v", p.ExitError())
		default:
		}
		clients, err := b.Command(ctx, path, target, "list-clients", "-t", target.SessionTarget(), "-F", "#{client_pid}")
		if err != nil {
			return fmt.Errorf("inspect attachment clients: %w", err)
		}
		for _, client := range strings.Fields(clients) {
			if client == pid {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for attachment client: %w", ctx.Err())
		case <-p.Done():
			return fmt.Errorf("attachment PTY exited before joining the persistent session: %v", p.ExitError())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (p *attachment) Done() <-chan struct{} { return p.done }

func (p *attachment) ExitError() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}

func (p *attachment) Resize(cols, rows int) error {
	raw, err := p.File.SyscallConn()
	if err != nil {
		return err
	}
	var operationErr error
	err = raw.Control(func(fd uintptr) {
		operationErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	})
	if err != nil {
		return err
	}
	return operationErr
}

func (p *attachment) Close() {
	p.once.Do(func() {
		// Only this client belongs to AXV. The server and Codex pane have separate
		// sessions/process groups and must survive a browser or service disconnect.
		_ = p.File.Close()
		select {
		case <-p.done:
		default:
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGHUP)
			select {
			case <-p.done:
			case <-time.After(500 * time.Millisecond):
				_ = p.cmd.Process.Kill()
			}
		}
		<-p.done
	})
}

func terminalSize(path string) (int, int, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, 0, err
	}
	defer unix.Close(fd)
	size, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(size.Col), int(size.Row), nil
}
