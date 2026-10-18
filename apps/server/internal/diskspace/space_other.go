//go:build !darwin && !linux

package diskspace

import "fmt"

func filesystemSpace(string) (available, total uint64, err error) {
	return 0, 0, fmt.Errorf("filesystem capacity inspection is unavailable on this host")
}
