package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestResolveCompleteDesktopPrecedenceWithoutSideEffects(t *testing.T) {
	home := filepath.Join(t.TempDir(), "profile")
	defaultData := filepath.Join(home, "data")
	profileData := filepath.Join(home, "profile-data")
	for _, tc := range []struct {
		name      string
		overrides Overrides
		env       map[string]string
		desktop   profile.Desktop
		addr      string
		data      string
	}{
		{name: "defaults", addr: "127.0.0.1:3001", data: defaultData},
		{name: "empty environment", env: map[string]string{"ADDR": "", "PORT": "", "DATA_DIR": ""}, addr: "127.0.0.1:3001", data: defaultData},
		{name: "profile", desktop: profile.Desktop{Addr: "localhost:3002", DataDir: profileData}, addr: "localhost:3002", data: profileData},
		{name: "port overrides profile", env: map[string]string{"PORT": "4000"}, desktop: profile.Desktop{Addr: "localhost:3002"}, addr: "127.0.0.1:4000", data: defaultData},
		{name: "address overrides port and profile", env: map[string]string{"ADDR": "localhost:4001", "PORT": "invalid"}, desktop: profile.Desktop{Addr: "invalid"}, addr: "localhost:4001", data: defaultData},
		{name: "environment data overrides profile", env: map[string]string{"DATA_DIR": "./env data"}, desktop: profile.Desktop{DataDir: profileData}, addr: "127.0.0.1:3001", data: "./env data"},
		{name: "flags override environment and profile", overrides: Overrides{Addr: "[::1]:4002", DataDir: "./flag data"}, env: map[string]string{"ADDR": "invalid", "PORT": "invalid", "DATA_DIR": "./env data"}, desktop: profile.Desktop{Addr: "invalid", DataDir: profileData}, addr: "[::1]:4002", data: "./flag data"},
		{name: "partial address override", overrides: Overrides{Addr: ":4003"}, env: map[string]string{"DATA_DIR": "./env"}, addr: ":4003", data: "./env"},
		{name: "partial data override", overrides: Overrides{DataDir: "./flag"}, env: map[string]string{"PORT": "4004"}, addr: "127.0.0.1:4004", data: "./flag"},
		{name: "ephemeral port", overrides: Overrides{Addr: "127.0.0.1:0"}, addr: "127.0.0.1:0", data: defaultData},
		{name: "named port remains supported", overrides: Overrides{Addr: "localhost:http"}, addr: "localhost:http", data: defaultData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := profile.Profile{Home: home, Desktop: tc.desktop, WorkspaceDir: "~/projects", ProxyURL: "http://127.0.0.1:7890"}
			got, err := Resolve(tc.overrides, profile, func(key string) string { return tc.env[key] }, func() (string, error) { return home, nil })
			want := Config{Addr: tc.addr, DataDir: tc.data, Home: home, ConfigPath: filepath.Join(home, "config.json"), WorkspaceDir: filepath.Join(home, "projects"), ProxyURL: profile.ProxyURL}
			if err != nil || got != want {
				t.Fatalf("resolved = %+v, %v; want %+v", got, err, want)
			}
		})
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("resolution created a profile, storage or workspace: %v", err)
	}
}

func TestResolveRejectsInvalidAddressBeforeOtherSettings(t *testing.T) {
	for _, addr := range []string{"localhost", "localhost:", "localhost:-1", "localhost:65536", "localhost:not-a-port"} {
		t.Run(addr, func(t *testing.T) {
			_, err := Resolve(Overrides{Addr: addr}, profile.Profile{WorkspaceDir: "~/workspace"}, func(string) string { return "" }, func() (string, error) {
				t.Fatal("invalid address should fail before resolving the workspace")
				return "", nil
			})
			if err == nil || !strings.Contains(err.Error(), "invalid listen address") {
				t.Fatalf("invalid address error = %v", err)
			}
		})
	}
	_, err := Resolve(Overrides{}, profile.Profile{}, func(key string) string {
		if key == "PORT" {
			return "invalid"
		}
		return ""
	}, os.UserHomeDir)
	if err == nil || !strings.Contains(err.Error(), "invalid listen address") {
		t.Fatalf("invalid PORT error = %v", err)
	}
}

func TestResolveRetainsWorkspaceErrors(t *testing.T) {
	missingHome := errors.New("missing user home")
	_, err := Resolve(Overrides{}, profile.Profile{WorkspaceDir: "~/workspace"}, func(string) string { return "" }, func() (string, error) { return "", missingHome })
	if !errors.Is(err, missingHome) {
		t.Fatalf("workspace home error = %v", err)
	}
}

func TestInitializeDefaultsWithoutCreatingWorkspace(t *testing.T) {
	home := t.TempDir()
	settings, err := Initialize(Overrides{Addr: "127.0.0.1:0", DataDir: "./existing-data"}, func(string) string { return "" }, func() (string, error) { return home, nil })
	if err != nil {
		t.Fatal(err)
	}
	if settings.Home != filepath.Join(home, ".axv") || settings.ConfigPath != filepath.Join(home, ".axv", "config.json") || settings.WorkspaceDir != filepath.Join(home, ".axv", "workspace") || settings.Addr != "127.0.0.1:0" || settings.DataDir != "./existing-data" || settings.TmuxSocket != "" {
		t.Fatalf("unexpected settings: %+v", settings)
	}
	if _, err := os.Stat(settings.WorkspaceDir); !os.IsNotExist(err) {
		t.Fatalf("startup created workspace: %v", err)
	}
	for path, want := range map[string]os.FileMode{settings.Home: 0700, settings.ConfigPath: 0600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != want {
			t.Errorf("permissions for %s = %o, want %o", path, info.Mode().Perm(), want)
		}
	}
}

func TestInitializeValidatesBeforePublishingAProfile(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides Overrides
		env       map[string]string
		userHome  func() (string, error)
	}{
		{name: "invalid explicit address", overrides: Overrides{Addr: "invalid"}},
		{name: "invalid environment address", env: map[string]string{"ADDR": "invalid"}},
		{name: "invalid environment port", env: map[string]string{"PORT": "invalid"}},
		{name: "unresolvable workspace", env: map[string]string{"AXV_WORKSPACE_DIR": "~/workspace"}, userHome: func() (string, error) { return "", errors.New("missing user home") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "uninitialized")
			userHome := tc.userHome
			if userHome == nil {
				userHome = os.UserHomeDir
			}
			_, err := Initialize(tc.overrides, func(key string) string {
				if key == "AXV_HOME" {
					return home
				}
				return tc.env[key]
			}, userHome)
			if err == nil {
				t.Fatal("invalid desktop settings succeeded")
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("invalid settings created the profile: %v", err)
			}
		})
	}
}

func TestInitializeResolvesTheProfilePublishedByAnotherInitializer(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	original := `{"desktop":{"addr":"127.0.0.1:4100"},"future":{"preserve":true}}`
	published := false
	settings, err := Initialize(Overrides{}, func(key string) string {
		if key == "AXV_HOME" {
			return home
		}
		if key == "ADDR" && !published {
			// The initial profile read has completed. Publish another launcher's
			// complete profile before this initializer attempts publication.
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			published = true
		}
		return ""
	}, os.UserHomeDir)
	if err != nil || settings.Addr != "127.0.0.1:4100" {
		t.Fatalf("concurrent initializer's settings were discarded: %+v, %v", settings, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != original {
		t.Fatalf("concurrent profile replaced: %q, %v", contents, err)
	}
}

func TestInitializeKeepsComputeErrorsIndependentOfDesktopSettings(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	original := `{"desktop":{"addr":"127.0.0.1:4100"},"compute":{"mode":"external","socket":"relative.sock"},"future":{"preserve":true}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := Initialize(Overrides{}, func(key string) string {
		if key == "AXV_HOME" {
			return home
		}
		return ""
	}, os.UserHomeDir)
	if err != nil || settings.Addr != "127.0.0.1:4100" {
		t.Fatalf("compute-only error prevented desktop resolution: %+v, %v", settings, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != original {
		t.Fatalf("existing compute or unknown profile fields changed: %q, %v", contents, err)
	}
}

func TestExistingConfigAndEnvironmentPrecedence(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	original := []byte("{\n  \"workspace_dir\": \"~/custom workspace\",\n  \"proxy_url\": \"http://127.0.0.1:7890\"\n}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"AXV_HOME": home, "AXV_TMUX_SOCKET": "isolated-test"}
	getenv := func(key string) string { return env[key] }
	userHome := func() (string, error) { return home, nil }
	got, err := Initialize(Overrides{}, getenv, userHome)
	if err != nil || got.WorkspaceDir != filepath.Join(home, "custom workspace") || got.ProxyURL != "http://127.0.0.1:7890" || got.TmuxSocket != "isolated-test" {
		t.Fatalf("stored config: %+v, %v", got, err)
	}
	env["AXV_WORKSPACE_DIR"] = filepath.Join(home, "override")
	got, err = Initialize(Overrides{}, getenv, userHome)
	if err != nil || got.WorkspaceDir != env["AXV_WORKSPACE_DIR"] {
		t.Fatalf("workspace override: %+v, %v", got, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatalf("existing configuration changed: %q, %v", after, err)
	}
}

func TestInvalidConfigIsNotReplaced(t *testing.T) {
	for _, content := range []string{"", "null", "[]", "{broken", "{} {}", `{"workspace_dir":true}`, `{"desktop":{"data_dir":true}}`, `{"desktop":{"addr":"invalid"}}`, `{"proxy_url":"invalid-secret-value"}`} {
		t.Run(content, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "config.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Initialize(Overrides{}, func(key string) string {
				if key == "AXV_HOME" {
					return home
				}
				return ""
			}, os.UserHomeDir)
			if err == nil {
				t.Fatal("invalid configuration succeeded")
			}
			if strings.Contains(err.Error(), "invalid-secret-value") {
				t.Fatal("proxy validation exposed its value")
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || string(after) != content {
				t.Fatalf("invalid configuration replaced: %q, %v", after, readErr)
			}
		})
	}
}

func TestConcurrentInitializationPublishesCompleteConfig(t *testing.T) {
	home := t.TempDir()
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			settings, err := Initialize(Overrides{}, func(key string) string {
				if key == "AXV_HOME" {
					return home
				}
				return ""
			}, os.UserHomeDir)
			if err != nil || settings.WorkspaceDir != filepath.Join(home, "workspace") {
				t.Errorf("concurrent initialize: %+v, %v", settings, err)
			}
		})
	}
	group.Wait()
	files, err := os.ReadDir(home)
	if err != nil || len(files) != 1 || files[0].Name() != "config.json" {
		t.Fatalf("unexpected initialization files: %+v, %v", files, err)
	}
}
