package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

var ErrCloudflareRevision = errors.New("Cloudflare configuration changed; reload it before saving")

// ReadCloudflareConfiguration returns a revision of only the Cloudflare object.
// Unrelated profile writers therefore do not invalidate an explicit edit.
func ReadCloudflareConfiguration(home string) (policy.Configuration, string, error) {
	values, err := readCloudflareValues(home)
	if err != nil {
		return policy.Configuration{}, "", err
	}
	var settings policy.Configuration
	if raw := values["cloudflare"]; len(raw) > 0 {
		if err := decode(raw, &settings); err != nil {
			return policy.Configuration{}, "", errors.New("Invalid Cloudflare configuration")
		}
	}
	if settings.Listen == "" {
		settings.Listen = policy.DefaultDesktopListen
	}
	if settings.Management.Listen == "" {
		settings.Management.Listen = policy.DefaultPublishListen
	}
	if err := policy.Validate(settings); err != nil {
		return settings, "", err
	}
	return settings, cloudflareRevision(values["cloudflare"]), nil
}

func cloudflareRevision(raw json.RawMessage) string {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil {
		raw, _ = json.Marshal(fields)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func readCloudflareValues(home string) (map[string]json.RawMessage, error) {
	values := map[string]json.RawMessage{}
	file, err := os.Open(filepath.Join(home, "config.json"))
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if err := decode(data, &values); err != nil {
		return nil, errors.New("Invalid AXV configuration")
	}
	var version int
	if raw, ok := values["runtime_version"]; ok {
		if json.Unmarshal(raw, &version) != nil || version < 0 || version > 1 {
			return nil, errors.New("Unsupported AXV configuration version")
		}
	}
	return values, nil
}

// SaveCloudflareConfiguration preserves unowned fields and requires the shared
// profile lock. Callers must also serialize against listener transitions.
func SaveCloudflareConfiguration(home, expectedRevision string, next policy.Configuration) (policy.Configuration, string, error) {
	values, err := readCloudflareValues(home)
	if err != nil {
		return policy.Configuration{}, "", err
	}
	if expectedRevision == "" || cloudflareRevision(values["cloudflare"]) != expectedRevision {
		return policy.Configuration{}, "", ErrCloudflareRevision
	}
	var old policy.Configuration
	fields := map[string]json.RawMessage{}
	if raw := values["cloudflare"]; len(raw) > 0 {
		if decode(raw, &old) != nil || decode(raw, &fields) != nil {
			return policy.Configuration{}, "", errors.New("Invalid Cloudflare configuration")
		}
	}
	if old.Enabled || next.Enabled {
		return policy.Configuration{}, "", errors.New("Disable desktop access before changing Cloudflare settings")
	}
	if err := policy.Validate(next); err != nil {
		return policy.Configuration{}, "", err
	}
	encoded, _ := json.Marshal(next)
	var owned map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &owned)
	for _, key := range []string{"enabled", "listen", "origin", "issuer", "audience", "owner_subject"} {
		delete(fields, key)
		if raw, ok := owned[key]; ok {
			fields[key] = raw
		}
	}
	management := map[string]json.RawMessage{}
	if raw := fields["management"]; len(raw) > 0 {
		if decode(raw, &management) != nil {
			return policy.Configuration{}, "", errors.New("Invalid Cloudflare account configuration")
		}
	}
	var managed map[string]json.RawMessage
	_ = json.Unmarshal(owned["management"], &managed)
	for _, key := range []string{"api_token", "account_id", "zone_id", "tunnel_id", "domain", "listen", "audience"} {
		delete(management, key)
		if raw, ok := managed[key]; ok {
			management[key] = raw
		}
	}
	fields["management"], _ = json.Marshal(management)
	values["cloudflare"], _ = json.Marshal(fields)
	if err := jsonfile.Write(filepath.Join(home, "config.json"), values, 0600); err != nil {
		return policy.Configuration{}, "", fmt.Errorf("save Cloudflare configuration: %w", err)
	}
	return next, cloudflareRevision(values["cloudflare"]), nil
}

// SaveCloudflareAPIToken rotates only the account credential. It deliberately
// preserves active ingress identity and publication resource ownership.
func SaveCloudflareAPIToken(home, expectedRevision, token string) (policy.Configuration, string, error) {
	if token == "" {
		return policy.Configuration{}, "", errors.New("Provide a new Cloudflare API token")
	}
	if err := policy.ValidateAccount(policy.Account{APIToken: token}); err != nil {
		return policy.Configuration{}, "", err
	}
	values, err := readCloudflareValues(home)
	if err != nil {
		return policy.Configuration{}, "", err
	}
	if expectedRevision == "" || cloudflareRevision(values["cloudflare"]) != expectedRevision {
		return policy.Configuration{}, "", ErrCloudflareRevision
	}
	fields := map[string]json.RawMessage{}
	if raw := values["cloudflare"]; len(raw) > 0 {
		if err := decode(raw, &fields); err != nil {
			return policy.Configuration{}, "", err
		}
	}
	management := map[string]json.RawMessage{}
	if raw := fields["management"]; len(raw) > 0 {
		if err := decode(raw, &management); err != nil {
			return policy.Configuration{}, "", err
		}
	}
	management["api_token"], _ = json.Marshal(token)
	fields["management"], _ = json.Marshal(management)
	values["cloudflare"], _ = json.Marshal(fields)
	var settings policy.Configuration
	if err := decode(values["cloudflare"], &settings); err != nil {
		return policy.Configuration{}, "", err
	}
	if err := policy.Validate(settings); err != nil {
		return policy.Configuration{}, "", err
	}
	if err := jsonfile.Write(filepath.Join(home, "config.json"), values, 0600); err != nil {
		return policy.Configuration{}, "", err
	}
	if settings.Listen == "" {
		settings.Listen = policy.DefaultDesktopListen
	}
	if settings.Management.Listen == "" {
		settings.Management.Listen = policy.DefaultPublishListen
	}
	return settings, cloudflareRevision(values["cloudflare"]), nil
}
