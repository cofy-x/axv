//go:build linux || darwin

package guest

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func startTerminal(dir string, cols, rows uint16) (*os.File, *exec.Cmd, error) {
	cmd := exec.Command("/bin/bash", "-l")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, nil, err
	}
	// Register a nonblocking PTY with Go's poller so deadlines and Close can
	// interrupt a process that stops reading input or holds the terminal open.
	fd, err := unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	f.Close()
	if err == nil {
		err = unix.SetNonblock(fd, true)
	}
	if err != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, err
	}
	f = os.NewFile(uintptr(fd), "guest-terminal")
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
