package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestHostFailureUsesStderrWithoutProfile(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "uncreated-profile")
	t.Setenv("AXV_HOME", profile)
	blocked := filepath.Join(home, "blocked-data")
	if err := os.WriteFile(blocked, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}
	configuration := compute.Config{
		DataDir: blocked,
		// Startup fails at DataDir before the socket or provider is touched.
		Socket:   filepath.Join(filepath.VolumeName(home)+string(filepath.Separator), "axv-unused-host.sock"),
		Capacity: compute.Capacity{Resources: compute.Resources{CPUs: 2, MemoryMiB: 1024, DiskGiB: 20}, Machines: 1, Workers: 1},
		Lima:     &compute.LimaConfig{Binary: filepath.Join(home, "limactl"), Version: compute.SupportedLimaVersion},
	}
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "host.json")
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(home, "stderr-")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	originalStderr := os.Stderr
	defer func() { os.Stderr = originalStderr }()
	os.Stderr = stderr
	var stdout bytes.Buffer
	runErr := Run(configPath, &stdout)
	os.Stderr = originalStderr
	var pathError *os.PathError
	if !errors.As(runErr, &pathError) || pathError.Path != blocked {
		t.Fatalf("expected the blocked data directory to prevent startup, got %v", runErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("host diagnostic reached readiness output: %q", stdout.String())
	}
	if _, err := os.Lstat(profile); !os.IsNotExist(err) {
		t.Fatalf("host initialized the ambient profile: %v", err)
	}
	if _, err = stderr.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(stderr)
	for _, name := range []string{"service.start", "service.failed", "service.stop"} {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event["event"] != name || event["role"] != "host" {
			t.Fatalf("unexpected host diagnostic: %v", event)
		}
		if name == "service.failed" && event["error"] == nil {
			t.Fatal("host failure omitted its cause")
		}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("unexpected trailing host diagnostic: %v", err)
	}
}
