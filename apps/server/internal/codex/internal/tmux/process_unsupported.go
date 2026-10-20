//go:build !darwin && !linux

package tmux

import (
	"context"
	"errors"
)

func (b Backend) Attach(context.Context, string, Target, int, int) (Process, error) {
	return nil, errors.New("Host Codex sessions require macOS or Linux with tmux.")
}

func terminalSize(string) (int, int, error) {
	return 0, 0, errors.New("Host Codex requires macOS or Linux")
}
