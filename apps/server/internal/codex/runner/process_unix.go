//go:build darwin || linux

package runner

import (
	"os/exec"
	"syscall"
)

func configureServerProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcess(process *ownedProcess) {
	if process.group {
		if syscall.Kill(-process.cmd.Process.Pid, syscall.SIGTERM) != nil {
			_ = process.cmd.Process.Signal(syscall.SIGTERM)
		}
	} else {
		_ = process.cmd.Process.Signal(syscall.SIGTERM)
	}
}

func killProcess(process *ownedProcess) {
	if process.group {
		if syscall.Kill(-process.cmd.Process.Pid, syscall.SIGKILL) != nil {
			_ = process.cmd.Process.Kill()
		}
	} else {
		_ = process.cmd.Process.Kill()
	}
}
