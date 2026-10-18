// Package app composes desktop services, VM preparation and host activation.
package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
	preparehttp "github.com/cofy-x/axv/apps/server/internal/compute/prepare/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
	"github.com/cofy-x/axv/apps/server/internal/server"
)

func Resolve(p profile.Profile, desktop config.Config) (Settings, error) {
	s := Settings{Inputs: launch.Inputs{Home: p.Home, Desktop: desktop, Environment: launch.CaptureEnvironment(os.Getenv)}, ComputeSettings: ComputeSettings{Compute: p.Compute}}
	var err error
	s.Desktop.DataDir, err = filepath.Abs(s.Desktop.DataDir)
	if err != nil {
		return s, err
	}
	s.ComputeOverride = os.Getenv("AXV_COMPUTE_SOCKET")
	s.ComputeError = p.ComputeError
	s = resolveCompute(s)
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		return s, fmt.Errorf("run the AXV desktop as your normal user; administer the privileged compute host separately")
	}
	return s, nil
}

func (s Settings) RunDir() string    { return filepath.Join(s.Home, "run") }
func (s Settings) StatePath() string { return filepath.Join(s.RunDir(), "desktop.json") }

func (s Settings) Prepare() error {
	if err := os.MkdirAll(s.Home, 0700); err != nil {
		return err
	}
	return owneddir.Ensure(s.RunDir(), profile.Identity(s.Home))
}

// ServeWithHostRunnerContext injects supervision without a package cycle.
func ServeWithHostRunnerContext(ctx context.Context, s Settings, output io.Writer, runner HostRunner) error {
	return serveContext(ctx, s, output, runner)
}

func ServeContext(ctx context.Context, s Settings, output io.Writer, manageHost bool) error {
	var runner HostRunner
	if manageHost {
		runner = startHost
	}
	return serveContext(ctx, s, output, runner)
}

func serveContext(ctx context.Context, s Settings, output io.Writer, runner HostRunner) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	profileLog, err := logs.Open(s.Home, "desktop", os.Stderr)
	if err != nil {
		return err
	}
	logger := profileLog.Logger
	defer func() {
		if result != nil {
			logger.Error("service.failed", result)
		}
		logger.Info("service.stop")
	}()
	if err := s.Prepare(); err != nil {
		return err
	}
	lock, err := filelock.TryLock(filepath.Join(s.RunDir(), "desktop.lock"))
	if err != nil {
		return fmt.Errorf("desktop already running for this profile; use axv daemon status: %w", err)
	}
	defer lock.Close()
	if err := os.MkdirAll(s.Desktop.DataDir, 0700); err != nil {
		return err
	}
	dbLock, err := filelock.TryLock(filepath.Join(s.Desktop.DataDir, "desktop.lock"))
	if err != nil {
		return fmt.Errorf("desktop database is already in use: %w", err)
	}
	defer dbLock.Close()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	s.RunnerExecutable, err = profile.PinExecutable(s.Home, executable)
	if err != nil {
		return fmt.Errorf("pin AXV runtime executable: %w", err)
	}
	state := launch.State{LaunchID: s.LaunchID, Executable: s.RunnerExecutable, InstanceID: compute.NewID("desktop"), PID: os.Getpid(), Fingerprint: s.Fingerprint()}
	logger = logger.With("instanceId", state.InstanceID, "launchId", s.LaunchID)
	s.Logger = logger
	ctx = diagnostics.Context(ctx, logger)
	restoreLog := diagnostics.RedirectStandard(logger)
	defer restoreLog()
	logger.Info("service.start")
	defer func() {
		current, err := launch.ReadState(s.Home)
		if err == nil && current.InstanceID == state.InstanceID {
			_ = os.Remove(s.StatePath())
		}
	}()
	gateway := computegateway.NewGateway("", computegateway.GatewayOptions{ConfigPath: s.Desktop.ConfigPath, ProfileHome: s.Home, Logger: logger, LogPath: profileLog.Path()})
	defer gateway.Close()
	coordinator := newComputeCoordinator(ctx, s, output, gateway, runner)
	defer coordinator.close()
	lockedReason := ""
	if s.ComputeOverride != "" {
		lockedReason = "AXV_COMPUTE_SOCKET supplies this connection. Change it in the launcher before configuring another VM service."
	}
	preparation, err := prepare.New(prepare.Options{Home: s.Home, ConfigurationLockedReason: lockedReason, Activator: coordinator.activatePrepared, Logger: logger.With("component", "preparation")})
	var preparationHandler http.Handler
	if err != nil {
		logger.Error("preparation.unavailable", err)
		fmt.Fprintf(output, "VM preparation unavailable: %v\n", err)
		preparationHandler = preparehttp.Unavailable(state.InstanceID, err)
		preparationError := err.Error()
		gateway.SetPreparationSnapshot(func() prepare.Check {
			source := "profile"
			if s.ComputeOverride != "" {
				source = "environment"
			}
			return prepare.Check{Platform: runtime.GOOS, Arch: runtime.GOARCH, ConfigurationSource: source, LockedReason: preparationError, Checks: []prepare.Requirement{{ID: "preparation", Label: "VM preparation", Status: "blocked", Detail: preparationError}}}
		})
	} else {
		preparationHandler = preparehttp.New(preparation, state.InstanceID)
		gateway.SetPreparationSnapshot(preparation.Snapshot)
		defer func() { coordinator.close(); preparation.Close() }()
	}
	return server.RunContext(ctx, s.Desktop, server.Options{LaunchID: s.LaunchID, RunnerExecutable: s.RunnerExecutable, ComputeGateway: gateway, PreparationHandler: preparationHandler, InstanceID: state.InstanceID, Ready: func(url string) error {
		logger.Info("service.ready", "instanceId", state.InstanceID)
		state.URL = url
		if err := jsonfile.Write(s.StatePath(), state, 0600); err != nil {
			return err
		}
		go coordinator.bootstrap()
		return nil
	}})
}
