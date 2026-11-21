//go:build linux && amd64

package hostinstall

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// All parents of privileged writes must be real directories owned exclusively
// by the administrator. Tests use the current user's private fixture root.
type installFS struct {
	root string
	uid  uint32
}

func (fs installFS) path(name string) string { return filepath.Join(fs.root, name) }

func (fs installFS) trusted(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != fs.uid || info.Mode().Perm()&0022 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe ownership or permissions on %s", path)
	}
	return nil
}

func (fs installFS) directories(name string, create bool) error {
	path := fs.root
	for _, member := range strings.Split(filepath.Clean(name), "/") {
		if member == "" {
			continue
		}
		path = filepath.Join(path, member)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			if !create {
				return nil
			}
			if err := os.Mkdir(path, 0755); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(path)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("expected an administrator-owned directory at %s", path)
		}
		if err := fs.trusted(path, info); err != nil {
			return err
		}
	}
	return nil
}

func (fs installFS) read(name string, mode os.FileMode) ([]byte, error) {
	path := fs.path(name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err = fs.trusted(path, info); err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode || info.Size() > 1<<20 {
		return nil, fmt.Errorf("unexpected file type, size or permissions on %s", path)
	}
	return os.ReadFile(path)
}

func syncDirectory(name string) error {
	dir, err := os.Open(name)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func writeFile(file *os.File, src io.Reader, mode os.FileMode) error {
	defer file.Close()
	if _, err := io.Copy(file, src); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

// A link publishes a complete file only when the destination remains absent.
func (fs installFS) publish(name string, contents []byte, mode os.FileMode) error {
	if err := fs.directories(filepath.Dir(name), true); err != nil {
		return err
	}
	path := fs.path(name)
	current, err := fs.read(name, mode)
	if err == nil {
		if !bytes.Equal(current, contents) {
			return fmt.Errorf("existing %s differs from this installation", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".axv-install-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = writeFile(tmp, bytes.NewReader(contents), mode); err != nil {
		return err
	}
	if err = os.Link(tmp.Name(), path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func (fs installFS) lock() (*os.File, error) {
	if err := fs.directories("/run", false); err != nil {
		return nil, err
	}
	path := fs.path("/run/axv-host-install.lock")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("expected a regular installation lock at %s", path)
	}
	if err == nil {
		err = fs.trusted(path, info)
	}
	if err == nil {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("cannot lock AXV host installation: %w", err)
	}
	return file, nil
}
