package control

import (
	"maps"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
)

// Options supplies the controller's owned store, fixed configuration, and drivers.
// The host keeps the store open until Run and all request handlers have stopped.
type Options struct {
	Logger  *diagnostics.Logger
	Config  compute.Config
	Store   *Store
	Drivers map[string]compute.Driver
	OnReady func()
}

// NewService captures the provider registry before serving requests. Drivers own
// their runtime state; later changes to the supplied map do not retarget commands.
func NewService(options Options) *Service {
	return &Service{Logger: options.Logger, config: options.Config, store: options.Store, drivers: maps.Clone(options.Drivers), onReady: options.OnReady}
}
