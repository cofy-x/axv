package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type controlledCloudflare struct {
	mu     sync.Mutex
	status cloudflare.Status
	closes int
	close  func() error
}

func (s *controlledCloudflare) Status() cloudflare.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}
func (s *controlledCloudflare) Close() error {
	s.mu.Lock()
	s.closes++
	close := s.close
	s.mu.Unlock()
	if close != nil {
		return close()
	}
	return nil
}
func (s *controlledCloudflare) setState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = state
}
func (s *controlledCloudflare) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}

func cloudflareControllerFixture(t *testing.T) (*cloudflareController, *[]*controlledCloudflare, *[]profile.Cloudflare) {
	t.Helper()
	home := t.TempDir()
	settings := profile.Cloudflare{Listen: profile.DefaultCloudflareListen, Origin: "https://axv.example.com", Issuer: "https://my-team.cloudflareaccess.com", Audience: "private-audience", OwnerSubject: "private-owner"}
	data, _ := json.Marshal(map[string]any{"cloudflare": settings})
	if err := os.WriteFile(filepath.Join(home, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	services, inputs := []*controlledCloudflare{}, []profile.Cloudflare{}
	c := newCloudflareControllerWithStarter(context.Background(), home, func(ctx context.Context, settings profile.Cloudflare) (cloudflareService, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		inputs = append(inputs, settings)
		service := &controlledCloudflare{status: cloudflare.Status{State: "listening", URL: settings.Origin, Listen: settings.Listen}}
		services = append(services, service)
		return service, nil
	})
	t.Cleanup(func() { _ = c.Close() })
	return c, &services, &inputs
}

func cloudflareSwitch(t *testing.T, c *cloudflareController, enabled bool) {
	t.Helper()
	if err := c.SetEnabled(context.Background(), enabled); err != nil {
		t.Fatal(err)
	}
}

func TestCloudflareDesktopRechecksHistoricalPublicationSitesAtStartupAndEnable(t *testing.T) {
	original, _, _ := cloudflareControllerFixture(t)
	cloudflareSwitch(t, original, true)
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	allowed, starts := false, 0
	c := newCloudflareControllerGuarded(context.Background(), original.home, func(_ context.Context, settings profile.Cloudflare) (cloudflareService, error) {
		starts++
		return &controlledCloudflare{status: cloudflare.Status{State: "listening", URL: settings.Origin}}, nil
	}, func(string) error {
		if !allowed {
			return errors.New("This site belongs to a retained publication.")
		}
		return nil
	})
	t.Cleanup(func() { _ = c.Close() })
	if status := c.Status(); starts != 0 || status.State != "error" || !status.Enabled {
		t.Fatalf("startup exposed a reserved desktop site: %+v, starts=%d", status, starts)
	}
	cloudflareSwitch(t, c, false)
	cloudflareSwitch(t, c, true)
	if status := c.Status(); starts != 0 || status.State != "error" {
		t.Fatalf("enable bypassed the site guard: %+v, starts=%d", status, starts)
	}
	allowed = true
	cloudflareSwitch(t, c, true)
	if status := c.Status(); starts != 1 || status.State != "listening" {
		t.Fatalf("an explicitly valid site did not recover: %+v, starts=%d", status, starts)
	}
}

func TestCloudflareDisabledStartupAndPersistentSwitch(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	c := newCloudflareControllerWithStarter(context.Background(), home, func(context.Context, profile.Cloudflare) (cloudflareService, error) {
		t.Fatal("disabled ingress created a listener")
		return nil, nil
	})
	defer c.Close()
	if status := c.Status(); status.Enabled || status.Configured || status.State != "disabled" {
		t.Fatalf("default status: %+v", status)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("default startup initialized state")
	}
	c, services, _ := cloudflareControllerFixture(t)
	if status := c.Status(); status.Enabled || !status.Configured || status.State != "disabled" {
		t.Fatalf("configured but disabled startup: %+v", status)
	}
	cloudflareSwitch(t, c, true)
	cloudflareSwitch(t, c, true)
	if len(*services) != 1 || c.Status().State != "listening" || !c.Status().Enabled {
		t.Fatal("healthy enable was not idempotent")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if saved, err := profile.ReadCloudflare(c.home); err != nil || !saved.Enabled || (*services)[0].closeCount() != 1 {
		t.Fatalf("shutdown lost durable choice or failed to close ingress: %+v %v", saved, err)
	}
	restarted := newCloudflareControllerWithStarter(context.Background(), c.home, c.start)
	defer restarted.Close()
	if len(*services) != 2 || restarted.Status().State != "listening" {
		t.Fatal("enabled profile did not restart its own ingress")
	}
	cloudflareSwitch(t, restarted, false)
	cloudflareSwitch(t, restarted, false)
	if (*services)[1].closeCount() != 1 || restarted.Status().Enabled || restarted.Status().State != "disabled" {
		t.Fatal("disable was not limited and idempotent")
	}
}

func TestCloudflareRetryReadsCurrentConfigurationAndClosesPreviousIngress(t *testing.T) {
	c, services, inputs := cloudflareControllerFixture(t)
	cloudflareSwitch(t, c, true)
	(*services)[0].setState("error")
	settings := (*inputs)[0]
	settings.Origin, settings.OwnerSubject = "https://other.example.com", "replacement-subject"
	data, _ := json.Marshal(map[string]any{"cloudflare": settings})
	if err := os.WriteFile(filepath.Join(c.home, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	(*services)[0].close = func() error {
		if len(*services) != 1 {
			t.Error("replacement started before previous ingress closed")
		}
		return nil
	}
	cloudflareSwitch(t, c, true)
	if len(*services) != 2 || (*services)[0].closeCount() != 1 || (*inputs)[1] != settings {
		t.Fatalf("retry used stale identity settings: %+v", *inputs)
	}
}

func TestCloudflareConfigurationFailureAndBusyLockPreserveLiveIngress(t *testing.T) {
	for _, busy := range []bool{false, true} {
		c, services, _ := cloudflareControllerFixture(t)
		cloudflareSwitch(t, c, true)
		before := c.Status()
		code := "cloudflare_config_failed"
		if busy {
			lock, err := profile.Lock(c.home)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			code = "cloudflare_config_busy"
		} else if err := os.WriteFile(filepath.Join(c.home, "config.json"), []byte(`{"cloudflare":null}`), 0600); err != nil {
			t.Fatal(err)
		}
		var failure *cloudflareControlError
		err := c.SetEnabled(context.Background(), false)
		if !errors.As(err, &failure) || failure.code != code || c.Status() != before || (*services)[0].closeCount() != 0 {
			t.Fatalf("failed save changed the live ingress: %+v %v", c.Status(), err)
		}
	}
}

func TestCloudflareTransitionSerializesAndFailedCloseBlocksReplacement(t *testing.T) {
	c, services, _ := cloudflareControllerFixture(t)
	cloudflareSwitch(t, c, true)
	entered, release := make(chan struct{}), make(chan struct{})
	(*services)[0].close = func() error { close(entered); <-release; return nil }
	disabled := make(chan error, 1)
	go func() { disabled <- c.SetEnabled(context.Background(), false) }()
	<-entered
	if status := c.Status(); status.Enabled || status.State == "listening" {
		t.Fatalf("closing ingress advertised as listening: %+v", status)
	}
	enabled := make(chan error, 1)
	go func() { enabled <- c.SetEnabled(context.Background(), true) }()
	select {
	case err := <-enabled:
		t.Fatalf("overlapping ingress transition: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if err := <-enabled; err != nil {
		t.Fatal(err)
	}
	if len(*services) != 2 {
		t.Fatal("serial replacement did not complete")
	}
	(*services)[1].close = func() error { return errors.New("private fixture close failure") }
	for _, enabled := range []bool{false, true, false} {
		var failure *cloudflareControlError
		if !errors.As(c.SetEnabled(context.Background(), enabled), &failure) || failure.code != "cloudflare_close_failed" {
			t.Fatal("failed close was hidden")
		}
	}
	if len(*services) != 2 || (*services)[1].closeCount() != 1 || c.Status().State != "error" || c.Status().Enabled {
		t.Fatal("failed close allowed a replacement or lost disable intent")
	}
}

func TestCloudflareStartAndConfigurationFailuresAreVisibleAndRetryable(t *testing.T) {
	c, services, _ := cloudflareControllerFixture(t)
	start := c.start
	c.start = func(context.Context, profile.Cloudflare) (cloudflareService, error) {
		return nil, errors.New("private fixture start failure")
	}
	cloudflareSwitch(t, c, true)
	if status := c.Status(); !status.Enabled || !status.Configured || status.State != "error" || status.Error == "" {
		t.Fatalf("saved switch or startup diagnostic lost: %+v", status)
	}
	c.start = start
	cloudflareSwitch(t, c, true)
	if len(*services) != 1 || c.Status().State != "listening" {
		t.Fatal("explicit retry failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(c.SetEnabled(ctx, false), context.Canceled) {
		t.Fatal("canceled request changed the ingress")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	var failure *cloudflareControlError
	if !errors.As(c.SetEnabled(context.Background(), true), &failure) || failure.code != "cloudflare_stopping" {
		t.Fatal("closed controller restarted an ingress")
	}
	if err := os.WriteFile(filepath.Join(c.home, "config.json"), []byte(`{"cloudflare":{"enabled":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	invalid := newCloudflareControllerWithStarter(context.Background(), c.home, start)
	defer invalid.Close()
	if status := invalid.Status(); status.State != "error" || !status.Enabled || status.Configured || status.Error == "" || len(*services) != 1 {
		t.Fatalf("invalid configuration started an ingress or hid its error: %+v", status)
	}
}

func TestCloudflareConfigurationCanBeCompletedWithoutDesktopRestart(t *testing.T) {
	c, services, inputs := cloudflareControllerFixture(t)
	path := filepath.Join(c.home, "config.json")
	settings, err := profile.ReadCloudflare(c.home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"cloudflare":{"enabled":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if status := c.Status(); status.Configured || status.State != "disabled" {
		t.Fatalf("incomplete profile reported configured: %+v", status)
	}
	data, _ := json.Marshal(map[string]any{"cloudflare": settings})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if status := c.Status(); !status.Configured || status.Enabled || status.State != "disabled" || len(*services) != 0 {
		t.Fatalf("configuration completion required restart or started networking: %+v", status)
	}
	cloudflareSwitch(t, c, true)
	if len(*services) != 1 || (*inputs)[0].Origin != settings.Origin {
		t.Fatal("completed configuration could not be explicitly enabled")
	}
	settings.Origin = "https://replacement.example.com"
	data, _ = json.Marshal(map[string]any{"cloudflare": settings})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if status := c.Status(); status.URL != "https://axv.example.com" || !status.Configured || !status.Enabled {
		t.Fatalf("external edit silently changed the running identity: %+v", status)
	}
}

func TestCloudflareDisabledConfigurationRepairClearsStaleStartupError(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, []byte(`{"cloudflare":{"enabled":false,"listen":"0.0.0.0:3002"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := newCloudflareControllerWithStarter(context.Background(), home, func(context.Context, profile.Cloudflare) (cloudflareService, error) {
		t.Fatal("repairing a disabled configuration automatically started an ingress")
		return nil, nil
	})
	defer c.Close()
	if status := c.Status(); status.State != "error" || status.Error == "" || status.Configured || status.Enabled {
		t.Fatalf("invalid disabled configuration did not report its error: %+v", status)
	}
	settings := profile.Cloudflare{Origin: "https://axv.example.com", Issuer: "https://my-team.cloudflareaccess.com", Audience: "application-audience", OwnerSubject: "owner-stable-subject"}
	data, _ := json.Marshal(map[string]any{"cloudflare": settings})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if status := c.Status(); status.State != "disabled" || !status.Configured || status.Enabled || status.Error != "" || status.URL != "" || status.Listen != profile.DefaultCloudflareListen {
		t.Fatalf("repaired disabled configuration retained a stale error: %+v", status)
	}
}
