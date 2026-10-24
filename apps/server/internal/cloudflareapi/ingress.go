package cloudflareapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// TunnelConfig is a bounded remote configuration snapshot. Config stays out of
// API responses: other applications' ingress rules belong to their owners.
type TunnelConfig struct {
	AccountID   string          `json:"-"`
	TunnelID    string          `json:"-"`
	Version     int64           `json:"version"`
	Source      string          `json:"source,omitempty"`
	Fingerprint string          `json:"fingerprint"`
	Config      json.RawMessage `json:"-"`
}

type IngressOwnership struct {
	AccountID       string `json:"accountId"`
	TunnelID        string `json:"tunnelId"`
	Hostname        string `json:"hostname"`
	Service         string `json:"service"`
	RuleFingerprint string `json:"ruleFingerprint"`
}

type IngressRequest struct {
	AccountID, TunnelID, Hostname, Service, ExpectedFingerprint string
	Existing                                                    *IngressOwnership
}

type ingressRule struct {
	Hostname string `json:"hostname"`
	Path     string `json:"path"`
	Service  string `json:"service"`
}

type parsedConfig struct {
	fields map[string]json.RawMessage
	raw    []json.RawMessage
	rules  []ingressRule
}

func fingerprint(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return "", &Error{Kind: "invalid_response"}
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", &Error{Kind: "invalid_response"}
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func validFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validService(value string) bool {
	if len(value) > 2048 || strings.TrimSpace(value) != value {
		return false
	}
	origin, err := url.Parse(value)
	return err == nil && (origin.Scheme == "http" || origin.Scheme == "https") && origin.Hostname() != "" &&
		origin.User == nil && origin.RawQuery == "" && !origin.ForceQuery && origin.Fragment == "" && origin.Opaque == "" &&
		(origin.Path == "" || origin.Path == "/")
}

func desiredRule(request IngressRequest) (json.RawMessage, error) {
	if !validID(request.AccountID) || !validID(request.TunnelID) || !validHostname(request.Hostname) || !validService(request.Service) ||
		request.ExpectedFingerprint != "" && !validFingerprint(request.ExpectedFingerprint) {
		return nil, invalidInput()
	}
	// Public Host is required by AXV's local/remote identity boundary. This also
	// overrides a tunnel-wide httpHostHeader without changing other routes.
	raw, _ := json.Marshal(map[string]any{"hostname": request.Hostname, "service": request.Service,
		"originRequest": map[string]string{"httpHostHeader": request.Hostname}})
	return raw, nil
}

// PrepareIngress lets a durable manager save its exact intent before a write.
// It is not evidence that an existing provider rule belongs to AXV. The manager
// must first prove absence with CheckHostnameAvailable and retain that snapshot's
// fingerprint. Never derive ownership from an already existing matching rule.
func PrepareIngress(request IngressRequest) (IngressOwnership, error) {
	raw, err := desiredRule(request)
	if err != nil {
		return IngressOwnership{}, err
	}
	hash, _ := fingerprint(raw)
	return IngressOwnership{AccountID: request.AccountID, TunnelID: request.TunnelID, Hostname: request.Hostname,
		Service: request.Service, RuleFingerprint: hash}, nil
}

func parseConfig(raw json.RawMessage) (parsedConfig, error) {
	var parsed parsedConfig
	if len(raw) == 0 || len(raw) > maxResponseBytes || json.Unmarshal(raw, &parsed.fields) != nil || parsed.fields == nil {
		return parsed, &Error{Kind: "invalid_response"}
	}
	if encoded, ok := parsed.fields["ingress"]; ok && string(encoded) != "null" {
		if json.Unmarshal(encoded, &parsed.raw) != nil || len(parsed.raw) > 500 {
			return parsed, &Error{Kind: "invalid_response"}
		}
	}
	if len(parsed.raw) == 0 {
		// A new remote tunnel can have an empty config. The explicit catch-all
		// fails closed for hostnames outside the newly published route.
		parsed.raw = []json.RawMessage{json.RawMessage(`{"service":"http_status:404"}`)}
	}
	parsed.rules = make([]ingressRule, len(parsed.raw))
	for index, rawRule := range parsed.raw {
		if json.Unmarshal(rawRule, &parsed.rules[index]) != nil || parsed.rules[index].Service == "" {
			return parsed, &Error{Kind: "invalid_response"}
		}
		rule := parsed.rules[index]
		if index == len(parsed.raw)-1 {
			if rule.Hostname != "" || rule.Path != "" {
				return parsed, conflict("ingress", "the tunnel has no final catch-all rule")
			}
		} else if rule.Hostname == "" {
			return parsed, conflict("ingress", "an earlier rule can intercept this hostname")
		} else if !validHostname(strings.TrimPrefix(rule.Hostname, "*.")) {
			return parsed, conflict("ingress", "an existing hostname rule cannot be safely interpreted")
		}
	}
	return parsed, nil
}

func hostnameMatches(pattern, hostname string) bool {
	return pattern == hostname || strings.HasPrefix(pattern, "*.") && strings.HasSuffix(hostname, pattern[1:])
}

// HasHostname is conservative: malformed snapshots and wildcard coverage count
// as occupied. Use CheckHostnameAvailable to receive a reviewable error.
func (snapshot TunnelConfig) HasHostname(hostname string) bool {
	return snapshot.CheckHostnameAvailable(hostname) != nil
}

func (snapshot TunnelConfig) CheckHostnameAvailable(hostname string) error {
	if !validHostname(hostname) {
		return invalidInput()
	}
	parsed, err := parseConfig(snapshot.Config)
	if err != nil {
		return err
	}
	for _, rule := range parsed.rules {
		if hostnameMatches(rule.Hostname, hostname) {
			return conflict("ingress", "an existing rule already covers this hostname")
		}
	}
	return nil
}

func (c *Client) GetTunnelConfig(ctx context.Context, accountID, tunnelID string) (TunnelConfig, error) {
	if !validID(accountID) || !validID(tunnelID) {
		return TunnelConfig{}, invalidInput()
	}
	var result struct {
		Config  json.RawMessage `json:"config"`
		Version int64           `json:"version"`
		Source  string          `json:"source"`
	}
	_, err := c.do(ctx, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/configurations", nil, &result)
	if err != nil {
		return TunnelConfig{}, err
	}
	if _, err = parseConfig(result.Config); err != nil {
		return TunnelConfig{}, err
	}
	hash, err := fingerprint(result.Config)
	return TunnelConfig{AccountID: accountID, TunnelID: tunnelID, Config: result.Config, Version: result.Version, Fingerprint: hash, Source: result.Source}, err
}

func ownedIngress(raw json.RawMessage, rule ingressRule, ownership IngressOwnership) bool {
	hash, err := fingerprint(raw)
	return err == nil && rule.Hostname == ownership.Hostname && rule.Path == "" && rule.Service == ownership.Service && hash == ownership.RuleFingerprint
}

func validIngressOwnership(ownership IngressOwnership) bool {
	return validID(ownership.AccountID) && validID(ownership.TunnelID) && validHostname(ownership.Hostname) &&
		validService(ownership.Service) && validFingerprint(ownership.RuleFingerprint)
}

// EnsureIngress preserves unrelated rules and opaque config fields. Cloudflare
// does not offer a conditional configuration update: re-reading/fingerprinting
// detects observed concurrent changes, but cannot prevent a writer racing the
// final PUT. The caller must serialize AXV writes and expose that limitation.
func (c *Client) EnsureIngress(ctx context.Context, request IngressRequest) (IngressOwnership, error) {
	desired, err := desiredRule(request)
	if err != nil {
		return IngressOwnership{}, err
	}
	want, _ := PrepareIngress(request)
	if old := request.Existing; old != nil && (!validIngressOwnership(*old) || old.AccountID != want.AccountID || old.TunnelID != want.TunnelID || old.Hostname != want.Hostname) {
		return IngressOwnership{}, invalidInput()
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	tunnel, err := c.GetTunnel(ctx, request.AccountID, request.TunnelID)
	if err != nil {
		return IngressOwnership{}, err
	}
	if !tunnel.RemoteConfig {
		return IngressOwnership{}, conflict("ingress", "this tunnel is locally managed")
	}
	snapshot, err := c.GetTunnelConfig(ctx, request.AccountID, request.TunnelID)
	if err != nil {
		return IngressOwnership{}, err
	}
	if request.ExpectedFingerprint != "" && request.ExpectedFingerprint != snapshot.Fingerprint {
		return IngressOwnership{}, conflict("ingress", "the configuration changed after publication was prepared")
	}
	parsed, _ := parseConfig(snapshot.Config)
	match := -1
	for index, rule := range parsed.rules {
		if !hostnameMatches(rule.Hostname, request.Hostname) {
			continue
		}
		if match >= 0 || request.Existing == nil || !ownedIngress(parsed.raw[index], rule, *request.Existing) && !ownedIngress(parsed.raw[index], rule, want) {
			return IngressOwnership{}, conflict("ingress", "an existing rule covers this hostname without matching ownership")
		}
		match = index
	}
	if match >= 0 {
		if ownedIngress(parsed.raw[match], parsed.rules[match], want) {
			return want, nil
		}
		parsed.raw[match] = desired
	} else {
		end := len(parsed.raw) - 1
		parsed.raw = append(parsed.raw, nil)
		copy(parsed.raw[end+1:], parsed.raw[end:])
		parsed.raw[end] = desired
	}
	if err := c.writeTunnelConfig(ctx, snapshot, parsed); err != nil {
		return IngressOwnership{}, err
	}
	return want, nil
}

// RemoveIngress deletes only the exact owned rule. Other rules and tunnel-wide
// origin settings survive, including unknown provider fields.
func (c *Client) RemoveIngress(ctx context.Context, ownership IngressOwnership) error {
	if !validIngressOwnership(ownership) {
		return invalidInput()
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	snapshot, err := c.GetTunnelConfig(ctx, ownership.AccountID, ownership.TunnelID)
	if err != nil {
		return err
	}
	parsed, _ := parseConfig(snapshot.Config)
	match := -1
	for index, rule := range parsed.rules {
		if !hostnameMatches(rule.Hostname, ownership.Hostname) {
			continue
		}
		if match >= 0 || !ownedIngress(parsed.raw[index], rule, ownership) {
			return conflict("ingress", "the recorded rule no longer matches its ownership proof")
		}
		match = index
	}
	if match < 0 {
		return nil
	}
	parsed.raw = append(parsed.raw[:match], parsed.raw[match+1:]...)
	return c.writeTunnelConfig(ctx, snapshot, parsed)
}

func (c *Client) writeTunnelConfig(ctx context.Context, before TunnelConfig, parsed parsedConfig) error {
	if before.Source != "" && before.Source != "cloudflare" {
		return conflict("ingress", "this configuration is not remotely managed")
	}
	latest, err := c.GetTunnelConfig(ctx, before.AccountID, before.TunnelID)
	if err != nil {
		return err
	}
	if latest.Fingerprint != before.Fingerprint || latest.Source != before.Source {
		return conflict("ingress", "the tunnel changed while the update was being prepared")
	}
	encodedRules, _ := json.Marshal(parsed.raw)
	parsed.fields["ingress"] = encodedRules
	desired, _ := json.Marshal(parsed.fields)
	wantHash, _ := fingerprint(desired)
	_, err = c.do(ctx, http.MethodPut, "/accounts/"+before.AccountID+"/cfd_tunnel/"+before.TunnelID+"/configurations",
		map[string]json.RawMessage{"config": desired}, nil)
	if err != nil {
		return err
	}
	actual, err := c.GetTunnelConfig(ctx, before.AccountID, before.TunnelID)
	if err != nil {
		return uncertain(err)
	}
	if actual.Fingerprint != wantHash {
		return uncertain(conflict("ingress", "the tunnel changed while the update was being verified"))
	}
	return nil
}
