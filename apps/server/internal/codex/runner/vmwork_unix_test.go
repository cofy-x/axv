//go:build darwin || linux

package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

func TestVMWorkFailureCannotPreventRunnerExitProof(t *testing.T) {
	for _, damaged := range []string{"context", "skill"} {
		t.Run(damaged, func(t *testing.T) {
			directory, manifestPath := exitManifest(t)
			manifest, err := readManifest(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			_, _, work := vmWorkBridge(t)
			manifest.VMContextPath, manifest.VMSkillPath, err = vmwork.Publish(directory, work)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(manifest)
			if err := os.WriteFile(manifestPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			path := manifest.VMContextPath
			if damaged == "skill" {
				path = manifest.VMSkillPath
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := Run(manifestPath); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("damaged VM work context started a runner: %v", err)
			}
			proof, err := ReadExit(directory, manifest.RunID)
			if err != nil || proof == nil || proof.ExitCode != 1 || proof.RunID != manifest.RunID {
				t.Fatalf("damaged context prevented cleanup proof: %+v %v", proof, err)
			}
			if state := stateAt(t, filepath.Join(directory, "state.json")); state.Status != StatusFailed || state.ErrorCode != "codex_vm_context_failed" {
				t.Fatalf("VM context startup failure was hidden: %+v", state)
			}
		})
	}
}
