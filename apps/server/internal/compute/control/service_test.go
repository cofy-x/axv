package control

import (
	"context"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestServiceRetainsItsProviderRegistry(t *testing.T) {
	store, config, _ := configurationFixture(t)
	var probed []string
	drivers := map[string]compute.Driver{"lima": apiProbeDriver{provider: "original", probed: &probed, t: t}}
	service := NewService(Options{Store: store, Config: config, Drivers: drivers})
	drivers["lima"] = apiProbeDriver{provider: "replacement", probed: &probed, t: t}
	drivers["firecracker"] = apiProbeDriver{provider: "later", probed: &probed, t: t}
	info, err := service.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Providers) != 1 || info.Providers[0].Provider != "original" {
		t.Fatalf("a caller changed the running service's provider routing: %+v", info.Providers)
	}
}
