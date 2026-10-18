//go:build darwin || linux

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// setupConfirmationLine waits for terminal input without closing the caller's
// file or leaving a reader behind when its context is cancelled.
func setupConfirmationLine(ctx context.Context, input *os.File) (string, error) {
	poll := []unix.PollFd{{Fd: int32(input.Fd()), Events: unix.POLLIN}}
	var line strings.Builder
	var next [1]byte
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, err := unix.Poll(poll, 100); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return "", err
		}
		if poll[0].Revents&unix.POLLNVAL != 0 {
			return "", os.ErrClosed
		}
		if poll[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := input.Read(next[:])
		if n > 0 {
			line.WriteByte(next[0])
			if next[0] == '\n' {
				return line.String(), nil
			}
		}
		if err != nil {
			return line.String(), err
		}
		if n == 0 {
			return line.String(), io.EOF
		}
	}
}
