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

func TestDaemonLogsCanonicalAndLegacyFallback(t *testing.T) {
	home := t.TempDir()
	logger, err := logs.Open(home, "desktop", nil)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("canonical.event")
	legacy := filepath.Join(home, "logs", "host.log")
	if err := os.WriteFile(legacy, []byte("older host output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Logs(context.Background(), home, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "canonical.event") || !strings.Contains(out.String(), "older host output") {
		t.Fatal("canonical or legacy output missing")
	}
	if err := os.Remove(logger.Path()); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Logs(context.Background(), home, &out); err != nil || !strings.Contains(out.String(), "older host output") {
		t.Fatalf("legacy-only fallback: %s %v", out.String(), err)
	}
}
