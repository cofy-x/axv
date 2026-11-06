//go:build linux || darwin

package guest

import (
	"os"
	"syscall"
)

func projectGitHubProfileOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
