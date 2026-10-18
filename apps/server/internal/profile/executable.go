package profile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/filedigest"
)

// PinExecutable publishes an immutable copy before a service becomes ready.
// Later children keep using that copy even if the installed command is replaced.
func PinExecutable(home, source string) (string, error) {
	dir := filepath.Join(home, "bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	from, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer from.Close()
	info, err := from.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("AXV executable must be a regular file: %s", source)
	}
	to, err := os.CreateTemp(dir, ".axv-")
	if err != nil {
		return "", err
	}
	defer to.Close()
	defer os.Remove(to.Name())
	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(to, h), from); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	path := filepath.Join(dir, "axv-"+digest)
	if err = to.Chmod(0700); err != nil {
		return "", err
	}
	if err = to.Sync(); err != nil {
		return "", err
	}
	if err = to.Close(); err != nil {
		return "", err
	}
	// A hard link publishes without replacing an existing file or symlink.
	if err = os.Link(to.Name(), path); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("invalid pinned AXV executable: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	actual, err := filedigest.Sum(context.Background(), f)
	_ = f.Close()
	if err != nil {
		return "", err
	}
	if actual != digest {
		return "", fmt.Errorf("pinned AXV executable changed: %s", path)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	return path, directory.Sync()
}
