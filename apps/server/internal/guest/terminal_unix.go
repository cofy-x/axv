//go:build linux || darwin

package guest

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/nativepty"
	"golang.org/x/sys/unix"
)

func startTerminal(dir string, cols, rows uint16) (*os.File, *exec.Cmd, error) {
	cmd := exec.Command("/bin/bash", "-l")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := nativepty.Start(cmd, cols, rows)
	if err != nil {
		return nil, nil, err
	}
	return f, cmd, nil
}

func (s *terminal) withFD(action func(int) error) error {
	raw, err := s.file.SyscallConn()
	if err != nil {
		return err
	}
	var actionErr error
	if err = raw.Control(func(fd uintptr) { actionErr = action(int(fd)) }); err != nil {
		return err
	}
	return actionErr
}

func (s *terminal) terminate() {
	s.terminateOnce.Do(func() {
		foreground := 0
		_ = s.withFD(func(fd int) error { var err error; foreground, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP); return err })
		pid := s.cmd.Process.Pid
		if foreground > 0 && foreground != pid {
			_ = syscall.Kill(-foreground, syscall.SIGHUP)
		}
		select {
		case <-s.done:
		default:
			_ = syscall.Kill(-pid, syscall.SIGHUP)
			select {
			case <-s.done:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
		if foreground > 0 && foreground != pid {
			_ = syscall.Kill(-foreground, syscall.SIGKILL)
		}
		s.file.Close()
		<-s.done
	})
}

func (s *terminal) resize(cols, rows uint16) error {
	return s.withFD(func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
	})
}
