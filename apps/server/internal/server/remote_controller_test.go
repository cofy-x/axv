package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/tailnet"
)

type controlledRemote struct {
	mu     sync.Mutex
	status tailnet.Status
	closes int
	close  func() error
}

func (n *controlledRemote) Status() tailnet.Status { n.mu.Lock(); defer n.mu.Unlock(); return n.status }
func (n *controlledRemote) Close() error {
	n.mu.Lock()
	n.closes++
	close := n.close
	n.mu.Unlock()
	if close != nil {
		return close()
	}
	return nil
}
func (n *controlledRemote) setState(state tailnet.State) {
	n.mu.Lock()
	n.status.State = state
	n.mu.Unlock()
}
func (n *controlledRemote) closeCount() int { n.mu.Lock(); defer n.mu.Unlock(); return n.closes }

func remoteControllerFixture(t *testing.T) (*remoteController, *[]*controlledRemote, *[]profile.Remote) {
	t.Helper()
	home := t.TempDir()
	if _, err := profile.InitializeDesktop(home); err != nil {
		t.Fatal(err)
	}
	nodes, inputs := []*controlledRemote{}, []profile.Remote{}
	c := newRemoteControllerWithStarter(context.Background(), home, true, func(ctx context.Context, settings profile.Remote) (remoteService, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		inputs = append(inputs, settings)
		node := &controlledRemote{status: tailnet.Status{State: tailnet.StateReady, URL: "https://workstation.example.ts.net"}}
		nodes = append(nodes, node)
		return node, nil
	})
	t.Cleanup(func() { _ = c.Close() })
	return c, &nodes, &inputs
}

func remoteSwitch(t *testing.T, c *remoteController, enabled bool) {
	t.Helper()
	if err := c.SetEnabled(context.Background(), enabled); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteSwitchPersistsAndDoesNotCreateDefaultNodeState(t *testing.T) {
	c, nodes, _ := remoteControllerFixture(t)
	if status := c.Status(); status.Enabled || !status.Supported || status.State != tailnet.StateDisabled {
		t.Fatalf("default: %+v", status)
	}
	remoteSwitch(t, c, false)
	if len(*nodes) != 0 {
		t.Fatal("disabled switch started a node")
	}
	if _, err := os.Stat(filepath.Join(c.home, "tailscale")); !os.IsNotExist(err) {
		t.Fatalf("disabled switch created node state: %v", err)
	}
	remoteSwitch(t, c, true)
	remoteSwitch(t, c, true)
	if len(*nodes) != 1 || !c.Status().Enabled {
		t.Fatal("healthy enable was not idempotent")
	}
	if settings, err := profile.ReadRemote(c.home); err != nil || !settings.Enabled {
		t.Fatalf("enable not persisted: %+v %v", settings, err)
	}
	if err := os.Mkdir(filepath.Join(c.home, "tailscale"), 0700); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(c.home, "tailscale", "tailscaled.state")
	if err := os.WriteFile(retained, []byte("private-existing-credentials"), 0600); err != nil {
		t.Fatal(err)
	}
	remoteSwitch(t, c, false)
	remoteSwitch(t, c, false)
	if (*nodes)[0].closeCount() != 1 || c.Status().Enabled || c.Status().State != tailnet.StateDisabled {
		t.Fatal("disable was not limited and idempotent")
	}
	if data, err := os.ReadFile(retained); err != nil || string(data) != "private-existing-credentials" {
		t.Fatal("disable reset enrollment state")
	}
	if settings, err := profile.ReadRemote(c.home); err != nil || settings.Enabled {
		t.Fatalf("disable not persisted: %+v %v", settings, err)
	}
}

func TestRemoteServiceRestartUsesCurrentDurableSettings(t *testing.T) {
	c, nodes, _ := remoteControllerFixture(t)
	remoteSwitch(t, c, true)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	settings, err := profile.ReadRemote(c.home)
	if err != nil || !settings.Enabled {
		t.Fatal("service shutdown overwrote the durable switch")
	}
	started := 0
	restarted := newRemoteControllerWithStarter(context.Background(), c.home, true, func(_ context.Context, got profile.Remote) (remoteService, error) {
		started++
		if got != settings {
			t.Fatalf("startup used stale settings: %+v", got)
		}
		return &controlledRemote{status: tailnet.Status{State: tailnet.StateNeedsLogin}}, nil
	})
	defer restarted.Close()
	if started != 1 || !restarted.Status().Enabled || (*nodes)[0].closeCount() != 1 {
		t.Fatal("service restart lost its enabled choice or reused its old node")
	}
}

func TestRemoteRetryClosesPreviousNodeAndReadsCurrentIdentitySettings(t *testing.T) {
	for _, state := range []tailnet.State{tailnet.StateError, tailnet.StateStopped} {
		t.Run(string(state), func(t *testing.T) {
			c, nodes, inputs := remoteControllerFixture(t)
			remoteSwitch(t, c, true)
			(*nodes)[0].setState(state)
			path := filepath.Join(c.home, "config.json")
			if err := os.WriteFile(path, []byte(`{"remote":{"enabled":true,"hostname":"axv-office","owner_user_id":"456","future":"preserve"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			(*nodes)[0].close = func() error {
				if len(*nodes) != 1 {
					t.Error("replacement started before old node closed")
				}
				return nil
			}
			remoteSwitch(t, c, true)
			if len(*nodes) != 2 || (*nodes)[0].closeCount() != 1 || (*inputs)[1] != (profile.Remote{Enabled: true, Hostname: "axv-office", OwnerUserID: "456"}) {
				t.Fatalf("retry used a stale identity configuration: %+v", *inputs)
			}
			data, _ := os.ReadFile(path)
			if !strings.Contains(string(data), `"future":"preserve"`) {
				t.Fatal("retry rewrote unrelated identity settings")
			}
		})
	}
}

func TestRemoteTransitionsAreSerialAndStatusDoesNotReportClosingNodeReady(t *testing.T) {
	c, nodes, _ := remoteControllerFixture(t)
	remoteSwitch(t, c, true)
	entered, release := make(chan struct{}), make(chan struct{})
	(*nodes)[0].close = func() error { close(entered); <-release; return nil }
	disabled := make(chan error, 1)
	go func() { disabled <- c.SetEnabled(context.Background(), false) }()
	<-entered
	if status := c.Status(); status.Enabled || status.State == tailnet.StateReady {
		t.Fatalf("closing node still ready: %+v", status)
	}
	enabled := make(chan error, 1)
	go func() { enabled <- c.SetEnabled(context.Background(), true) }()
	select {
	case err := <-enabled:
		t.Fatalf("overlapping transition: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if err := <-enabled; err != nil {
		t.Fatal(err)
	}
	if len(*nodes) != 2 || !c.Status().Enabled {
		t.Fatal("serial replacement did not finish")
	}
}

func TestRemoteConfigurationFailureAndBusyProfilePreserveLiveTransport(t *testing.T) {
	for _, kind := range []string{"invalid configuration", "busy profile"} {
		t.Run(kind, func(t *testing.T) {
			c, nodes, _ := remoteControllerFixture(t)
			remoteSwitch(t, c, true)
			before := c.Status()
			wantCode := "remote_config_failed"
			if kind == "busy profile" {
				lock, err := profile.Lock(c.home)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				wantCode = "remote_config_busy"
			} else if err := os.WriteFile(filepath.Join(c.home, "config.json"), []byte(`{"remote":null}`), 0600); err != nil {
				t.Fatal(err)
			}
			var failure *remoteControlError
			err := c.SetEnabled(context.Background(), false)
			if !errors.As(err, &failure) || failure.code != wantCode || c.Status() != before || (*nodes)[0].closeCount() != 0 {
				t.Fatalf("configuration failure changed live connection: %+v %v", c.Status(), err)
			}
		})
	}
}

func TestRemoteCloseFailurePreventsDuplicateNodesUntilServiceRestart(t *testing.T) {
	c, nodes, _ := remoteControllerFixture(t)
	remoteSwitch(t, c, true)
	(*nodes)[0].close = func() error { return errors.New("fixture close failure") }
	for _, enabled := range []bool{false, false, true} {
		var failure *remoteControlError
		err := c.SetEnabled(context.Background(), enabled)
		if !errors.As(err, &failure) || failure.code != "remote_close_failed" {
			t.Fatalf("failed close was hidden: %v", err)
		}
	}
	if len(*nodes) != 1 || (*nodes)[0].closeCount() != 1 || c.Status().Enabled || c.Status().State != tailnet.StateError {
		t.Fatal("failed close allowed a duplicate node or changed the saved choice")
	}
	if saved, err := profile.ReadRemote(c.home); err != nil || saved.Enabled {
		t.Fatal("disable intent was not retained after a close error")
	}
}

func TestRemoteUnsupportedCancellationAndClosedServiceDoNotEnable(t *testing.T) {
	c, nodes, _ := remoteControllerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(c.SetEnabled(ctx, true), context.Canceled) {
		t.Fatal("canceled request enabled remote access")
	}
	c.supported = false
	var failure *remoteControlError
	if !errors.As(c.SetEnabled(context.Background(), true), &failure) || failure.status != http.StatusNotImplemented {
		t.Fatal("unsupported system enabled remote access")
	}
	remoteSwitch(t, c, false)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.As(c.SetEnabled(context.Background(), true), &failure) || failure.code != "remote_stopping" {
		t.Fatal("closed controller restarted a node")
	}
	if len(*nodes) != 0 {
		t.Fatal("invalid transition started a node")
	}
}

func TestRemoteImmediateStartFailureRemainsRetryable(t *testing.T) {
	c, nodes, _ := remoteControllerFixture(t)
	start := c.start
	c.start = func(context.Context, profile.Remote) (remoteService, error) {
		return nil, errors.New("fixture unavailable")
	}
	remoteSwitch(t, c, true)
	if !c.Status().Enabled || c.Status().State != tailnet.StateError {
		t.Fatal("start failure hid the saved switch or diagnostic")
	}
	c.start = start
	remoteSwitch(t, c, true)
	if len(*nodes) != 1 || c.Status().State != tailnet.StateReady {
		t.Fatal("explicit retry did not start the saved node")
	}
}
