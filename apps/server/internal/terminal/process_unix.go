//go:build darwin || linux

package terminal

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/nativepty"
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
	cmd            *exec.Cmd
	done           chan struct{}
	once           sync.Once
	closeRequested atomic.Bool
}

func startProcess(config shellConfig, cols, rows int) (process, error) {
	cmd := exec.Command(config.path, "-i")
	// A leading dash in argv[0] requests a login shell without shell-specific flags.
	cmd.Args[0] = "-" + filepath.Base(config.path)
	cmd.Dir, cmd.Env = config.dir, config.env
	started := time.Now()
	file, err := nativepty.Start(cmd, uint16(cols), uint16(rows))
	if err != nil {
		return nil, err
	}
	p := &unixProcess{File: file, cmd: cmd, done: make(chan struct{})}
	go func() {
		waitErr := cmd.Wait()
		elapsed := time.Since(started)
		closeRequested := p.closeRequested.Load()
		exitCode := -1
		var signal syscall.Signal
		if state := cmd.ProcessState; state != nil {
			exitCode = state.ExitCode()
			if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				signal = status.Signal()
			}
		}
		// Capture the close request before publishing completion. Natural-exit
		// cleanup may call Close next, and logging must not delay process reaping.
		close(p.done)
		waitError := ""
		if waitErr != nil {
			waitError = waitErr.Error()
		}
		log.Printf("host terminal process exited pid=%d shell=%q exit_code=%d signal=%d elapsed_ms=%d close_requested=%t wait_error=%q", cmd.Process.Pid, cmd.Path, exitCode, signal, elapsed.Milliseconds(), closeRequested, waitError)
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
		p.closeRequested.Store(true)
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
