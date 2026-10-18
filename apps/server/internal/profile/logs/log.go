// Package logs owns private operational log files within an AXV profile.
package logs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
)

const maxSize = 10 << 20
const archives = 5

type sink struct {
	dir      string
	identity os.FileInfo
	limit    int64
}

// Log combines operational diagnostics with its initialized profile log path.
// File logging is unavailable on Windows, where events are written to stderr.
type Log struct {
	*diagnostics.Logger
	sink *sink
}

// Open validates the private sink before a service performs work.
func Open(home, component string, fallback io.Writer) (*Log, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("profile log home must be absolute")
	}
	if runtime.GOOS == "windows" {
		logger := &Log{Logger: diagnostics.Stderr(component, fallback)}
		logger.Info("logging.stderr_only", "reason", "Private profile-file logging is unavailable on Windows.")
		return logger, nil
	}
	dir := filepath.Join(home, "logs")
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	dir, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "logs")
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !owned(info) || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("profile log directory must be a private directory owned by the current user: %s", dir)
	}
	s := &sink{dir: dir, identity: info, limit: maxSize}
	l := &Log{Logger: diagnostics.New(component, s, fallback), sink: s}
	if _, err := s.Write(nil); err != nil {
		return nil, fmt.Errorf("initialize profile log %s: %w", l.Path(), err)
	}
	return l, nil
}

// Path is empty when profile-file logging is unavailable.
func (l *Log) Path() string {
	if l == nil || l.sink == nil {
		return ""
	}
	return filepath.Join(l.sink.dir, "daemon.log")
}

func (s *sink) Write(record []byte) (int, error) {
	current, err := os.Lstat(s.dir)
	if err != nil {
		return 0, err
	}
	if !os.SameFile(current, s.identity) || !current.IsDir() || current.Mode().Perm()&0077 != 0 {
		return 0, fmt.Errorf("profile log directory changed or is no longer private")
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil {
		return 0, err
	}
	if !os.SameFile(opened, s.identity) {
		return 0, fmt.Errorf("profile log directory changed during open")
	}
	lock, err := checkedFile(root, "daemon.lock", true)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ok, e := tryLock(lock)
		if e != nil {
			return 0, e
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("profile log lock timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer unlock(lock)
	info, err := root.Lstat("daemon.lock")
	if err != nil {
		return 0, err
	}
	held, err := lock.Stat()
	if err != nil {
		return 0, err
	}
	if !os.SameFile(info, held) {
		return 0, fmt.Errorf("profile log lock changed")
	}
	path := "daemon.log"
	f, err := checkedFile(root, path, true)
	if err != nil {
		return 0, err
	}
	info, err = f.Stat()
	if err != nil {
		f.Close()
		return 0, err
	}
	if len(record) > 0 && info.Size() > 0 && info.Size()+int64(len(record)) > s.limit {
		f.Close()
		// Validate every archive before changing any name.
		for i := 1; i <= archives; i++ {
			p := fmt.Sprintf("%s.%d", path, i)
			a, e := checkedFile(root, p, false)
			if e != nil && !os.IsNotExist(e) {
				return 0, e
			}
			if a != nil {
				a.Close()
			}
		}
		if err = root.Remove(fmt.Sprintf("%s.%d", path, archives)); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		for i := archives - 1; i >= 1; i-- {
			if err = root.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1)); err != nil && !os.IsNotExist(err) {
				return 0, err
			}
		}
		if err = root.Rename(path, path+".1"); err != nil {
			return 0, err
		}
		f, err = checkedFile(root, path, true)
		if err != nil {
			return 0, err
		}
	}
	defer f.Close()
	if len(record) == 0 {
		return 0, nil
	}
	n, err := f.Write(record)
	if err == nil && n != len(record) {
		err = io.ErrShortWrite
	}
	return n, err
}

func checkedFile(root *os.Root, path string, create bool) (*os.File, error) {
	for attempt := 0; attempt < 4; attempt++ {
		before, err := root.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			if err = privateFile(before); err != nil {
				return nil, fmt.Errorf("unsafe log file %s: %w", path, err)
			}
		} else if !create {
			return nil, err
		}
		flags := os.O_WRONLY | os.O_APPEND
		if before == nil {
			flags |= os.O_CREATE | os.O_EXCL
		}
		f, err := openRootFile(root, path, flags, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err == nil {
			err = privateFile(info)
		}
		if err == nil && before != nil && !os.SameFile(before, info) {
			err = fmt.Errorf("log file changed during open")
		}
		if err != nil {
			f.Close()
			return nil, err
		}
		return f, nil
	}
	return nil, fmt.Errorf("log file repeatedly changed during open")
}

func privateFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() || !owned(info) || !singleLink(info) || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("expected a private regular file with one link owned by the current user")
	}
	return nil
}
