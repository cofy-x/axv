//go:build darwin || linux

package tailnet

import (
	"os"
	"syscall"
)

func ownedFile(info os.FileInfo) bool {
	value, ok := info.Sys().(*syscall.Stat_t)
	return ok && value.Uid == uint32(os.Geteuid())
}

func singleLink(info os.FileInfo) bool {
	value, ok := info.Sys().(*syscall.Stat_t)
	return ok && value.Nlink == 1
}
