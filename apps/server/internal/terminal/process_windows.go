//go:build windows

package terminal

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/UserExistsError/conpty"
)

func platformSupport() string {
	if !conpty.IsConPtyAvailable() {
		return "Terminal requires Windows 10 version 1809 or newer with ConPTY support."
	}
	return ""
}

func platformShell() (string, error) {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return exec.LookPath(shell)
}

type windowsProcess struct {
	*conpty.ConPty
	done       chan struct{}
	once       sync.Once
	mu         sync.Mutex
	closed     bool
	cancelWait context.CancelFunc
}

func startProcess(config shellConfig, cols, rows int) (process, error) {
	console, err := conpty.Start(syscall.EscapeArg(config.path),
		conpty.ConPtyDimensions(cols, rows), conpty.ConPtyWorkDir(config.dir), conpty.ConPtyEnv(config.env))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &windowsProcess{ConPty: console, done: make(chan struct{}), cancelWait: cancel}
	go func() {
		_, _ = console.Wait(ctx)
		close(p.done)
	}()
	return p, nil
}

func (p *windowsProcess) Done() <-chan struct{} { return p.done }

func (p *windowsProcess) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return os.ErrClosed
	}
	return p.ConPty.Resize(cols, rows)
}

func (p *windowsProcess) Close() {
	p.once.Do(func() {
		defer p.cancelWait()
		// Wait must finish before Close releases its process handle. The output
		// reader keeps draining while ClosePseudoConsole runs (required on older Windows).
		select {
		case <-p.done:
		default:
			if shell, err := os.FindProcess(p.Pid()); err == nil {
				_ = shell.Kill()
				_ = shell.Release()
			}
			select {
			case <-p.done:
			case <-time.After(1500 * time.Millisecond):
				// A failed TerminateProcess must not leave shutdown waiting forever.
				// Stop the waiter before Close releases the handle and terminates ConPTY.
				p.cancelWait()
				<-p.done
			}
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.closed = true
		_ = p.ConPty.Close()
	})
}
