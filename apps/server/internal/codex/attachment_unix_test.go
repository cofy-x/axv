//go:build darwin || linux

package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
)

func TestAttachmentStartFailureLogsPrivateCauseWithoutEndingRun(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery; no native tmux process is started")
	}
	manager := testManager(t)
	ctx := context.Background()
	run := serviceRunningRun(t, manager, "attachment-start-diagnostic")
	manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
		switch {
		case slices.Contains(args, "list-panes"):
			return run.TmuxSession + "\t0\t0:", nil
		case slices.Contains(args, "#{pane_in_mode}"):
			return "0", nil
		default:
			t.Fatalf("failed attachment controlled another part of the runtime: %q", args)
			return "", nil
		}
	}
	const secret = "private-attachment-test-credential"
	t.Setenv("AXV_TEST_SECRET", secret)
	var output bytes.Buffer
	manager.settings.Logger = diagnostics.New("desktop", &output, io.Discard).With("component", "codex")
	manager.settings.ProxyURL = "https://user:" + secret + "@proxy.invalid"
	lease, err := manager.PrepareAttachment(ctx, run.ID, run.RunID, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lease.Close)
	lease.path = filepath.Join(t.TempDir(), secret, "missing-tmux")
	err = lease.Open(ctx)
	requireServiceError(t, err, Unavailable, "attach_failed")
	if err.Error() != "Could not attach to the persistent Codex session." {
		t.Fatalf("native failure escaped the safe attachment message: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("missing structured attachment diagnostic: %v", err)
	}
	if event["event"] != "codex.attach_failed" || event["component"] != "codex" || event["runId"] != run.RunID || event["executable"] != strings.ReplaceAll(lease.path, secret, "[REDACTED]") || !strings.Contains(event["error"].(string), "start attachment PTY") {
		t.Fatalf("attachment diagnostic lost exact identity or native cause: %v", event)
	}
	if strings.Contains(output.String(), secret) || strings.Contains(output.String(), "proxy.invalid") || strings.Contains(output.String(), run.HistoryHome) {
		t.Fatal("attachment diagnostic exposed credentials or unrelated configuration")
	}
	lease.Close()
	kept, err := manager.store.CodexRun(ctx, run.ID, run.RunID)
	if err != nil || kept.Status != "running" || manager.attachments != 0 || len(manager.connections) != 0 {
		t.Fatalf("failed attachment ended its run or retained capacity: run=%+v attachments=%d connections=%d error=%v", kept, manager.attachments, len(manager.connections), err)
	}
}
