package profile

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

// Initialize publishes defaults only when absent and never creates a workspace.
func Initialize(home string) (Profile, error) {
	return initialize(home, readStrict)
}

// InitializeDesktop preserves compute configuration errors for the Web setup UI.
func InitializeDesktop(home string) (Profile, error) {
	return initialize(home, readDesktop)
}

func initialize(home string, mode readMode) (Profile, error) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return Profile{}, fmt.Errorf("create AXV home: %w", err)
	}
	defaults := Profile{RuntimeVersion: 1, Desktop: Desktop{Addr: "127.0.0.1:3001"}, Remote: Remote{Hostname: "axv"}, Compute: Compute{Mode: "disabled"}}
	if err := createDefault(filepath.Join(home, "config.json"), defaults); err != nil {
		return Profile{}, fmt.Errorf("initialize AXV configuration: %w", err)
	}
	return readProfile(home, mode)
}

// SaveCompute updates only fields owned by this package, preserving workspace,
// proxy and future agent settings. The caller holds the profile command lock.
func SaveCompute(home string, c Compute) error {
	if _, err := Read(home); err != nil {
		return err
	}
	path := filepath.Join(home, "config.json")
	values := map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	if err == nil {
		if err = decode(b, &values); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	values["runtime_version"] = json.RawMessage("1")
	values["compute"], err = json.Marshal(c)
	if err != nil {
		return err
	}
	return jsonfile.Write(path, values, 0600)
}

// SaveRemoteEnabled changes only the durable remote switch. The caller holds
// Lock, shared with compute preparation and CLI profile writers. Unknown fields
// and the existing enrollment identity settings retain their ownership.
func SaveRemoteEnabled(home string, enabled bool) (Remote, error) {
	remote, err := ReadRemote(home)
	if err != nil {
		return Remote{}, err
	}
	if remote.Enabled == enabled {
		return remote, nil
	}
	path := filepath.Join(home, "config.json")
	values := map[string]json.RawMessage{}
	file, err := os.Open(path)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
		closeErr := file.Close()
		if readErr != nil {
			return Remote{}, readErr
		}
		if closeErr != nil {
			return Remote{}, closeErr
		}
		if err := decode(data, &values); err != nil {
			return Remote{}, err
		}
	} else if !os.IsNotExist(err) {
		return Remote{}, err
	}
	fields := map[string]json.RawMessage{}
	if data, exists := values["remote"]; exists {
		if err := decode(data, &fields); err != nil {
			return Remote{}, err
		}
		// Recheck the exact data we will preserve if an external editor replaced
		// the profile between reads. Participating AXV writers hold the lock.
		remote = Remote{}
		if err := decode(data, &remote); err != nil {
			return Remote{}, err
		}
		if err := ValidateRemote(remote); err != nil {
			return Remote{}, err
		}
	}
	fields["enabled"], _ = json.Marshal(enabled)
	values["remote"], _ = json.Marshal(fields)
	if err := jsonfile.Write(path, values, 0600); err != nil {
		return Remote{}, err
	}
	remote.Enabled = enabled
	if remote.Hostname == "" {
		remote.Hostname = "axv"
	}
	return remote, nil
}

// SaveCloudflareEnabled updates only the ingress switch while preserving unknown
// profile and Cloudflare fields. The caller holds the shared profile Lock.
func SaveCloudflareEnabled(home string, enabled bool) (policy.Configuration, error) {
	path := filepath.Join(home, "config.json")
	values := map[string]json.RawMessage{}
	file, err := os.Open(path)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
		closeErr := file.Close()
		if readErr != nil {
			return policy.Configuration{}, readErr
		}
		if closeErr != nil {
			return policy.Configuration{}, closeErr
		}
		if err := decode(data, &values); err != nil {
			return policy.Configuration{}, err
		}
	} else if !os.IsNotExist(err) {
		return policy.Configuration{}, err
	}
	var version int
	if raw, exists := values["runtime_version"]; exists {
		if err := json.Unmarshal(raw, &version); err != nil {
			return policy.Configuration{}, fmt.Errorf("AXV runtime_version must be a supported integer")
		}
		if version != 0 && version != 1 {
			return policy.Configuration{}, fmt.Errorf("unsupported AXV runtime_version %d", version)
		}
	}
	settings := policy.Configuration{}
	fields := map[string]json.RawMessage{}
	if raw, exists := values["cloudflare"]; exists {
		if err := decode(raw, &fields); err != nil {
			return policy.Configuration{}, err
		}
		if err := decode(raw, &settings); err != nil {
			return policy.Configuration{}, err
		}
	}
	previous := settings.Enabled
	settings.Enabled = enabled
	if err := policy.Validate(settings); err != nil {
		return policy.Configuration{}, err
	}
	if settings.Listen == "" {
		settings.Listen = policy.DefaultDesktopListen
	}
	if previous == enabled {
		return settings, nil
	}
	fields["enabled"], _ = json.Marshal(enabled)
	values["cloudflare"], _ = json.Marshal(fields)
	if err := jsonfile.Write(path, values, 0600); err != nil {
		return policy.Configuration{}, err
	}
	return settings, nil
}

func createDefault(path string, defaults any) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.MarshalIndent(defaults, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Link publishes the complete file without replacing a concurrent initializer
	// or an existing user configuration. The temporary file is always removed.
	if err := os.Link(file.Name(), path); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}
