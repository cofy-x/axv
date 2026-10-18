// Package diskspace checks available host filesystem capacity before storage operations.
package diskspace

import "fmt"

// Require checks requested capacity plus 256 MiB of host headroom.
func Require(path string, bytes int64) error {
	available, _, err := filesystemSpace(path)
	if err != nil {
		return err
	}
	const headroom = 256 << 20
	if bytes < 0 || uint64(bytes)+headroom > available {
		return fmt.Errorf("insufficient free storage; keep at least 256 MiB of host headroom in addition to the operation's temporary copies")
	}
	return nil
}
