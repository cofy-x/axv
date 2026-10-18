//go:build darwin || linux

package runner

import (
	"errors"
	"golang.org/x/sys/unix"
)

func contextLockContended(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
