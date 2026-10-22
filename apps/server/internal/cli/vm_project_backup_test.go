package cli

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestVMProjectBackupCLIPreservesOriginalReviewsAndRequest(t *testing.T) {
	var received []compute.BackupProjectRequest
	cliHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AXV-Host-ID") != "host-cli" || r.URL.Path != "/v1/projects/project-original/backups" {
			t.Errorf("backup changed host/project: %s %s", r.Method, r.URL.Path)
		}
		var req compute.BackupProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		received = append(received, req)
		json.NewEncoder(w).Encode(compute.VolumeResult{Volume: compute.Volume{ID: "backup-original"}, Operation: compute.Operation{ID: "operation-original"}})
	})
	args := []string{"vm", "project", "backup", "create", "project-original", "--host-id", "host-cli", "--request-id", "backup-original-request", "--machine", "machine-original", "--machine-revision", "7", "--project-revision", "2", "--volume-revision", "9", "--name", "Original backup"}
	for range 2 {
		if _, _, err := cliInvoke(args...); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 2 || received[0] != received[1] || received[0].MachineID != "machine-original" || received[0].MachineRevision != 7 || received[0].VolumeRevision != 9 || received[0].ProjectRevision != 2 {
		t.Fatalf("backup replay changed reviewed payload: %+v", received)
	}
	if _, _, err := cliInvoke("vm", "project", "backup", "restore", "backup", "--host-id", "host-cli", "--request-id", "restore-original", "--name", "Restored"); err == nil {
		t.Fatal("restore did not require reviewed backup revision")
	}
}
