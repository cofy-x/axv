package artifactcache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/hostpath"
)

// Home is user-scoped and deliberately independent of profile/test roots.
func Home() (string, error) { return ResolveHome(os.Getenv, os.UserHomeDir) }

func ResolveHome(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if value := getenv("AXV_CACHE_HOME"); value != "" {
		if !filepath.IsAbs(value) && value != "~" && !strings.HasPrefix(value, "~/") {
			return "", fmt.Errorf("AXV_CACHE_HOME must be an absolute path")
		}
		return hostpath.Resolve(value, userHomeDir)
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve AXV cache home: %w", err)
	}
	return filepath.Join(home, ".axv", "cache"), nil
}
