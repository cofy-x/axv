//go:build darwin || linux

package codex

import (
	"os"
	"syscall"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
)

// openActivityReview rejects unsafe replacements before opening and uses a
// directory handle and nonblocking, no-follow open to close the check/open gap.
func openActivityReview(directory string) (*os.File, error) {
	private := func(info os.FileInfo) bool {
		stat, ok := info.Sys().(*syscall.Stat_t)
		return ok && stat.Uid == uint32(os.Getuid()) && info.Mode().Perm()&0077 == 0
	}
	entry, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !entry.IsDir() || !private(entry) {
		return nil, runner.ErrActivityUnavailable
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(entry, opened) || !private(opened) {
		return nil, runner.ErrActivityUnavailable
	}
	entry, err = root.Lstat("activity-review.json")
	if err != nil {
		return nil, err
	}
	if !entry.Mode().IsRegular() || !private(entry) || entry.Size() > 4096 {
		return nil, runner.ErrActivityUnavailable
	}
	file, err := root.OpenFile("activity-review.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	opened, err = file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !private(opened) || !os.SameFile(entry, opened) || opened.Size() > 4096 {
		_ = file.Close()
		return nil, runner.ErrActivityUnavailable
	}
	return file, nil
}
