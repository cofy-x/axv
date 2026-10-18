package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/hostpath"
)

func Home() (string, error) {
	return ResolveHome(os.Getenv, os.UserHomeDir)
}

func ResolveHome(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	home := getenv("AXV_HOME")
	if home == "" {
		base, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve AXV home: %w", err)
		}
		home = filepath.Join(base, ".axv")
	}
	return hostpath.Resolve(home, userHomeDir)
}

func Identity(home string) string {
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	h := sha256.Sum256([]byte(filepath.Clean(home)))
	return hex.EncodeToString(h[:])[:16]
}
