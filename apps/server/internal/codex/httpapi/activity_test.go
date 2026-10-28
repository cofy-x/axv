package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func TestActivityReviewChecksAccessAndStrictInput(t *testing.T) {
	handler, manager := testHandler(t)
	mux := http.NewServeMux()
	handler.Register(mux)
	for _, tc := range []struct {
		name, origin, body string
		status             int
		code               string
	}{
		{"missing origin", "", `{}`, 403, "host_forbidden"},
		{"foreign origin", "http://localhost:3002", `{}`, 403, "host_forbidden"},
		{"unknown field", "http://localhost:3001", `{"eventId":"` + strings.Repeat("a", 32) + `","approve":true}`, 400, "invalid_json"},
		{"missing event", "http://localhost:3001", `{}`, 400, "invalid_activity_event"},
		{"malformed event", "http://localhost:3001", `{"eventId":"old-event"}`, 400, "invalid_activity_event"},
		{"unknown run", "http://localhost:3001", `{"eventId":"` + strings.Repeat("a", 32) + `"}`, 404, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/host/codex/sessions/missing/runs/original/activity/review", strings.NewReader(tc.body))
			r.RemoteAddr, r.Host = "127.0.0.1:3456", "localhost:3001"
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set(httpaccess.DatabaseIdentityHeader, manager.DatabaseID())
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
				t.Fatalf("unexpected activity review response: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestActivitySuccessfulTurnRejectsDismissalWithoutChangingExistingRecord(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires supported Codex activity filesystem reads")
	}
	directory := t.TempDir()
	store, err := storage.Open(filepath.Join(directory, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	run, _, err := store.ReserveCodexRun(ctx, storage.CodexRunInput{
		Mode: "new", RequestID: "success-dismissal", ProjectPath: "/fixture/project", HistoryHome: "/fixture/history",
		NamePrefix: "fixture", Socket: "fixture", Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	threadID := "01234567-89ab-cdef-0123-456789abcdef"
	if _, err := store.BindCodexConversation(ctx, run.RunID, threadID, run.HistoryHome); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCodexRun(ctx, run.RunID); err != nil {
		t.Fatal(err)
	}
	manager, err := codex.New(store, codex.Options{DataDir: directory, ProfileHome: directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	handler := New(manager)
	t.Cleanup(handler.Close)
	mux := http.NewServeMux()
	handler.Register(mux)
	runDirectory := filepath.Join(directory, "codex-runs", run.RunID)
	if err := os.MkdirAll(runDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	event := strings.Repeat("a", 32)
	if err := jsonfile.Write(filepath.Join(runDirectory, "activity.json"), runner.Activity{
		RunID: run.RunID, ThreadID: threadID, State: runner.ActivityCompleted, EventID: event,
		Sequence: 2, TurnID: "turn-fixture", UpdatedAt: time.Now().UTC(),
	}, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runDirectory, "activity-review.json")
	const damaged = `{"runId":"foreign","eventId":"invalid"}`
	if err := os.WriteFile(path, []byte(damaged), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/host/codex/sessions/"+run.ID+"/runs/"+run.RunID+"/activity/review", strings.NewReader(`{"eventId":"`+event+`"}`))
	r.RemoteAddr, r.Host = "127.0.0.1:3456", "localhost:3001"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(httpaccess.DatabaseIdentityHeader, manager.DatabaseID())
	r.Header.Set("Origin", "http://localhost:3001")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "activity_not_actionable") {
		t.Fatalf("success dismissal = %d %s", w.Code, w.Body.String())
	}
	if after, err := os.Lstat(path); err != nil || !os.SameFile(before, after) {
		t.Fatalf("success dismissal replaced an existing record: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != damaged {
		t.Fatalf("success dismissal changed an existing record: %s, %v", data, err)
	}
}
