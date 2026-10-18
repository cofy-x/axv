package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	defaults := Profile{RuntimeVersion: 1, Desktop: Desktop{Addr: "127.0.0.1:3001"}, Compute: Compute{Mode: "disabled"}}
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
