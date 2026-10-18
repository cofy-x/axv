//go:build !linux

package firecracker

import (
	"context"
	"fmt"
	"net"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type unavailable struct{}

func New(string, string, compute.FirecrackerConfig) (compute.Driver, error) {
	return unavailable{}, nil
}
func (unavailable) Probe(context.Context) compute.Capability {
	return compute.Capability{Provider: "firecracker", Reason: "Firecracker requires a Linux host with KVM."}
}
func unsupported() error { return fmt.Errorf("Firecracker requires Linux/KVM") }
func (unavailable) Ensure(context.Context, compute.Machine, compute.Image, compute.Volume) error {
	return unsupported()
}
func (unavailable) Start(context.Context, compute.Machine, compute.Image, compute.Volume) error {
	return unsupported()
}
func (unavailable) Stop(context.Context, compute.Machine) error        { return unsupported() }
func (unavailable) Delete(context.Context, compute.Machine) error      { return unsupported() }
func (unavailable) DeleteVolume(context.Context, compute.Volume) error { return unsupported() }
func (unavailable) Inspect(context.Context, compute.Machine) (compute.Observation, error) {
	return compute.Observation{}, unsupported()
}
func (unavailable) DialGuest(context.Context, compute.Machine) (net.Conn, error) {
	return nil, unsupported()
}
func (unavailable) GuestToken(compute.Machine) (string, error) { return "", unsupported() }
