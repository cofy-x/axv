package control

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

type apiProbeDriver struct {
	compute.Driver
	provider string
	probed   *[]string
	t        *testing.T
}

func (d apiProbeDriver) Probe(ctx context.Context) compute.Capability {
	d.t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 5*time.Second {
		d.t.Fatal("provider probe has no bounded deadline")
	}
	*d.probed = append(*d.probed, d.provider)
	return compute.Capability{Provider: d.provider, Available: true}
}

func TestRuntimeAvoidsProbesAndInfoUsesOnlyAvailableCatalogImages(t *testing.T) {
	store, config, _ := configurationFixture(t)
	ctx := context.Background()
	for _, status := range []string{"ready", "queued", "failed", "deleted"} {
		record := compute.ImageRecord{Image: compute.Image{ID: status, Name: status, Provider: "lima", Arch: runtime.GOARCH, Rootfs: "/private/image"}, Status: status}
		if err := store.transaction(ctx, func(tx *sql.Tx) error { return writeCatalog(ctx, tx, status, "image", record, 0) }); err != nil {
			t.Fatal(err)
		}
	}
	var probed []string
	service := &Service{store: store, config: config, drivers: map[string]compute.Driver{
		"lima":        apiProbeDriver{provider: "lima", probed: &probed, t: t},
		"firecracker": apiProbeDriver{provider: "firecracker", probed: &probed, t: t},
	}}
	status := service.Runtime()
	if status.Ready || status.HostID != store.HostID || status.ProtocolVersion != compute.ProtocolVersion || status.ConfigFingerprint != config.Fingerprint() || len(probed) != 0 {
		t.Fatalf("runtime changed identity or probed providers: %+v, %v", status, probed)
	}
	service.ready.Store(true)
	if !service.Runtime().Ready {
		t.Fatal("runtime lost controller recovery state")
	}
	info, err := service.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(probed, []string{"firecracker", "lima"}) {
		t.Fatalf("provider probe order changed: %v", probed)
	}
	if len(info.Images) != 2 || info.Images[0].ID != "ready" || info.Images[1].ID != config.Images[0].ID || info.Defaults.Spec.ImageID != "ready" {
		t.Fatalf("unavailable images entered creation defaults: %+v", info)
	}
	images, err := service.Images(ctx)
	if err != nil || len(images) != 4 {
		t.Fatalf("image listing discarded non-deleted work: %+v, %v", images, err)
	}
	for _, image := range images {
		if image.Status == "deleted" {
			t.Fatal("deleted image remained in public catalog")
		}
	}
}

func TestVolumeCommandsPreserveCapabilityAndLookupPrecedence(t *testing.T) {
	store, config, machine := configurationFixture(t)
	service := &Service{store: store, config: config}
	ctx := context.Background()
	_, err := service.CreateVolume(ctx, compute.CreateVolumeRequest{Provider: "lima"})
	if compute.ErrorCode(err) != "unsupported" {
		t.Fatalf("unavailable provider did not precede request validation: %v", err)
	}
	_, err = service.CreateVolume(ctx, compute.CreateVolumeRequest{RequestID: "copy-missing-volume", Name: "Copy", SourceVolumeID: "missing"})
	if compute.ErrorCode(err) != "not_found" {
		t.Fatalf("source-copy request was rejected before source resolution: %v", err)
	}
	_, err = service.VolumeAction(ctx, "missing", compute.VolumeRequest{})
	if compute.ErrorCode(err) != "not_found" {
		t.Fatalf("volume lookup no longer precedes capability and request checks: %v", err)
	}
	_, err = service.VolumeAction(ctx, machine.Spec.VolumeID, compute.VolumeRequest{})
	if compute.ErrorCode(err) != "unsupported" {
		t.Fatalf("volume command bypassed the provider capability gate: %v", err)
	}
	operations, err := service.Operations(ctx)
	if err != nil || len(operations) != 1 {
		t.Fatalf("rejected volume command left durable work: %+v, %v", operations, err)
	}
}

func TestGuestAccessValidatesBindingAndLifecycleBeforeGuestIO(t *testing.T) {
	store, config, machine := configurationFixture(t)
	service := &Service{store: store, config: config, drivers: map[string]compute.Driver{"lima": apiProbeDriver{}}}
	ctx := context.Background()
	for _, hostID := range []string{"", "replacement-host"} {
		if _, err := service.BoundGuestAccess(ctx, "missing", "ssh", hostID, true); compute.ErrorCode(err) != "binding_changed" {
			t.Fatalf("host binding did not precede machine lookup: %v", err)
		}
	}
	if _, err := service.BoundGuestAccess(ctx, "missing", "network", "", false); compute.ErrorCode(err) != "not_found" {
		t.Fatalf("network discovery incorrectly requires a host header: %v", err)
	}
	if _, err := service.BoundGuestAccess(ctx, machine.ID, "ssh", store.HostID, true); compute.ErrorCode(err) != "unavailable" {
		t.Fatalf("stopped VM passed the bound guest gate: %v", err)
	}
	if _, err := service.GuestAccess(ctx, machine.ID, "exec"); compute.ErrorCode(err) != "unavailable" {
		t.Fatalf("stopped VM passed the guest gate: %v", err)
	}
}

func TestUploadCleanupPreservesAcceptedOwnerAndRemovesReplayedInput(t *testing.T) {
	store, config, _ := configurationFixture(t)
	config.DataDir = t.TempDir()
	service := &Service{store: store, config: config, drivers: map[string]compute.Driver{"lima": apiProbeDriver{}}}
	ctx := context.Background()
	request := compute.ImageRequest{RequestID: "accepted-upload", Name: "Uploaded image", Arch: runtime.GOARCH, SHA256: strings.Repeat("a", 64)}
	var paths []string
	var acceptedID string
	for index := range 2 {
		file, err := service.NewUpload()
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, file.Name())
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		result, err := service.CreateImage(ctx, request, file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			acceptedID = result.Operation.ID
		} else if result.Operation.ID != acceptedID {
			t.Fatal("replayed upload created different durable work")
		}
		if err = service.RemoveUnusedUpload(ctx, file.Name()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("cleanup removed the upload owned by the accepted operation: %v", err)
	}
	if _, err := os.Stat(paths[1]); !os.IsNotExist(err) {
		t.Fatalf("replayed input was not cleaned up: %v", err)
	}
}
