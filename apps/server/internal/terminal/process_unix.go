//go:build darwin || linux

package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func platformSupport() string { return "" }

func platformShell() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	info, err := os.Stat(shell)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		return "", fmt.Errorf("The configured host shell is not executable.")
	}
	return shell, nil
}

type unixProcess struct {
	*os.File
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

func startProcess(config shellConfig, cols, rows int) (process, error) {
	cmd := exec.Command(config.path, "-i")
	// A leading dash in argv[0] requests a login shell without shell-specific flags.
	cmd.Args[0] = "-" + filepath.Base(config.path)
	cmd.Dir, cmd.Env = config.dir, config.env
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	// Rewrap a nonblocking duplicate so Go registers the PTY with its poller on
	// macOS as well as Linux. Close can then interrupt stalled reads and writes.
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
	file = os.NewFile(uintptr(fd), "host-terminal")
	p := &unixProcess{File: file, cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *unixProcess) Done() <-chan struct{} { return p.done }

func (p *unixProcess) Resize(cols, rows int) error {
	return p.withFD(func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	})
}

func (p *unixProcess) withFD(operation func(int) error) error {
	raw, err := p.File.SyscallConn()
	if err != nil {
		return err
	}
	var operationErr error
	if err = raw.Control(func(fd uintptr) { operationErr = operation(int(fd)) }); err != nil {
		return err
	}
	return operationErr
}

func (p *unixProcess) foregroundGroup() int {
	group := 0
	_ = p.withFD(func(fd int) error {
		var err error
		group, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		return err
	})
	return group
}

func (p *unixProcess) Close() {
	p.once.Do(func() {
		// Interactive jobs may own a different process group from the shell.
		foreground := p.foregroundGroup()
		pid := p.cmd.Process.Pid
		if foreground > 0 && foreground != pid {
			_ = syscall.Kill(-foreground, syscall.SIGHUP)
		}
		select {
		case <-p.done:
		default:
			_ = syscall.Kill(-pid, syscall.SIGHUP)
			select {
			case <-p.done:
			case <-time.After(500 * time.Millisecond):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
		if foreground > 0 && foreground != pid {
			_ = syscall.Kill(-foreground, syscall.SIGKILL)
		}
		_ = p.File.Close()
		<-p.done // The single Wait goroutine reaps the shell before releasing a slot.
	})
}
