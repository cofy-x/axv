package compute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadConfigReadsSelectedRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.json")
	want := Config{
		DataDir: dir, Socket: filepath.Join(filepath.VolumeName(dir)+string(filepath.Separator), "host.sock"),
		Capacity: Capacity{Resources: Resources{CPUs: 2, MemoryMiB: 512, DiskGiB: 8}, Machines: 3, Workers: 1},
		Lima:     &LimaConfig{},
	}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadConfig(path); err != nil || got.Fingerprint() != want.Fingerprint() {
		t.Fatalf("load selected configuration: %v, %v", got, err)
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		link := filepath.Join(t.TempDir(), "host.json")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		if got, err := LoadConfig(link); err != nil || got.Fingerprint() != want.Fingerprint() {
			t.Fatalf("load selected configuration through a symlink: %v, %v", got, err)
		}
	}
	if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("load directory: %v", err)
	}
	if err := os.WriteFile(path, append(body, make([]byte, maxHostConfigBytes)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "at most 1 MiB") {
		t.Fatalf("load oversized file: %v", err)
	}
}

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
