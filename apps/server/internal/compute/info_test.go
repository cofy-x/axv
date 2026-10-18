package compute

import "testing"

func TestCreationDefaultsKeepResourcesIndependentOfImageAndCapacity(t *testing.T) {
	images := []ImageInfo{
		{ID: "wrong-arch", Provider: "lima", Arch: "amd64", Status: "configured"},
		{ID: "unavailable-provider", Provider: "firecracker", Arch: "arm64", Status: "ready"},
		{ID: "retired", Provider: "lima", Arch: "arm64", Status: "deleting"},
		{ID: "large-image", Provider: "lima", Arch: "arm64", Status: "ready", MinRootGiB: 20},
	}
	got := DefaultCreationSpec(images, []Capability{{Provider: "lima", Available: true}}, "arm64")
	if got.Spec.Name != "my-vm" || got.Spec.ImageID != "large-image" || got.Spec.Provider != "lima" || got.Spec.Resources != (Resources{CPUs: 2, MemoryMiB: 1024, DiskGiB: 8}) || got.Spec.ProjectGiB != 4 || !got.Spec.RetainVolume || got.Spec.Kind != "development" || !got.Start || got.TaskTTLSeconds != 3600 {
		t.Fatalf("defaults drifted or adopted an image's resource requirement: %+v", got)
	}
	empty := DefaultCreationSpec(images, nil, "arm64")
	if empty.Spec.ImageID != "" || empty.Spec.Provider != "" || empty.Spec.Resources != got.Spec.Resources {
		t.Fatalf("defaults selected an unavailable provider: %+v", empty)
	}
}
