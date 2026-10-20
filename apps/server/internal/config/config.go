// Package config resolves service settings from the shared AXV profile.
package config

import (
	"fmt"
	"net"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/hostpath"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// Config keeps desktop storage separate from the host workspace and user defaults.
type Config struct {
	Addr         string
	DataDir      string
	Home         string
	ConfigPath   string
	WorkspaceDir string
	ProxyURL     string
	TmuxSocket   string
}

// Overrides contains explicit desktop startup options, independently of their
// command-line representation. Empty values use environment or profile settings.
type Overrides struct {
	Addr    string
	DataDir string
}

// Initialize validates desktop settings before publishing a missing profile, then
// resolves the published profile so a concurrent initializer's settings win.
func Initialize(overrides Overrides, getenv func(string) string, userHomeDir func() (string, error)) (Config, error) {
	home, err := profile.ResolveHome(getenv, userHomeDir)
	if err != nil {
		return Config{}, err
	}
	p, err := profile.ReadDesktop(home)
	if err != nil {
		return Config{}, err
	}
	if _, err := Resolve(overrides, p, getenv, userHomeDir); err != nil {
		return Config{}, err
	}
	p, err = profile.InitializeDesktop(home)
	if err != nil {
		return Config{}, err
	}
	return Resolve(overrides, p, getenv, userHomeDir)
}

// Resolve selects and validates all desktop settings from explicit overrides,
// environment and an already-read profile without creating files or directories.
func Resolve(overrides Overrides, p profile.Profile, getenv func(string) string, userHomeDir func() (string, error)) (Config, error) {
	settings := Config{Addr: overrides.Addr, DataDir: overrides.DataDir, Home: p.Home, ConfigPath: filepath.Join(p.Home, "config.json")}
	if err := profile.ValidateRemote(p.Remote); err != nil {
		return Config{}, err
	}
	if settings.Addr == "" {
		settings.Addr = getenv("ADDR")
	}
	if settings.Addr == "" {
		if port := getenv("PORT"); port != "" {
			settings.Addr = net.JoinHostPort("127.0.0.1", port)
		} else {
			settings.Addr = p.Desktop.Addr
		}
	}
	if settings.Addr == "" {
		settings.Addr = "127.0.0.1:3001"
	}
	_, port, err := net.SplitHostPort(settings.Addr)
	if err != nil {
		return Config{}, fmt.Errorf("invalid listen address %q: %w", settings.Addr, err)
	}
	if port == "" {
		return Config{}, fmt.Errorf("invalid listen address %q: port is required", settings.Addr)
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return Config{}, fmt.Errorf("invalid listen address %q: %w", settings.Addr, err)
	}
	if settings.DataDir == "" {
		settings.DataDir = getenv("DATA_DIR")
	}
	if settings.DataDir == "" {
		settings.DataDir = p.Desktop.DataDir
	}
	if settings.DataDir == "" {
		settings.DataDir = filepath.Join(p.Home, "data")
	}
	workspace := p.WorkspaceDir
	if workspace == "" {
		workspace = filepath.Join(p.Home, "workspace")
	}
	if override := getenv("AXV_WORKSPACE_DIR"); override != "" {
		workspace = override
	}
	settings.WorkspaceDir, err = hostpath.Resolve(workspace, userHomeDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve AXV workspace: %w", err)
	}
	settings.ProxyURL = p.ProxyURL
	settings.TmuxSocket = getenv("AXV_TMUX_SOCKET")
	return settings, nil
}
