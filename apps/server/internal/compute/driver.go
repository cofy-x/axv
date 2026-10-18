package compute

import (
	"context"
	"net"
)

type Configurer interface {
	Configure(context.Context, Machine, Spec, Volume) error
}

type ForceStopper interface {
	ForceStop(context.Context, Machine) error
}

type VolumeDriver interface {
	EnsureVolume(context.Context, Volume) error
	ResizeVolume(context.Context, Volume, int64) error
	AttachVolume(context.Context, Machine, Volume) error
	CopyVolume(context.Context, Volume, Volume) error
}

// Driver methods must be idempotent and inspect ownership before any mutation.
// An observation error means unknown, never stopped. Delete must retain volumes.
type Driver interface {
	Probe(context.Context) Capability
	Ensure(context.Context, Machine, Image, Volume) error
	Start(context.Context, Machine, Image, Volume) error
	Stop(context.Context, Machine) error
	Delete(context.Context, Machine) error
	DeleteVolume(context.Context, Volume) error
	Inspect(context.Context, Machine) (Observation, error)
	DialGuest(context.Context, Machine) (net.Conn, error)
	GuestToken(Machine) (string, error)
}

type GuestPreparer interface {
	PrepareGuest(context.Context, Machine, Image, Volume) error
}

// PackageSourceConfigurer runs only for an explicit maintenance operation.
type PackageSourceConfigurer interface {
	ConfigurePackageSource(context.Context, Machine, string) error
}
