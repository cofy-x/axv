//go:build darwin || linux

package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type attachment struct {
	*os.File
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	once sync.Once
}

func (b Backend) Attach(path string, target Target, cols, rows int) (Process, error) {
	cmd := exec.Command(path, Arguments(target.Socket, "attach-session", "-E", "-t", target.SessionTarget())...)
	cmd.Env = Environment(filepath.Dir(path))
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	// Register a nonblocking descriptor with Go's poller on macOS and Linux so
	// detaching interrupts stalled reads/writes rather than blocking shutdown.
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	_ = file.Close()
	if err == nil {
		err = unix.SetNonblock(fd, true)
	}
	if err != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	p := &attachment{File: os.NewFile(uintptr(fd), "codex-tmux-client"), cmd: cmd, done: make(chan struct{})}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	return p, nil
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
