//go:build !windows

package guest

import (
	"os"
	"path"

	"golang.org/x/sys/unix"
)

func projectFileOpen(root *os.Root, name string) (*os.File, error) {
	parent, err := root.OpenRoot(path.Dir(name))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	directory, err := parent.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	// os.Root.OpenFile resolves a final symlink itself even when O_NOFOLLOW is
	// supplied. Open only the basename through this confined directory handle
	// so the kernel enforces no-follow on the final component.
	fd, err := unix.Openat(int(directory.Fd()), path.Base(name), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}
