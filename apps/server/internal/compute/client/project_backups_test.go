package client

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestProjectBackupClientRejectsUnicodeNormalizationAndRetainsHost(t *testing.T) {
	var requests atomic.Int32
	socket := socketHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime" {
			runtime(w, "host")
			return
		}
		requests.Add(1)
		if r.Header.Get("X-AXV-Host-ID") != "host" {
			t.Error("backup lost original host")
		}
		json.NewEncoder(w).Encode(map[string]any{"backups": []any{}})
	}))
	c, err := Connect(t.Context(), socket, Binding{HostID: "host"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.BackupProject(t.Context(), "project", compute.BackupProjectRequest{Name: "bad-\xff"}); compute.ErrorCode(err) != "invalid_request" {
		t.Fatal("backup normalized invalid name", err)
	}
	if _, err := c.RestoreProjectVolume(t.Context(), "backup-\xff", compute.RestoreProjectVolumeRequest{}); compute.ErrorCode(err) != "invalid_request" {
		t.Fatal("restore normalized original identity", err)
	}
	if _, err := c.BindRestoredProject(t.Context(), "backup", compute.BindRestoredProjectRequest{SourceProjectID: "project-\xff"}); compute.ErrorCode(err) != "invalid_request" {
		t.Fatal("binding normalized original project", err)
	}
	if requests.Load() != 0 {
		t.Fatal("invalid request reached original host")
	}
	if _, err := c.ProjectBackups(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("backup inventory was not fetched")
	}
}
