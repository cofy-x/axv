//go:build linux || darwin

package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
)

func TestDaemonLogsCanonicalAndRoleFiles(t *testing.T) {
	home := t.TempDir()
	logger, err := logs.Open(home, "desktop", nil)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("canonical.event")
	hostLog := filepath.Join(home, "logs", "host.log")
	if err := os.WriteFile(hostLog, []byte("host supervisor output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Logs(context.Background(), home, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "canonical.event") || !strings.Contains(out.String(), "host supervisor output") {
		t.Fatal("canonical or role log output missing")
	}
	if err := os.Remove(logger.Path()); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Logs(context.Background(), home, &out); err != nil || !strings.Contains(out.String(), "host supervisor output") {
		t.Fatalf("role-only log read: %s %v", out.String(), err)
	}
}
