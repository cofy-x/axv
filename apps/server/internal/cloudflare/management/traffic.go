package management

import (
	"context"
	"time"

	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func (m *Service) Traffic(request context.Context, hours int) (TrafficResult, error) {
	if hours != 1 && hours != 6 && hours != 12 && hours != 24 && hours != 168 && hours != 720 {
		return TrafficResult{}, problem("invalid_cloudflare_request", "Choose a traffic window of 1, 6, 12, 24, 168 or 720 hours.")
	}
	c, _, err := profile.ReadCloudflareConfiguration(m.home)
	if err != nil {
		return TrafficResult{}, problem("cloudflare_config_failed", "Cloudflare settings are unavailable. Inspect the profile locally.")
	}
	// Use complete hourly buckets. Extending the start of a rolling window
	// would exceed its requested duration and can cross provider plan limits.
	until := time.Now().UTC().Truncate(time.Hour)
	since := until.Add(-time.Duration(hours) * time.Hour)
	if !c.Management.Configured() {
		return TrafficResult{Reason: "Connect a Cloudflare account to read zone traffic.", Since: since, Until: until}, nil
	}
	client, err := m.client(c.Management.APIToken)
	if err != nil {
		return TrafficResult{}, problem("cloudflare_config_failed", "The Cloudflare API token is invalid. Check it locally.")
	}
	defer client.CloseIdleConnections()
	ctx, cancel := m.requestContext(request)
	defer cancel()
	traffic, err := client.Traffic(ctx, cfclient.TrafficRequest{ZoneID: c.Management.ZoneID, Since: since, Until: until})
	if err := m.requestError(request); err != nil {
		return TrafficResult{}, err
	}
	if err != nil {
		return TrafficResult{Reason: err.Error(), Since: since, Until: until}, nil
	}
	return TrafficResult{Supported: true, Since: traffic.Since, Until: traffic.Until, Traffic: &traffic}, nil
}
