package control

import (
	"context"
	"runtime"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

// Runtime reports controller recovery and identity without provider I/O.
func (s *Service) Runtime() compute.Runtime {
	return compute.Runtime{Ready: s.Ready(), HostID: s.store.HostID, ProtocolVersion: compute.ProtocolVersion, ConfigFingerprint: s.config.Fingerprint()}
}

func (s *Service) Info(ctx context.Context) (compute.Info, error) {
	records, err := s.store.Images(ctx)
	if err != nil {
		return compute.Info{}, err
	}
	images := []compute.ImageInfo{}
	for _, record := range records {
		if record.Status == "ready" || record.Status == "configured" {
			images = append(images, record.Info())
		}
	}
	capabilities := []compute.Capability{}
	for _, provider := range []string{"firecracker", "lima"} {
		if driver := s.drivers[provider]; driver != nil {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			capabilities = append(capabilities, driver.Probe(probeCtx))
			cancel()
		}
	}
	return compute.Info{ConfigFingerprint: s.config.Fingerprint(), Configured: true, HostID: s.store.HostID, ProtocolVersion: compute.ProtocolVersion, HostArch: runtime.GOARCH, Capacity: s.config.Capacity, Providers: capabilities, Images: images, Defaults: compute.DefaultCreationSpec(images, capabilities, runtime.GOARCH), Experimental: true}, nil
}

func (s *Service) Machines(ctx context.Context) ([]compute.Machine, error) {
	return s.store.Machines(ctx)
}

func (s *Service) Machine(ctx context.Context, id string) (compute.Machine, error) {
	return s.store.Machine(ctx, id)
}

func (s *Service) Action(ctx context.Context, id string, req compute.ActionRequest) (compute.Result, error) {
	return s.store.Action(ctx, s.config.Capacity, id, req)
}

func (s *Service) Operations(ctx context.Context) ([]compute.Operation, error) {
	records, err := s.store.Operations(ctx)
	if err != nil {
		return nil, err
	}
	operations := make([]compute.Operation, len(records))
	for index, record := range records {
		operations[index] = record.Operation
	}
	return operations, nil
}

func (s *Service) Operation(ctx context.Context, id string) (compute.Operation, error) {
	record, err := s.store.Operation(ctx, id)
	return record.Operation, err
}

func (s *Service) OperationByRequestID(ctx context.Context, requestID string) (compute.Operation, error) {
	record, err := s.store.OperationByRequestID(ctx, requestID)
	return record.Operation, err
}

func (s *Service) Volumes(ctx context.Context) ([]compute.Volume, error) {
	return s.store.Volumes(ctx)
}

func (s *Service) CreateVolume(ctx context.Context, req compute.CreateVolumeRequest) (compute.VolumeResult, error) {
	if _, ok := s.drivers[req.Provider].(compute.VolumeDriver); !ok && req.SourceVolumeID == "" {
		return compute.VolumeResult{}, compute.Problem("unsupported", "This provider does not support volume management.")
	}
	return s.store.CreateVolume(ctx, s.config, req)
}

func (s *Service) VolumeAction(ctx context.Context, id string, req compute.VolumeRequest) (compute.VolumeResult, error) {
	volume, err := s.store.Volume(ctx, id)
	if err != nil {
		return compute.VolumeResult{}, err
	}
	if _, ok := s.drivers[volume.Provider].(compute.VolumeDriver); !ok {
		return compute.VolumeResult{}, compute.Problem("unsupported", "This provider does not support volume management.")
	}
	return s.store.VolumeAction(ctx, s.config, volume.ID, req)
}

func (s *Service) DeleteVolume(ctx context.Context, id, requestID string, revision int64) (compute.Operation, error) {
	record, err := s.store.DeleteVolume(ctx, id, requestID, revision)
	return record.Operation, err
}

func (s *Service) Images(ctx context.Context) ([]compute.ImageInfo, error) {
	records, err := s.store.Images(ctx)
	if err != nil {
		return nil, err
	}
	images := []compute.ImageInfo{}
	for _, record := range records {
		if record.Status != "deleted" {
			images = append(images, record.Info())
		}
	}
	return images, nil
}

func (s *Service) DeleteImage(ctx context.Context, id, requestID string, revision int64) (compute.Operation, error) {
	record, err := s.store.DeleteImage(ctx, id, requestID, revision)
	return record.Operation, err
}

// RequireImageImport checks support before the adapter reads a large upload.
func (s *Service) RequireImageImport() error {
	if s.drivers["lima"] == nil {
		return compute.Problem("unsupported", "Image management requires macOS VM hosting.")
	}
	return nil
}

// RemoveUnusedUpload retains files already owned by accepted catalog work.
func (s *Service) RemoveUnusedUpload(ctx context.Context, path string) error {
	inUse, err := s.store.UploadInUse(ctx, path)
	if err != nil || inUse {
		return err
	}
	return s.RemoveUpload(path)
}

// GuestAccess binds a guest transport to the machine and provider verified by
// the controller. The adapter can use this result without looking up a newer VM.
type GuestAccess struct {
	Machine compute.Machine
	Driver  compute.Driver
	HostID  string
}

func (s *Service) GuestAccess(ctx context.Context, id, capability string) (GuestAccess, error) {
	machine, err := s.store.Machine(ctx, id)
	if err != nil {
		return GuestAccess{}, err
	}
	if machine.Status != "running" || machine.Desired != "running" {
		return GuestAccess{}, compute.Problem("unavailable", "The machine is not running or is stopping.")
	}
	driver := s.drivers[machine.Spec.Provider]
	if driver == nil {
		return GuestAccess{}, compute.Problem("unsupported", "Provider is unavailable.")
	}
	if err := s.RequireGuest(ctx, machine, capability); err != nil {
		return GuestAccess{}, err
	}
	return GuestAccess{Machine: machine, Driver: driver, HostID: s.store.HostID}, nil
}

// BoundGuestAccess validates an explicit host binding before resource lookup.
// Network discovery permits an omitted binding; SSH requires the original host.
func (s *Service) BoundGuestAccess(ctx context.Context, id, capability, hostID string, requireHost bool) (GuestAccess, error) {
	if (requireHost && hostID == "") || (hostID != "" && hostID != s.store.HostID) {
		return GuestAccess{}, compute.Problem("binding_changed", "This request requires the original AXV host identity.")
	}
	machine, err := s.store.Machine(ctx, id)
	if err != nil {
		return GuestAccess{}, err
	}
	if machine.HostID != s.store.HostID {
		return GuestAccess{}, compute.Problem("binding_changed", "The machine belongs to a different AXV host.")
	}
	if machine.Status != "running" || machine.Desired != "running" {
		return GuestAccess{}, compute.Problem("unavailable", "The machine is not running or is stopping. Start it explicitly before connecting.")
	}
	driver := s.drivers[machine.Spec.Provider]
	if driver == nil {
		return GuestAccess{}, compute.Problem("unsupported", "The machine provider is unavailable.")
	}
	if err := s.RequireGuest(ctx, machine, capability); err != nil {
		return GuestAccess{}, err
	}
	return GuestAccess{Machine: machine, Driver: driver, HostID: s.store.HostID}, nil
}
