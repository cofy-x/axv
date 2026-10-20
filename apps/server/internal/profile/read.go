package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type readMode uint8

const (
	readStrict readMode = iota
	readDesktop
	readCompute
	readRemote
)

func Load() (Profile, error) {
	home, err := Home()
	if err != nil {
		return Profile{}, err
	}
	return Read(home)
}

func LoadDesktop() (Profile, error) {
	home, err := Home()
	if err != nil {
		return Profile{}, err
	}
	return ReadDesktop(home)
}

func Read(home string) (Profile, error) {
	return readProfile(home, readStrict)
}

// ReadDesktop validates the profile while reporting compute-only errors separately.
// It never repairs or replaces malformed configuration.
func ReadDesktop(home string) (Profile, error) {
	return readProfile(home, readDesktop)
}

// ReadCompute reads only the profile version and compute configuration. Invalid
// desktop or agent settings cannot prevent independent VM administration.
// Compute errors remain separate so a launch-time socket override can replace them.
func ReadCompute(home string) (Profile, error) {
	return readProfile(home, readCompute)
}

// ReadRemote resolves mutable remote settings independently of desktop and VM
// configuration, without initializing a profile or creating node state.
func ReadRemote(home string) (Remote, error) {
	p, err := readProfile(home, readRemote)
	if p.Remote.Hostname == "" {
		p.Remote.Hostname = "axv"
	}
	return p.Remote, err
}

func readProfile(home string, mode readMode) (Profile, error) {
	p := Profile{Home: home}
	file, err := os.Open(filepath.Join(home, "config.json"))
	if os.IsNotExist(err) {
		p.Compute.Mode = "disabled"
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return p, err
	}
	var values map[string]json.RawMessage
	if err = decode(b, &values); err != nil {
		return p, fmt.Errorf("AXV configuration: %w", err)
	}
	computeJSON := values["compute"]
	delete(values, "compute")
	if mode == readCompute || mode == readRemote {
		version := values["runtime_version"]
		remote := values["remote"]
		values = map[string]json.RawMessage{}
		if len(version) > 0 {
			values["runtime_version"] = version
		}
		if mode == readRemote && len(remote) > 0 {
			// Reject null rather than silently treating malformed remote settings
			// as a fresh, default-disabled configuration.
			if err := decode(remote, &p.Remote); err != nil {
				return p, fmt.Errorf("AXV remote configuration: %w", err)
			}
		}
	}
	profileJSON, _ := json.Marshal(values)
	if err = decode(profileJSON, &p); err != nil {
		return p, fmt.Errorf("AXV configuration: %w", err)
	}
	if p.RuntimeVersion != 0 && p.RuntimeVersion != 1 {
		return p, fmt.Errorf("unsupported AXV runtime_version %d", p.RuntimeVersion)
	}
	if mode == readRemote {
		return p, ValidateRemote(p.Remote)
	}
	if len(computeJSON) > 0 {
		if err := json.Unmarshal(computeJSON, &p.Compute); err != nil {
			p.ComputeError = fmt.Sprintf("invalid compute configuration: %v", err)
		}
	}
	if p.Compute.Mode == "" {
		p.Compute.Mode = "disabled"
	}
	if p.ComputeError == "" {
		if err := ValidateCompute(p.Compute); err != nil {
			p.ComputeError = err.Error()
		}
	}
	if mode == readStrict && p.ComputeError != "" {
		return p, errors.New(p.ComputeError)
	}
	if mode == readCompute {
		return p, nil
	}
	return p, validateDesktop(p)
}

func decode(b []byte, value any) error {
	if len(b) > 1<<20 {
		return fmt.Errorf("configuration exceeds 1 MiB")
	}
	if len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return fmt.Errorf("expected a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
