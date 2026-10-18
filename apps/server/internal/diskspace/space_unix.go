//go:build darwin || linux

package diskspace

import "golang.org/x/sys/unix"

func filesystemSpace(path string) (available, total uint64, err error) {
	var stat unix.Statfs_t
	if err = unix.Statfs(path, &stat); err != nil {
		return
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), uint64(stat.Blocks) * uint64(stat.Bsize), nil
}
