package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

type launchSupervisor struct {
	mu      sync.Mutex
	loaded  map[string]bool
	starts  int
	stops   int
	stopErr error
	onStart func(Job)
}

func (s *launchSupervisor) Loaded(_ context.Context, j Job) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded[j.Label], nil
}

func (s *launchSupervisor) Start(_ context.Context, j Job) error {
	s.mu.Lock()
	s.starts++
	s.loaded[j.Label] = true
	s.mu.Unlock()
	if s.onStart != nil {
		s.onStart(j)
	}
	return nil
}

func (s *launchSupervisor) Stop(_ context.Context, j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops++
	if s.stopErr != nil {
		return s.stopErr
	}
	delete(s.loaded, j.Label)
	return nil
}

func launchSettings(t *testing.T) app.Settings {
	t.Helper()
	home := t.TempDir()
	s := app.Settings{Inputs: desktoplaunch.Inputs{Home: home, Desktop: config.Config{Addr: "127.0.0.1:0", DataDir: filepath.Join(home, "data")}}}
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	return s
}

func readyLaunchFixture(t *testing.T, s app.Settings) desktoplaunch.State {
	t.Helper()
	state := desktoplaunch.State{InstanceID: "process-instance", LaunchID: s.LaunchID, Fingerprint: s.Fingerprint()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": state.InstanceID, "launchId": state.LaunchID})
	}))
	t.Cleanup(srv.Close)
	state.URL = srv.URL
	if err := jsonfile.Write(s.StatePath(), state, 0600); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestDailyLaunchReusesIdentityBeforeReadingChangedConfiguration(t *testing.T) {
	for _, kind := range []string{"foreground", "background"} {
		t.Run(kind, func(t *testing.T) {
			s := launchSettings(t)
			if kind == "background" {
				s.LaunchID = "existing-launch"
			}
			state := readyLaunchFixture(t, s)
			// A running foreground desktop needs neither a valid daemon record nor
			// today's configuration, environment or native service manager.
			original := []byte("not consulted while healthy")
			if err := os.WriteFile(manifestPath(s.Home), original, 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err := launch(context.Background(), s.Home, &output, func() (app.Settings, error) {
				t.Fatal("read changed configuration while reusing a healthy desktop")
				return app.Settings{}, errors.New("invalid current configuration")
			}, func() (supervisor, error) {
				t.Fatal("claimed a healthy service through the native supervisor")
				return nil, errors.New("native manager unavailable")
			}, func(url string) error {
				if url != state.URL {
					t.Fatalf("opened %s", url)
				}
				return errors.New("browser unavailable")
			})
			if err != nil || !strings.Contains(output.String(), "Browser warning:") || !strings.Contains(output.String(), state.URL) || !strings.Contains(output.String(), "current settings and executable") {
				t.Fatalf("reuse/browser result: %v\n%s", err, output.String())
			}
			remaining, err := os.ReadFile(manifestPath(s.Home))
			if err != nil || !bytes.Equal(remaining, original) {
				t.Fatalf("rewrote existing ownership: %q %v", remaining, err)
			}
		})
	}
}

func TestConcurrentDailyLaunchesPublishOnlyOneDesktopJob(t *testing.T) {
	s := launchSettings(t)
	manager := &launchSupervisor{loaded: map[string]bool{}}
	started := make(chan app.Settings, 1)
	manager.onStart = func(job Job) {
		var snapshot desktoplaunch.Snapshot
		if err := jsonfile.Read(job.Marker, &snapshot); err != nil {
			t.Error(err)
		}
		started <- app.SettingsFromSnapshot(snapshot)
	}
	var resolutions, opened atomic.Int32
	resolve := func() (app.Settings, error) { resolutions.Add(1); return s, nil }
	inspections := make(chan struct{}, 2)
	factory := func() (supervisor, error) { inspections <- struct{}{}; return manager, nil }
	open := func(string) error { opened.Add(1); return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 2)
	go func() { done <- launch(ctx, s.Home, io.Discard, resolve, factory, open) }()
	var snapshot app.Settings
	select {
	case snapshot = <-started:
	case <-ctx.Done():
		t.Fatal("first launcher did not publish a job")
	}
	<-inspections
	go func() { done <- launch(ctx, s.Home, io.Discard, resolve, factory, open) }()
	select {
	case <-inspections:
	case <-ctx.Done():
		t.Fatal("concurrent launcher did not inspect pending native job")
	}
	readyLaunchFixture(t, snapshot)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if resolutions.Load() != 1 || opened.Load() != 2 || manager.starts != 1 || manager.stops != 0 || snapshot.LaunchID == "" {
		t.Fatalf("unexpected launches: resolves=%d opens=%d starts=%d stops=%d launch=%q", resolutions.Load(), opened.Load(), manager.starts, manager.stops, snapshot.LaunchID)
	}
}

func TestDailyLaunchDoesNotRestartUnavailableOwnedDesktop(t *testing.T) {
	s, m := managedHostFixture(t)
	manager := &launchSupervisor{loaded: map[string]bool{m.Jobs[0].Label: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := launch(ctx, s.Home, io.Discard, func() (app.Settings, error) {
		t.Fatal("resolved changed settings before waiting for the registered desktop")
		return s, nil
	}, func() (supervisor, error) { return manager, nil }, func(string) error {
		t.Fatal("opened an unverified desktop")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "axv daemon restart explicitly") || manager.starts != 0 || manager.stops != 0 {
		t.Fatalf("changed unavailable owned service: %v starts=%d stops=%d", err, manager.starts, manager.stops)
	}
}

func TestDailyLaunchWaitsForForegroundStartupWithoutAdoptingIt(t *testing.T) {
	s := launchSettings(t)
	profileLock, err := filelock.TryLock(filepath.Join(s.RunDir(), "desktop.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer profileLock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- launch(ctx, s.Home, io.Discard, func() (app.Settings, error) {
			t.Error("resolved configuration while foreground startup owns the profile")
			return s, nil
		}, func() (supervisor, error) {
			t.Error("requested native supervisor for foreground startup")
			return nil, errors.New("should not be called")
		}, func(string) error { return nil })
	}()
	readyLaunchFixture(t, s)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manifestPath(s.Home)); !os.IsNotExist(err) {
		t.Fatalf("claimed foreground ownership: %v", err)
	}
}

func TestDailyLaunchWithoutNativeManagerExplainsForegroundAlternative(t *testing.T) {
	s := launchSettings(t)
	err := launch(context.Background(), s.Home, io.Discard, func() (app.Settings, error) { return s, nil }, func() (supervisor, error) { return nil, errors.New("no user manager") }, func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "axv serve") {
		t.Fatal(err)
	}
	if _, err := os.Stat(manifestPath(s.Home)); !os.IsNotExist(err) {
		t.Fatalf("registered a job without native supervision: %v", err)
	}
}

func TestDailyLaunchReusesForegroundThatBindsDuringResolution(t *testing.T) {
	s := launchSettings(t)
	manager := &launchSupervisor{loaded: map[string]bool{}}
	state := desktoplaunch.State{InstanceID: "racing-foreground"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": state.InstanceID, "launchId": ""})
	}))
	defer srv.Close()
	s.Desktop.Addr = srv.Listener.Addr().String()
	state.URL = srv.URL
	var profileLock *os.File
	written := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := launch(ctx, s.Home, io.Discard, func() (app.Settings, error) {
		var err error
		profileLock, err = filelock.TryLock(filepath.Join(s.RunDir(), "desktop.lock"))
		if err != nil {
			return s, err
		}
		time.AfterFunc(100*time.Millisecond, func() { written <- jsonfile.Write(s.StatePath(), state, 0600) })
		return s, nil
	}, func() (supervisor, error) { return manager, nil }, func(url string) error {
		if url != state.URL {
			t.Fatalf("opened %s", url)
		}
		return nil
	})
	if profileLock != nil {
		profileLock.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if manager.starts != 0 || manager.stops != 0 {
		t.Fatal("adopted foreground service after port preflight race")
	}
}

func TestDailyLaunchForegroundRaceCleansOnlyItsOwnJob(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "cleanup failure"}[cleanupFails], func(t *testing.T) {
			s, m := managedHostFixture(t)
			s.LaunchID = ""
			s.Desktop = config.Config{Addr: "127.0.0.1:0", DataDir: filepath.Join(s.Home, "data")}
			host, err := hostJob(s, m.Jobs[0].Args[0])
			if err != nil {
				t.Fatal(err)
			}
			m.OwnedHost = s.Host
			m.Jobs = append([]Job{host}, m.Jobs...)
			if err := jsonfile.Write(manifestPath(s.Home), m, 0600); err != nil {
				t.Fatal(err)
			}
			manager := &launchSupervisor{loaded: map[string]bool{host.Label: true}}
			if cleanupFails {
				manager.stopErr = errors.New("native stop failed")
			}
			manager.onStart = func(job Job) {
				published, err := readManifest(s.Home)
				if err != nil || published.OwnedHost == nil || len(published.Jobs) != 2 || published.Jobs[0].Marker != host.Marker || published.Desktop.LaunchID == "" {
					t.Fatalf("lost independent host ownership: %#v %v", published, err)
				}
				readyLaunchFixture(t, s)
			}
			opened := false
			err = launch(context.Background(), s.Home, io.Discard, func() (app.Settings, error) { return s, nil }, func() (supervisor, error) { return manager, nil }, func(string) error { opened = true; return nil })
			if cleanupFails && (err == nil || !strings.Contains(err.Error(), "cannot remove this launcher's background job") || opened) {
				t.Fatalf("masked cleanup failure: %v opened=%t", err, opened)
			}
			if !cleanupFails && (err != nil || !opened) {
				t.Fatalf("foreground reuse failed: %v opened=%t", err, opened)
			}
			if manager.starts != 1 || manager.stops != 1 || !manager.loaded[host.Label] {
				t.Fatalf("wrong ownership cleanup: starts=%d stops=%d host=%t", manager.starts, manager.stops, manager.loaded[host.Label])
			}
		})
	}
}
