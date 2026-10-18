//go:build !darwin && !linux

package runner

import "os/exec"

func configureServerProcess(command *exec.Cmd) {}

func terminateProcess(process *ownedProcess) { _ = process.cmd.Process.Kill() }

func killProcess(process *ownedProcess) { _ = process.cmd.Process.Kill() }

func Run(manifestPath string) error { return ErrUnavailable }
