package server

import (
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/publish"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// A manually edited profile does not change a running desktop's origin. New
// publications must remain isolated from both recorded and actual identities.
func cloudflarePublicationSettings(home string, desktop *cloudflareController) (publish.Settings, error) {
	c, _, err := profile.ReadCloudflareConfiguration(home)
	if err == nil && desktop != nil {
		err = policy.ValidateIsolation(desktop.BoundOrigin(), c.Management.Domain)
	}
	return publish.Settings{
		APIToken: c.Management.APIToken, AccountID: c.Management.AccountID,
		ZoneID: c.Management.ZoneID, TunnelID: c.Management.TunnelID,
		Domain: c.Management.Domain, Listen: c.Management.Listen,
		Audience: c.Management.Audience, Issuer: c.Issuer,
		OwnerSubject: c.OwnerSubject, DesktopOrigin: c.Origin,
	}, err
}
