package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
)

const DefaultPublishListen = "127.0.0.1:3003"

type Account struct {
	APIToken  string `json:"api_token,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	ZoneID    string `json:"zone_id,omitempty"`
	TunnelID  string `json:"tunnel_id,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Listen    string `json:"listen,omitempty"`
	Audience  string `json:"audience,omitempty"`
}

func (c *Account) UnmarshalJSON(data []byte) error {
	type plain Account
	var value plain
	if len(data) > 1<<20 || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' || json.Unmarshal(data, &value) != nil {
		return errors.New("Cloudflare account settings must be a JSON object")
	}
	*c = Account(value)
	return nil
}

func (c Account) Configured() bool {
	return c.APIToken != "" && c.AccountID != "" && c.ZoneID != "" && c.TunnelID != "" && c.Domain != "" && ValidateAccount(c) == nil
}

func ValidateAccount(c Account) error {
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
