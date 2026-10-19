package compute

import (
	"runtime"
	"testing"
)

func TestFirecrackerMachineCPULimitDoesNotLimitHostOrLima(t *testing.T) {
	for _, tc := range []struct {
		provider string
		cpus     int
		valid    bool
	}{
		{provider: "firecracker", cpus: 32, valid: true},
		{provider: "firecracker", cpus: 33},
		{provider: "lima", cpus: 64, valid: true},
	} {
		image := Image{ID: "image-test", Provider: tc.provider, Arch: runtime.GOARCH}
		config := Config{Capacity: Capacity{Resources: Resources{CPUs: 64, MemoryMiB: 4096, DiskGiB: 100}, Machines: 10, Workers: 2}, Images: []Image{image}}
		request := CreateRequest{Spec: Spec{Name: "CPU limit test", Provider: tc.provider, ImageID: image.ID, Kind: "development", Resources: Resources{CPUs: tc.cpus, MemoryMiB: 512, DiskGiB: 2}, ProjectGiB: 1, RetainVolume: true}}
		if err := config.ValidateRequest(request); (err == nil) != tc.valid {
			t.Fatalf("provider=%s cpus=%d: %v", tc.provider, tc.cpus, err)
		}
	}
}
