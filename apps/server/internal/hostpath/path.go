// Package hostpath resolves user-supplied host paths without accessing their contents.
package hostpath

import (
	"errors"
	"path/filepath"
	"strings"
)

// Resolve expands a leading home directory marker and returns an absolute path.
func Resolve(value string, userHomeDir func() (string, error)) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", errors.New("path contains a NUL character")
	}
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, err := userHomeDir()
		if err != nil {
			return "", err
		}
		suffix := ""
		if len(value) > 1 {
			suffix = value[2:]
		}
		value = filepath.Join(home, suffix)
	}
	return filepath.Abs(value)
}
