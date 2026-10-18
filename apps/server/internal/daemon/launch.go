package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

// Launch is the daily entry point. A verified running desktop keeps its startup
// settings and executable until the user explicitly restarts it.
func Launch(ctx context.Context, home string, output io.Writer, resolve func() (app.Settings, error)) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return launch(ctx, home, output, resolve, nativeSupervisor, func(url string) error { return OpenBrowser(ctx, url) })
}

func launch(ctx context.Context, home string, output io.Writer, resolve func() (app.Settings, error), supervisorFor func() (supervisor, error), open func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state, ok := readyDesktop(ctx, home); ok {
		return openDesktop(ctx, state, output, open, true)
	}
	lock, err := commandLock(ctx, home)
	if err != nil {
		return fmt.Errorf("wait for AXV launcher: %w", err)
	}
	defer lock.Close()
	if state, ok := readyDesktop(ctx, home); ok {
		lock.Close()
		return openDesktop(ctx, state, output, open, true)
	}
	// This also protects a foreground server that is still starting. Opening it
	// does not register it with the native supervisor or claim its lifetime.
	s := app.Settings{Inputs: desktoplaunch.Inputs{Home: home}}
	if err = s.Prepare(); err != nil {
		return err
	}
	profileLock, err := filelock.TryLock(filepath.Join(s.RunDir(), "desktop.lock"))
	if err != nil {
		if os.IsPermission(err) || os.IsNotExist(err) {
			return err
		}
		lock.Close()
		state, waitErr := waitReadyDesktop(ctx, home)
		if waitErr != nil {
			return fmt.Errorf("a desktop owns this profile but is unavailable; inspect its terminal or axv daemon logs before restarting: %w", waitErr)
		}
		return openDesktop(ctx, state, output, open, true)
	}
	profileLock.Close()

	supervisor, err := supervisorFor()
	if err != nil {
		return fmt.Errorf("cannot use background services: %w; use axv serve for a foreground desktop", err)
	}
	m, err := readManifest(home)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		for _, job := range m.Jobs {
			if job.Role != "desktop" {
				continue
			}
			loaded, loadErr := supervisor.Loaded(ctx, job)
			if loadErr != nil {
				return fmt.Errorf("cannot inspect background services: %w; use axv serve for a foreground desktop", loadErr)
			}
			if loaded {
				lock.Close()
				state, waitErr := waitReadyDesktop(ctx, home)
				if waitErr != nil {
					return fmt.Errorf("the registered AXV desktop is unavailable; run axv daemon logs, then axv daemon restart explicitly: %w", waitErr)
				}
				return openDesktop(ctx, state, output, open, true)
			}
		}
	}
	s, err = resolve()
	if err != nil {
		return err
	}
	if s.Home != home {
		return fmt.Errorf("resolved AXV profile changed during startup")
	}
	listener, err := net.Listen("tcp", s.Desktop.Addr)
	if err != nil {
		if state, ok := readyDesktop(ctx, home); ok {
			lock.Close()
			return openDesktop(ctx, state, output, open, true)
		}
		// A foreground server can acquire the profile and bind after the
		// first preflight. Wait for its identity instead of treating its
		// listener as an unrelated port conflict.
		profileLock, lockErr := filelock.TryLock(filepath.Join(s.RunDir(), "desktop.lock"))
		if lockErr == nil {
			profileLock.Close()
		} else if !os.IsPermission(lockErr) && !os.IsNotExist(lockErr) {
			lock.Close()
			state, waitErr := waitReadyDesktop(ctx, home)
			if waitErr == nil {
				return openDesktop(ctx, state, output, open, true)
			}
			return fmt.Errorf("a desktop acquired this profile but did not become ready; inspect its terminal or axv daemon logs: %w", waitErr)
		}
		return fmt.Errorf("desktop address %s is unavailable: %w", s.Desktop.Addr, err)
	}
	_ = listener.Close()
	replacement, err := newManifest(s)
	if err != nil {
		return err
	}
	// A host is independent of the desktop and remains under its recorded
	// ownership even when its native job is temporarily stopped.
	replacement = replacement.retainHost(m)
	if err = jsonfile.Write(manifestPath(home), replacement, 0600); err != nil {
		return err
	}
	var desktop Job
	for _, job := range replacement.Jobs {
		if job.Role == "desktop" {
			desktop = job
		}
	}
	if err = supervisor.Start(ctx, desktop); err != nil {
		err = errors.Join(err, cleanupFailedLaunch(ctx, supervisor, desktop))
		return fmt.Errorf("cannot start AXV background desktop: %w; run axv daemon logs or use axv serve", err)
	}
	// Host activation needs command.lock. Never retain it during readiness wait.
	lock.Close()
	state, err := waitReadyDesktop(ctx, home)
	if err != nil {
		// The record remains available for an explicit retry. This invocation
		// owns only the desktop it created, never a foreground service or host.
		err = errors.Join(err, stopLaunchJob(ctx, supervisor, desktop))
		return fmt.Errorf("AXV desktop did not become ready; run axv daemon logs: %w", err)
	}
	if state.LaunchID != replacement.Desktop.LaunchID {
		// A foreground server may win the profile lock after our preflight.
		// Remove only our new job, then reuse that verified server as-is.
		if err := stopLaunchJob(ctx, supervisor, desktop); err != nil {
			return err
		}
		return openDesktop(ctx, state, output, open, true)
	}
	fmt.Fprintln(output, "Managed for this login session; automatic login startup is not enabled.")
	return openDesktop(ctx, state, output, open, false)
}

func readyDesktop(ctx context.Context, home string) (desktoplaunch.State, bool) {
	state, err := desktoplaunch.ReadState(home)
	return state, err == nil && desktoplaunch.Verify(ctx, state) == nil
}

func waitReadyDesktop(ctx context.Context, home string) (desktoplaunch.State, error) {
	for {
		if state, ok := readyDesktop(ctx, home); ok {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return desktoplaunch.State{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// A native start can commit before its command reports cancellation. Inspect
// and clean up that exact job with a separate bounded cleanup context.
func cleanupFailedLaunch(ctx context.Context, supervisor supervisor, job Job) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	loaded, err := supervisor.Loaded(ctx, job)
	if err != nil {
		return err
	}
	if loaded {
		return supervisor.Stop(ctx, job)
	}
	return nil
}

func stopLaunchJob(ctx context.Context, supervisor supervisor, job Job) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx, job); err != nil {
		return fmt.Errorf("cannot remove this launcher's background job; inspect axv daemon status before continuing: %w", err)
	}
	return nil
}

func openDesktop(ctx context.Context, state desktoplaunch.State, output io.Writer, open func(string) error, reused bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fmt.Fprintf(output, "AXV running: %s\n", state.URL)
	if reused {
		if state.LaunchID == "" {
			fmt.Fprintln(output, "Reusing the foreground desktop with its current settings and executable. Restart it in its terminal to apply changes.")
		} else {
			fmt.Fprintln(output, "Reusing the background desktop with its current settings and executable. Run axv daemon restart to apply changes.")
		}
	}
	if err := open(state.URL); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Fprintf(output, "Browser warning: %v\nOpen %s in your browser.\n", err, state.URL)
	}
	return ctx.Err()
}
