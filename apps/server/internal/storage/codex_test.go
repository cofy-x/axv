package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCodexSessionsPreserveDocumentsIdentityAndLayout(t *testing.T) {
	s := testStore(t)
	file := create(t, s, "documents", "Kept.txt", "file")
	identity, err := s.DatabaseID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	layout := json.RawMessage(`{"version":2,"windows":[{"id":"kept"}]}`)
	if err := s.SaveLayout(ctx, "client", layout); err != nil {
		t.Fatal(err)
	}
	session, _, err := s.ReserveCodexRun(ctx, CodexRunInput{RequestID: "request-1", ProjectPath: "/project", Mode: "new", HistoryHome: "/history", NamePrefix: "axv-store-test", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != "starting" || session.Target != "host" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if _, _, err = s.ReserveCodexRun(ctx, CodexRunInput{RequestID: "request-1", ProjectPath: "/other", Mode: "new", HistoryHome: "/history", NamePrefix: "axv-store-test", Cols: 80, Rows: 24}); err == nil {
		t.Fatal("duplicate idempotency key accepted")
	}
	if _, err = s.BindCodexConversation(ctx, session.RunID, "thread-"+session.RunID, session.HistoryHome); err != nil {
		t.Fatal(err)
	}
	session, err = s.StartCodexRun(ctx, session.RunID)
	if err != nil || session.EndedAt != "" {
		t.Fatalf("start: %+v %v", session, err)
	}
	session, err = s.UpdateCodexRun(ctx, session.RunID, "ended", "Session ended.")
	if err != nil || session.EndedAt == "" {
		t.Fatalf("end: %+v %v", session, err)
	}
	ended := session.EndedAt
	session, err = s.UpdateCodexRun(ctx, session.RunID, "ended", "Session ended.")
	if err != nil || session.EndedAt != ended {
		t.Fatalf("idempotent end changed timestamp: %+v %v", session, err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	kept, err := s.CodexRequest(ctx, "request-1")
	if err != nil || kept.ID != session.ID {
		t.Fatalf("session not retained: %+v %v", kept, err)
	}
	bootstrap, err := s.Bootstrap(ctx, "client")
	if err != nil || bootstrap.DatabaseID != identity || string(bootstrap.Layout) != string(layout) {
		t.Fatalf("session operations changed document state: %+v %v", bootstrap, err)
	}
	entry(t, s, file.ID)
}

func TestCodexRoutingPersistsAcrossReopen(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	identity, err := s.DatabaseID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := s.ReserveCodexRun(ctx, CodexRunInput{RequestID: "runtime", ProjectPath: "/project", NamePrefix: "axv-codex-instance", Socket: "test-socket", Mode: "new", HistoryHome: "/history", Cols: 80, Rows: 24})
	if err != nil || created.TmuxSession != "axv-codex-instance-"+created.RunID || created.TmuxSocket != "test-socket" {
		t.Fatalf("runtime routing: %+v %v", created, err)
	}
	if err := s.SetCodexRunSocket(ctx, created.RunID, "/tmp/tmux-owned/socket"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindCodexConversation(ctx, created.RunID, "thread-"+created.RunID, created.HistoryHome); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartCodexRun(ctx, created.RunID); err != nil {
		t.Fatal(err)
	}
	title, archived := "Persistent title", true
	updated, err := s.PatchCodexMetadata(ctx, created.ID, CodexMetadataPatch{Revision: 1, Title: &title, Archived: &archived})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := s.CodexProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("recent project missing: %+v %v", projects, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	kept, err := reopened.CodexSession(ctx, created.ID)
	if err != nil || kept.ProjectPath != created.ProjectPath || kept.TmuxSocket != "/tmp/tmux-owned/socket" || kept.TmuxSession != created.TmuxSession {
		t.Fatalf("session routing was not retained: %+v %v", kept, err)
	}
	if kept.Title != title || kept.ArchivedAt != updated.ArchivedAt || kept.MetadataRevision != 2 || kept.Status != "running" {
		t.Fatalf("session metadata was not retained: %+v", kept)
	}
	recent, err := reopened.CodexProjects(ctx)
	if err != nil || len(recent) != 1 || recent[0] != projects[0] {
		t.Fatalf("recent project changed after reopening: %+v %v", recent, err)
	}
	actual, err := reopened.DatabaseID(ctx)
	if err != nil || actual != identity {
		t.Fatalf("database identity changed: %q %v", actual, err)
	}
}
