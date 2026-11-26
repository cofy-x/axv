package prepare

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestLimaShellResolvesManagedRuntimeAndQuotesPaths(t *testing.T) {
	e, home := testEngine(t, nil)
	dataDir := filepath.Join(home, "compute")
	limaHome := filepath.Join(home, "lima state")
	binary := filepath.Join(home, "tools", "lima 2.2.1", "bin", "limactl")
	hostConfig := filepath.Join(home, "host.json")

	cfg := compute.Config{
		DataDir:  dataDir,
		Socket:   filepath.Join(home, "host", "control.sock"),
		Capacity: compute.Capacity{Resources: compute.Resources{CPUs: 4, MemoryMiB: 4096, DiskGiB: 64}, Machines: 4, Workers: 2},
		Lima:     &compute.LimaConfig{Binary: binary, Version: compute.SupportedLimaVersion, Home: limaHome},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostConfig, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := profile.SaveCompute(home, profile.Compute{Mode: "managed", HostConfig: hostConfig}); err != nil {
		t.Fatal(err)
	}

	machineID := "vm-0123456789abcdef0123456789abcdef"
	machineDir := filepath.Join(dataDir, "lima-runtime", machineID)
	if err := os.MkdirAll(machineDir, 0700); err != nil {
		t.Fatal(err)
	}
	stateRaw, _ := json.Marshal(limaRuntimeState{MachineID: machineID, RuntimeID: "axv-instance-99"})
	if err := os.WriteFile(filepath.Join(machineDir, "state.json"), stateRaw, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := e.LimaShell(context.Background(), machineID)
	if err != nil {
		t.Fatalf("LimaShell failed: %v", err)
	}
	if got.Binary != binary || got.LimaHome != limaHome || got.Instance != "axv-instance-99" {
		t.Fatalf("unexpected LimaShell result: %+v", got)
	}
	wantCmd := "LIMA_HOME='" + limaHome + "' '" + binary + "' shell axv-instance-99"
	if got.Command != wantCmd {
		t.Fatalf("command = %q, want %q", got.Command, wantCmd)
	}

	// Missing machine returns KindNotFound.
	_, err = e.LimaShell(context.Background(), "vm-missing")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindNotFound {
		t.Fatalf("missing machine error = %v, want KindNotFound", err)
	}

	// Path traversal returns KindInvalidInput.
	_, err = e.LimaShell(context.Background(), "../escape")
	if !errors.As(err, &pe) || pe.Kind != KindInvalidInput {
		t.Fatalf("invalid id error = %v, want KindInvalidInput", err)
	}
}
