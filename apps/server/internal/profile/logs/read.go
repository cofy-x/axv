package logs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Tail reads a bounded canonical log without creating or following files.
func Tail(home string, limit int64) ([]byte, error) {
	return tail(home, "daemon.log", limit)
}

// TailLegacy retains native-supervisor diagnostics from existing host jobs.
func TailLegacy(home, role string, limit int64) ([]byte, error) {
	if role != "desktop" && role != "host" {
		return nil, fmt.Errorf("unknown service log role")
	}
	return tail(home, role+".log", limit)
}

func tail(home, name string, limit int64) ([]byte, error) {
	dir := filepath.Join(home, "logs")
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !owned(info) || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("unsafe profile log directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, info) {
		return nil, fmt.Errorf("profile log directory changed during read")
	}
	path := name
	before, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err = privateFile(before); err != nil {
		return nil, err
	}
	f, err := openRootFile(root, path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, errors.New("log changed during read")
	}
	start := after.Size() - limit
	if start < 0 {
		start = 0
	}
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if start > 0 {
		if i := strings.IndexByte(string(b), '\n'); i >= 0 {
			b = b[i+1:]
		} else {
			b = nil
		}
	}
	return b, err
}

// PrepareLegacy validates the private destination before a native supervisor
// receives its existing role-log path. It does not alter the manifest contract.
func PrepareLegacy(home, role string) error {
	if role != "desktop" && role != "host" {
		return fmt.Errorf("unknown service log role")
	}
	l, err := Open(home, "launcher", nil)
	if err != nil {
		return err
	}
	if l.sink == nil {
		return fmt.Errorf("private profile logging is unavailable on this platform")
	}
	root, err := os.OpenRoot(l.sink.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, l.sink.identity) {
		return fmt.Errorf("profile log directory changed")
	}
	f, err := checkedFile(root, role+".log", true)
	if err != nil {
		return err
	}
	return f.Close()
}
