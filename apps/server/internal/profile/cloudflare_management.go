package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

const DefaultCloudflareManagementListen = "127.0.0.1:3003"

type CloudflareManagement struct {
	APIToken  string `json:"api_token,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	ZoneID    string `json:"zone_id,omitempty"`
	TunnelID  string `json:"tunnel_id,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Listen    string `json:"listen,omitempty"`
	Audience  string `json:"audience,omitempty"`
}

func (c *CloudflareManagement) UnmarshalJSON(data []byte) error {
	type plain CloudflareManagement
	var value plain
	if err := decode(data, &value); err != nil {
		return errors.New("Cloudflare account settings must be a JSON object")
	}
	*c = CloudflareManagement(value)
	return nil
}

func (c CloudflareManagement) Configured() bool {
	return c.APIToken != "" && c.AccountID != "" && c.ZoneID != "" && c.TunnelID != "" && c.Domain != "" && ValidateCloudflareManagement(c) == nil
}

func ValidateCloudflareManagement(c CloudflareManagement) error {
	if len(c.APIToken) > 4096 || strings.ContainsAny(c.APIToken, " \t\r\n\x00") {
		return errors.New("Cloudflare API token must be a bounded token without whitespace")
	}
	for _, r := range c.APIToken {
		if r < 33 || r > 126 {
			return errors.New("Cloudflare API token must contain printable ASCII characters")
		}
	}
	for _, id := range []string{c.AccountID, c.ZoneID, c.TunnelID} {
		if id != "" && !cloudflareResourceID(id) {
			return errors.New("Cloudflare resource IDs must contain at most 128 ASCII letters, digits, hyphens or underscores")
		}
	}
	if c.Domain != "" {
		if err := ValidateAllocationDomain(c.Domain); err != nil {
			return err
		}
	}
	if c.Audience != "" && !cloudflareToken(c.Audience) {
		return errors.New("Cloudflare publishing audience must be a bounded Access audience")
	}
	if c.Listen != "" {
		host, port, err := net.SplitHostPort(c.Listen)
		ip := net.ParseIP(host)
		n, portErr := strconv.Atoi(port)
		if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return errors.New("Cloudflare publication listener must be a numeric loopback address and port")
		}
	}
	return nil
}

func cloudflareResourceID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_') {
			return false
		}
	}
	return true
}

var ErrCloudflareRevision = errors.New("Cloudflare configuration changed; reload it before saving")

// ReadCloudflareConfiguration returns a revision of only the Cloudflare object.
// Unrelated profile writers therefore do not invalidate an explicit edit.
func ReadCloudflareConfiguration(home string) (Cloudflare, string, error) {
	values, err := readCloudflareValues(home)
	if err != nil {
		return Cloudflare{}, "", err
	}
	var settings Cloudflare
	if raw := values["cloudflare"]; len(raw) > 0 {
		if err := decode(raw, &settings); err != nil {
			return Cloudflare{}, "", errors.New("Invalid Cloudflare configuration")
		}
	}
	if settings.Listen == "" {
		settings.Listen = DefaultCloudflareListen
	}
	if settings.Management.Listen == "" {
		settings.Management.Listen = DefaultCloudflareManagementListen
	}
	if err := ValidateCloudflare(settings); err != nil {
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
func SaveCloudflareConfiguration(home, expectedRevision string, next Cloudflare) (Cloudflare, string, error) {
	values, err := readCloudflareValues(home)
	if err != nil {
		return Cloudflare{}, "", err
	}
	if expectedRevision == "" || cloudflareRevision(values["cloudflare"]) != expectedRevision {
		return Cloudflare{}, "", ErrCloudflareRevision
	}
	var old Cloudflare
	fields := map[string]json.RawMessage{}
	if raw := values["cloudflare"]; len(raw) > 0 {
		if decode(raw, &old) != nil || decode(raw, &fields) != nil {
			return Cloudflare{}, "", errors.New("Invalid Cloudflare configuration")
		}
	}
	if old.Enabled || next.Enabled {
		return Cloudflare{}, "", errors.New("Disable desktop access before changing Cloudflare settings")
	}
	if err := ValidateCloudflare(next); err != nil {
		return Cloudflare{}, "", err
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
			return Cloudflare{}, "", errors.New("Invalid Cloudflare account configuration")
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
		return Cloudflare{}, "", fmt.Errorf("save Cloudflare configuration: %w", err)
	}
	return next, cloudflareRevision(values["cloudflare"]), nil
}

// SaveCloudflareAPIToken rotates only the account credential. It deliberately
// preserves active ingress identity and publication resource ownership.
func SaveCloudflareAPIToken(home, expectedRevision, token string) (Cloudflare, string, error) {
	if token == "" {
		return Cloudflare{}, "", errors.New("Provide a new Cloudflare API token")
	}
	if err := ValidateCloudflareManagement(CloudflareManagement{APIToken: token}); err != nil {
		return Cloudflare{}, "", err
	}
	values, err := readCloudflareValues(home)
	if err != nil {
		return Cloudflare{}, "", err
	}
	if expectedRevision == "" || cloudflareRevision(values["cloudflare"]) != expectedRevision {
		return Cloudflare{}, "", ErrCloudflareRevision
	}
	fields := map[string]json.RawMessage{}
	if raw := values["cloudflare"]; len(raw) > 0 {
		if err := decode(raw, &fields); err != nil {
			return Cloudflare{}, "", err
		}
	}
	management := map[string]json.RawMessage{}
	if raw := fields["management"]; len(raw) > 0 {
		if err := decode(raw, &management); err != nil {
			return Cloudflare{}, "", err
		}
	}
	management["api_token"], _ = json.Marshal(token)
	fields["management"], _ = json.Marshal(management)
	values["cloudflare"], _ = json.Marshal(fields)
	var settings Cloudflare
	if err := decode(values["cloudflare"], &settings); err != nil {
		return Cloudflare{}, "", err
	}
	if err := ValidateCloudflare(settings); err != nil {
		return Cloudflare{}, "", err
	}
	if err := jsonfile.Write(filepath.Join(home, "config.json"), values, 0600); err != nil {
		return Cloudflare{}, "", err
	}
	if settings.Listen == "" {
		settings.Listen = DefaultCloudflareListen
	}
	if settings.Management.Listen == "" {
		settings.Management.Listen = DefaultCloudflareManagementListen
	}
	return settings, cloudflareRevision(values["cloudflare"]), nil
}
