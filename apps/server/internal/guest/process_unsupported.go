//go:build !linux && !darwin

package guest

import "os/exec"

// Run and RunOptions reject unsupported guest platforms before execution.
func configureExecution(*exec.Cmd) {}
