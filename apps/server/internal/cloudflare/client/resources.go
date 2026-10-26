package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Zone struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	Account Account `json:"account"`
}

type TokenInfo struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	ExpiresOn *time.Time `json:"expires_on,omitempty"`
	NotBefore *time.Time `json:"not_before,omitempty"`
}

type Tunnel struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Status       string             `json:"status"`
	RemoteConfig bool               `json:"remote_config"`
	ConfigSource string             `json:"config_src,omitempty"`
	Connections  []TunnelConnection `json:"connections"`
}

// Cloudflare now uses config_src; remote_config remains a deprecated response
// field. Prefer the current field, including when it contradicts legacy data.
func (tunnel *Tunnel) UnmarshalJSON(data []byte) error {
	type wireTunnel Tunnel
	var value wireTunnel
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*tunnel = Tunnel(value)
	if tunnel.ConfigSource != "" {
		tunnel.RemoteConfig = tunnel.ConfigSource == "cloudflare"
	}
	return nil
}

type TunnelConnection struct {
	ID               string `json:"id"`
	ClientID         string `json:"client_id"`
	Location         string `json:"colo_name"`
	PendingReconnect bool   `json:"is_pending_reconnect"`
}

func (c *Client) VerifyToken(ctx context.Context) (TokenInfo, error) {
	var result TokenInfo
	_, err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &result)
	if err != nil {
		return TokenInfo{}, err
	}
	now := time.Now()
	if result.Status != "active" || result.ExpiresOn != nil && !result.ExpiresOn.After(now) || result.NotBefore != nil && result.NotBefore.After(now) {
		return TokenInfo{}, &Error{Kind: "inactive_token"}
	}
	return result, nil
}

func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	return listPages[Account](ctx, c, "/accounts", nil)
}

func (c *Client) ListZones(ctx context.Context, accountID string) ([]Zone, error) {
	query := make(url.Values)
	if accountID != "" {
		if !validID(accountID) {
			return nil, invalidInput()
		}
		query.Set("account.id", accountID)
	}
	return listPages[Zone](ctx, c, "/zones", query)
}

func (c *Client) GetZone(ctx context.Context, zoneID string) (Zone, error) {
	if !validID(zoneID) {
		return Zone{}, invalidInput()
	}
	var result Zone
	_, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID, nil, &result)
	if err == nil && (result.ID != zoneID || !validHostname(result.Name)) {
		err = &Error{Kind: "invalid_response"}
	}
	return result, err
}

// CheckDNSAccess checks read access only. It does not claim DNS Write permission.
func (c *Client) CheckDNSAccess(ctx context.Context, zoneID string) error {
	if !validID(zoneID) {
		return invalidInput()
	}
	var records []DNSRecord
	_, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?per_page=1", nil, &records)
	return err
}

func (c *Client) ListTunnels(ctx context.Context, accountID string) ([]Tunnel, error) {
	if !validID(accountID) {
		return nil, invalidInput()
	}
	return listPages[Tunnel](ctx, c, "/accounts/"+accountID+"/cfd_tunnel", url.Values{"is_deleted": {"false"}})
}

func (c *Client) GetTunnel(ctx context.Context, accountID, tunnelID string) (Tunnel, error) {
	if !validID(accountID) || !validID(tunnelID) {
		return Tunnel{}, invalidInput()
	}
	var result Tunnel
	_, err := c.do(ctx, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID, nil, &result)
	if err == nil && result.ID != tunnelID {
		err = &Error{Kind: "invalid_response"}
	}
	return result, err
}
