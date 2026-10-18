package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func GuestTransport(m Machine, d Driver) *http.Transport {
	return &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return d.DialGuest(ctx, m) }, DisableKeepAlives: true, ResponseHeaderTimeout: 15 * time.Second}
}

func Health(ctx context.Context, m Machine, d Driver) (guestproto.Health, error) {
	var health guestproto.Health
	token, err := d.GuestToken(m)
	if err != nil {
		return health, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	transport := GuestTransport(m, d)
	defer transport.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://guest/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return health, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return health, fmt.Errorf("guest returned status %d", res.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&health); err != nil {
		return health, err
	}
	if health.Version != guestproto.Version {
		return health, Problem("guest_incompatible", "Guest protocol %d is incompatible; this host requires protocol %d. Use a compatible image for guest enhancements.", health.Version, guestproto.Version)
	}
	if health.MachineID != m.ID {
		return health, Problem("guest_identity_mismatch", "Guest identity does not match the selected VM; verify this machine's image and initialization.")
	}
	return health, nil
}
