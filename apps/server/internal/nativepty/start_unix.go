//go:build darwin || linux

// Package nativepty starts pollable PTYs without owning session lifetimes.
package nativepty

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Start returns a master whose reads and writes support deadlines and Close.
// After success, the caller owns the master, process termination and cmd.Wait.
func Start(cmd *exec.Cmd, cols, rows uint16) (*os.File, error) {
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	file, err := pollableMaster(master)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	return file, nil
}

func pollableMaster(master *os.File) (*os.File, error) {
	defer master.Close()
	// Darwin's master is initially blocking. Rewrap a nonblocking duplicate so
	// Go registers it with the poller on both platforms. Avoid Fd, which can
	// switch an already pollable master back to blocking mode.
	raw, err := master.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("access PTY descriptor: %w", err)
	}
	fd := -1
	var duplicateErr error
	if err = raw.Control(func(original uintptr) {
		fd, duplicateErr = unix.FcntlInt(original, unix.F_DUPFD_CLOEXEC, 0)
	}); err != nil {
		return nil, fmt.Errorf("duplicate PTY descriptor: %w", err)
	}
	if duplicateErr != nil {
		return nil, fmt.Errorf("duplicate PTY descriptor: %w", duplicateErr)
	}
	if err = unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("set PTY nonblocking: %w", err)
	}
	return os.NewFile(uintptr(fd), "native-pty"), nil
}
