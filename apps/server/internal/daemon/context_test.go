package daemon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app"
	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type cancellationSupervisor struct {
	loaded  map[string]bool
	checks  int
	started []Job
	stopped []Job
	onStart func(context.Context, Job) error
	onStop  func(context.Context, Job) error
}

func (s *cancellationSupervisor) Loaded(ctx context.Context, j Job) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.checks++
	return s.loaded[j.Label], nil
}

func (s *cancellationSupervisor) Start(ctx context.Context, j Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.started = append(s.started, j)
	s.loaded[j.Label] = true
	if s.onStart != nil {
		return s.onStart(ctx, j)
	}
	return nil
}

func (s *cancellationSupervisor) Stop(ctx context.Context, j Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.onStop != nil {
		if err := s.onStop(ctx, j); err != nil {
			return err
		}
	}
	s.stopped = append(s.stopped, j)
	delete(s.loaded, j.Label)
	return nil
}

func TestPreCanceledDaemonCommandsHaveNoSideEffects(t *testing.T) {
	for _, operation := range []string{"Start", "start", "Stop", "stop", "Restart", "restart", "Status", "status", "Logs", "EnsureHost", "ensureHost", "Launch", "launch"} {
		t.Run(operation, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "absent-profile")
			s := app.Settings{Inputs: desktoplaunch.Inputs{Home: home, Desktop: config.Config{Addr: "127.0.0.1:0", DataDir: filepath.Join(home, "data")}}, ComputeSettings: app.ComputeSettings{Compute: profile.Compute{Mode: "managed", Socket: filepath.Join(home, "host.sock")}, Host: &compute.Config{Socket: filepath.Join(home, "host.sock")}}}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			manager := &cancellationSupervisor{loaded: map[string]bool{}}
			factory := func() (supervisor, error) {
				t.Error("canceled command initialized native supervision")
				return manager, nil
			}
			resolve := func() (app.Settings, error) {
				t.Error("canceled command resolved or initialized configuration")
				return s, nil
			}
			var err error
			switch operation {
			case "Start":
				err = Start(ctx, s, io.Discard, false)
			case "start":
				err = start(ctx, s, io.Discard, false, manager)
			case "Stop":
				err = Stop(ctx, home, io.Discard)
			case "stop":
				err = stop(ctx, home, io.Discard, manager)
			case "Restart":
				err = Restart(ctx, s, io.Discard, false)
			case "restart":
				err = restart(ctx, s, io.Discard, false, manager)
			case "Status":
				err = Status(ctx, home, io.Discard)
			case "status":
				err = status(ctx, home, io.Discard, factory)
			case "Logs":
				err = Logs(ctx, home, io.Discard)
			case "EnsureHost":
				_, err = EnsureHost(ctx, s, io.Discard)
			case "ensureHost":
				_, err = ensureHost(ctx, s, io.Discard, manager)
			case "Launch":
				err = Launch(ctx, home, io.Discard, resolve)
			case "launch":
				err = launch(ctx, home, io.Discard, resolve, factory, func(string) error {
					t.Error("canceled command opened a browser")
					return nil
				})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled command = %v", err)
			}
			if manager.checks != 0 || len(manager.started) != 0 || len(manager.stopped) != 0 {
				t.Fatalf("canceled command touched supervision: %+v", manager)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("canceled command created a profile or manifest: %v", err)
			}
		})
	}
}

func TestReadinessCancellationCleansOnlyTheDesktopStartedByThisInvocation(t *testing.T) {
	for _, daily := range []bool{false, true} {
		for _, partialStart := range []bool{false, true} {
			t.Run(map[bool]string{false: "explicit", true: "daily"}[daily]+map[bool]string{false: "/waiting for readiness", true: "/partially accepted start"}[partialStart], func(t *testing.T) {
				s, original := hostOnlyFixture(t)
				s.Desktop = config.Config{Addr: "127.0.0.1:0", DataDir: filepath.Join(s.Home, "data")}
				host := original.Jobs[0]
				type requestKey struct{}
				ctx, cancel := context.WithCancel(context.WithValue(context.Background(), requestKey{}, "launch-request"))
				defer cancel()
				manager := &cancellationSupervisor{loaded: map[string]bool{host.Label: true}}
				manager.onStart = func(_ context.Context, j Job) error {
					if j.Role != "desktop" {
						t.Fatalf("started an independent %s job", j.Role)
					}
					cancel()
					if partialStart {
						return context.Canceled
					}
					return nil
				}
				manager.onStop = func(cleanup context.Context, j Job) error {
					if ctx.Err() == nil || cleanup.Err() != nil || cleanup.Value(requestKey{}) != "launch-request" {
						t.Fatalf("cleanup did not preserve the request independently of cancellation: request=%v cleanup=%v", ctx.Err(), cleanup.Err())
					}
					deadline, ok := cleanup.Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
						t.Fatalf("cleanup has no independent bounded deadline: %v %t", deadline, ok)
					}
					if j.Role != "desktop" || j.Label != manager.started[0].Label {
						t.Fatalf("cleanup targeted another invocation or independent host: %+v", j)
					}
					return nil
				}
				var err error
				if daily {
					err = launch(ctx, s.Home, io.Discard, func() (app.Settings, error) { return s, nil }, func() (supervisor, error) { return manager, nil }, func(string) error {
						t.Error("canceled launch opened a browser")
						return nil
					})
				} else {
					err = start(ctx, s, io.Discard, false, manager)
				}
				if !errors.Is(err, context.Canceled) || len(manager.started) != 1 || len(manager.stopped) != 1 || !manager.loaded[host.Label] {
					t.Fatalf("readiness cancellation lost ownership: %v starts=%+v stops=%+v host=%t", err, manager.started, manager.stopped, manager.loaded[host.Label])
				}
				current, err := readManifest(s.Home)
				if err != nil || current.OwnedHost == nil || current.OwnedHost.Fingerprint() != original.OwnedHost.Fingerprint() || current.job("host").Marker != host.Marker {
					t.Fatalf("cancellation changed independent host ownership: %+v %v", current, err)
				}
			})
		}
	}
}

func TestDaemonCommandsCancelWhileWaitingForTheProfileLock(t *testing.T) {
	for _, operation := range []string{"start", "stop", "restart", "launch", "ensureHost"} {
		t.Run(operation, func(t *testing.T) {
			s := managedHostSettings(t)
			lock, err := profile.Lock(s.Home)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager := &cancellationSupervisor{loaded: map[string]bool{}}
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "start":
					err = start(ctx, s, io.Discard, false, manager)
				case "stop":
					err = stop(ctx, s.Home, io.Discard, manager)
				case "restart":
					err = restart(ctx, s, io.Discard, false, manager)
				case "launch":
					err = launch(ctx, s.Home, io.Discard, func() (app.Settings, error) { return s, nil }, func() (supervisor, error) { return manager, nil }, func(string) error { return nil })
				case "ensureHost":
					_, err = ensureHost(ctx, s, io.Discard, manager)
				}
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("command did not wait for the existing profile owner: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lock wait cancellation = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("lock wait ignored cancellation")
			}
			if manager.checks != 0 || len(manager.started) != 0 || len(manager.stopped) != 0 {
				t.Fatalf("waiting command changed supervision: %+v", manager)
			}
			if _, err := os.Stat(manifestPath(s.Home)); !os.IsNotExist(err) {
				t.Fatalf("waiting command published ownership: %v", err)
			}
		})
	}
}

func TestDesktopShutdownWaitDoesNotTreatCanceledVerificationAsStopped(t *testing.T) {
	s := launchSettings(t)
	inspecting := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inspecting <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	state := desktoplaunch.State{InstanceID: "live-desktop", URL: server.URL}
	if err := jsonfile.Write(s.StatePath(), state, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitDesktopStopped(ctx, s.Home) }()
	select {
	case <-inspecting:
	case <-time.After(time.Second):
		t.Fatal("shutdown wait did not inspect the desktop")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled verification was treated as proof of shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("desktop shutdown verification ignored cancellation")
	}
}
