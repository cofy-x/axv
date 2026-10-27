package management_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/management"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type configurationGate struct {
	state  management.DesktopState
	inside bool
}

func (g *configurationGate) ChangeConfiguration(ctx context.Context, change func(management.DesktopState) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g.inside = true
	defer func() { g.inside = false }()
	return change(g.state)
}

func TestConfigurationSaveKeepsOwnershipChecksAndRevisionInsideBothGuards(t *testing.T) {
	home := t.TempDir()
	gate := &configurationGate{}
	busy := true
	service := management.New(context.Background(), management.Options{
		Home: home, InstanceID: "desktop-test", Desktop: gate,
		Busy: func() bool {
			if !gate.inside {
				t.Error("publication ownership was checked outside the desktop transition guard")
			}
			if lock, err := profile.Lock(home); err == nil {
				_ = lock.Close()
				t.Error("publication ownership was checked without the shared profile lock")
			}
			return busy
		},
	})
	defer service.Close()
	view, err := service.Configuration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	input := management.ConfigUpdate{
		InstanceID: "desktop-test", ExpectedRevision: view.Revision,
		Access:  &management.AccessSettings{Origin: "https://desktop.example.com"},
		Account: &management.AccountSettings{}, TokenAction: "keep",
	}
	var failure *management.Problem
	if _, err = service.SaveConfiguration(context.Background(), input); !errors.As(err, &failure) || failure.Code != "cloudflare_publications_active" {
		t.Fatalf("active publications did not block the complete save: %v", err)
	}
	if _, err = os.Stat(filepath.Join(home, "config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected save changed the profile: %v", err)
	}
	busy = false
	saved, err := service.SaveConfiguration(context.Background(), input)
	if err != nil || saved.Access.Origin != input.Access.Origin || saved.Revision == view.Revision {
		t.Fatalf("guarded save did not publish its new revision: %+v %v", saved, err)
	}
	if _, err = service.SaveConfiguration(context.Background(), input); !errors.As(err, &failure) || failure.Code != "cloudflare_config_conflict" {
		t.Fatalf("stale revision replaced a newer save: %v", err)
	}
}

func TestConfigurationSaveUsesTheGuardedDesktopState(t *testing.T) {
	for _, tc := range []struct {
		state management.DesktopState
		code  string
	}{
		{management.DesktopState{Active: true}, "cloudflare_disable_required"},
		{management.DesktopState{Stopping: true}, "cloudflare_stopping"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			gate := &configurationGate{state: tc.state}
			service := management.New(context.Background(), management.Options{
				Home: t.TempDir(), InstanceID: "desktop-test", Desktop: gate,
				Busy: func() bool {
					t.Error("inactive desktop state was required before taking the profile lock")
					return false
				},
			})
			defer service.Close()
			var failure *management.Problem
			_, err := service.SaveConfiguration(context.Background(), management.ConfigUpdate{
				InstanceID: "desktop-test", Access: &management.AccessSettings{},
				Account: &management.AccountSettings{}, TokenAction: "clear",
			})
			if !errors.As(err, &failure) || failure.Code != tc.code {
				t.Fatalf("ignored the serialized desktop state: %v", err)
			}
		})
	}
}
