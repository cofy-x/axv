package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/app"
	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/daemon"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func resolvedSettings(options serveOptions) (app.Settings, error) {
	p, err := profile.LoadDesktop()
	if err != nil {
		return app.Settings{}, err
	}
	// Reject invalid startup settings before publishing any configuration file.
	if _, err := resolveProfileSettings(options, p); err != nil {
		return app.Settings{}, err
	}
	p, err = profile.InitializeDesktop(p.Home)
	if err != nil {
		return app.Settings{}, err
	}
	// Re-read through Initialize so a concurrent initializer's profile wins.
	return resolveProfileSettings(options, p)
}

func resolveProfileSettings(options serveOptions, p profile.Profile) (app.Settings, error) {
	desktop, err := config.Resolve(options.overrides(), p, os.Getenv, os.UserHomeDir)
	if err != nil {
		return app.Settings{}, err
	}
	return app.Resolve(p, desktop)
}

func runDesktopSnapshot(ctx context.Context, path string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("invalid internal desktop service arguments")
	}
	var snapshot launch.Snapshot
	if err := jsonfile.Read(path, &snapshot); err != nil {
		return err
	}
	settings := app.SettingsFromSnapshot(snapshot)
	if !filepath.IsAbs(settings.Home) || !filepath.IsAbs(settings.Desktop.DataDir) || settings.Desktop.Addr == "" || settings.LaunchID == "" {
		return fmt.Errorf("invalid desktop service snapshot")
	}
	if err := launch.RestoreEnvironment(settings.Environment); err != nil {
		return err
	}
	return app.ServeWithHostRunnerContext(ctx, settings, output, daemon.EnsureHost)
}
