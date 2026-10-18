package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/hostdir"
)

type shellConfig struct {
	path string
	dir  string
	env  []string
}

type process interface {
	io.ReadWriter
	Resize(cols, rows int) error
	Done() <-chan struct{}
	Close()
}

func resolveShell() (shellConfig, error) {
	if reason := platformSupport(); reason != "" {
		return shellConfig{}, fmt.Errorf("%s", reason)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return shellConfig{}, fmt.Errorf("Cannot find the host home directory: %w", err)
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		return shellConfig{}, fmt.Errorf("The host home directory is unavailable.")
	}
	shell, err := platformShell()
	if err != nil {
		return shellConfig{}, err
	}
	if !filepath.IsAbs(shell) {
		return shellConfig{}, fmt.Errorf("The host shell must be an absolute executable path.")
	}
	return shellConfig{path: shell, dir: home, env: terminalEnvironment(os.Environ())}, nil
}

func projectDirectory(path string) (string, error) {
	resolved, err := hostdir.CanonicalDirectory(path)
	if err == nil {
		return resolved, nil
	}
	var directoryError *hostdir.Error
	if !errors.As(err, &directoryError) {
		return "", err
	}
	switch directoryError.Kind {
	case hostdir.InvalidPath:
		return "", fmt.Errorf("Choose an absolute host project directory of at most 4096 bytes.")
	case hostdir.Unavailable:
		return "", fmt.Errorf("The project directory does not exist or cannot be accessed.")
	case hostdir.NotDirectory:
		return "", fmt.Errorf("The selected project path is a file, not a directory.")
	case hostdir.Unreadable:
		return "", fmt.Errorf("The server user cannot read the project directory. Check its permissions.")
	default:
		return "", err
	}
}

func terminalEnvironment(env []string) []string {
	result := make([]string, 0, len(env)+2)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "TERM") && !strings.EqualFold(key, "COLORTERM") {
			result = append(result, entry)
		}
	}
	return append(result, "TERM=xterm-256color", "COLORTERM=truecolor")
}
