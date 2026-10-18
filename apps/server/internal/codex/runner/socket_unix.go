//go:build darwin || linux

package runner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func createSocketPath(root string) (string, func(), error) {
	if err := ValidateSocketRoot(root); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, fmt.Errorf("create Codex socket root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", nil, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ok || owner.Uid != uint32(os.Getuid()) {
		return "", nil, fmt.Errorf("Codex socket root must be a private directory owned by the AXV user")
	}
	for range 32 {
		var token [socketDirectoryNameLength / 2]byte
		if _, err := rand.Read(token[:]); err != nil {
			return "", nil, err
		}
		directory := filepath.Join(root, hex.EncodeToString(token[:]))
		if err := os.Mkdir(directory, 0o700); os.IsExist(err) {
			continue
		} else if err != nil {
			return "", nil, err
		}
		owned, err := os.Lstat(directory)
		if err != nil {
			return "", nil, err
		}
		cleanup := func() {
			if current, err := os.Lstat(directory); err == nil && os.SameFile(owned, current) {
				_ = os.RemoveAll(directory)
			}
		}
		return filepath.Join(directory, "s"), cleanup, nil
	}
	return "", nil, fmt.Errorf("could not allocate a private Codex socket directory")
}
