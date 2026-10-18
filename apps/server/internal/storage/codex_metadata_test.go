package storage

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func newCodexRecord(t *testing.T, s *Store, request, project string) CodexSession {
	t.Helper()
	session, _, err := s.ReserveCodexRun(ctx, CodexRunInput{RequestID: request, ProjectPath: project, NamePrefix: "axv-codex-test", Socket: "/tmp/owned-socket", Mode: "new", HistoryHome: "/history", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	session, err = s.BindCodexConversation(ctx, session.RunID, "thread-"+session.RunID, session.HistoryHome)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestCodexMetadataPreservesLifecycleAndRequiresCurrentRevision(t *testing.T) {
	s := testStore(t)
	session := newCodexRecord(t, s, "metadata", "/project")
	if session.Title != "" || session.ArchivedAt != "" || session.MetadataRevision != 1 {
		t.Fatalf("invalid metadata defaults: %+v", session)
	}
	session, err := s.StartCodexRun(ctx, session.RunID)
	if err != nil {
		t.Fatal(err)
	}
	title, archived := "  修复 🚀  ", true
	patched, err := s.PatchCodexMetadata(ctx, session.ID, CodexMetadataPatch{Revision: 1, Title: &title, Archived: &archived})
	if err != nil || patched.Title != "修复 🚀" || patched.ArchivedAt == "" || patched.MetadataRevision != 2 {
		t.Fatalf("metadata patch failed: %+v %v", patched, err)
	}
	if patched.ID != session.ID || patched.Status != session.Status || patched.EndedAt != session.EndedAt || patched.TmuxSession != session.TmuxSession || patched.TmuxSocket != session.TmuxSocket || patched.ProjectPath != session.ProjectPath || patched.CreatedAt != session.CreatedAt {
		t.Fatalf("metadata patch changed runtime identity: %+v -> %+v", session, patched)
	}
	stale, unarchive := "Stale overwrite", false
	_, err = s.PatchCodexMetadata(ctx, session.ID, CodexMetadataPatch{Revision: 1, Title: &stale, Archived: &unarchive})
	requireCode(t, err, "metadata_conflict")
	kept, err := s.CodexSession(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(kept, patched) {
		t.Fatalf("conflict partially wrote metadata: %+v %v", kept, err)
	}
	noChange, err := s.PatchCodexMetadata(ctx, session.ID, CodexMetadataPatch{Revision: 2, Title: &title, Archived: &archived})
	if err != nil || !reflect.DeepEqual(noChange, patched) {
		t.Fatalf("no-op changed revision or archive timestamp: %+v %v", noChange, err)
	}
	ended, err := s.UpdateCodexRun(ctx, session.RunID, "ended", "Explicitly ended.")
	if err != nil || ended.Title != patched.Title || ended.ArchivedAt != patched.ArchivedAt || ended.MetadataRevision != 2 {
		t.Fatalf("lifecycle update changed metadata: %+v %v", ended, err)
	}
	blank := "  "
	cleared, err := s.PatchCodexMetadata(ctx, session.ID, CodexMetadataPatch{Revision: 2, Title: &blank, Archived: &unarchive})
	if err != nil || cleared.Title != "" || cleared.ArchivedAt != "" || cleared.MetadataRevision != 3 || cleared.Status != "ended" || cleared.EndedAt != ended.EndedAt || cleared.Reason != ended.Reason {
		t.Fatalf("clearing metadata changed final state: %+v %v", cleared, err)
	}
	if resumed, err := s.StartCodexRun(ctx, session.RunID); err != nil || !reflect.DeepEqual(resumed, cleared) {
		t.Fatalf("recording a start resurrected an ended session: %+v %v", resumed, err)
	}
}

func TestCodexMetadataValidationAndConcurrentConflict(t *testing.T) {
	s := testStore(t)
	session := newCodexRecord(t, s, "metadata", "/project")
	for _, title := range []string{strings.Repeat("界", 121), "line\nbreak", "tab\t", "\x00", "\x7f", string([]byte{0xff})} {
		_, err := s.PatchCodexMetadata(ctx, session.ID, CodexMetadataPatch{Revision: 1, Title: &title})
		requireCode(t, err, "invalid_metadata")
	}
	_, err := s.PatchCodexMetadata(ctx, session.ID, CodexMetadataPatch{Revision: 1})
	requireCode(t, err, "invalid_metadata")
	title := strings.Repeat("界", 120)
	_, err = s.PatchCodexMetadata(ctx, session.ID, CodexMetadataPatch{Title: &title})
	requireCode(t, err, "invalid_metadata")
	_, err = s.PatchCodexMetadata(ctx, "missing", CodexMetadataPatch{Revision: 1, Title: &title})
	requireCode(t, err, "not_found")

	errs := make(chan error, 2)
	var workers sync.WaitGroup
	archived := true
	for _, patch := range []CodexMetadataPatch{{Revision: 1, Title: &title}, {Revision: 1, Archived: &archived}} {
		workers.Go(func() { _, err := s.PatchCodexMetadata(ctx, session.ID, patch); errs <- err })
	}
	workers.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			requireCode(t, err, "metadata_conflict")
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent writes: %d successes, %d conflicts", success, conflict)
	}
	current, err := s.CodexSession(ctx, session.ID)
	if err != nil || current.MetadataRevision != 2 || (current.Title != "") == (current.ArchivedAt != "") {
		t.Fatalf("conflicting patch partially committed: %+v %v", current, err)
	}
	combined, err := s.PatchCodexMetadata(ctx, session.ID, CodexMetadataPatch{Revision: 2, Title: &title, Archived: &archived})
	if err != nil || combined.Title != title || combined.ArchivedAt == "" || combined.MetadataRevision != 3 {
		t.Fatalf("explicit retry failed: %+v %v", combined, err)
	}
}

func TestCodexRecentProjectsTrackOnlyConfirmedStartsAndStayBounded(t *testing.T) {
	s := testStore(t)
	failed := newCodexRecord(t, s, "failed", "/failed")
	if _, err := s.UpdateCodexRun(ctx, failed.RunID, "failed", "Did not start."); err != nil {
		t.Fatal(err)
	}
	projects, err := s.CodexProjects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("failed start added a project: %+v %v", projects, err)
	}
	var latest CodexSession
	for i := range 12 {
		latest = newCodexRecord(t, s, fmt.Sprintf("start-%d", i), fmt.Sprintf("/project-%02d", i))
		if _, err := s.StartCodexRun(ctx, latest.RunID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateCodexRun(ctx, latest.RunID, "ended", ""); err != nil {
			t.Fatal(err)
		}
	}
	projects, err = s.CodexProjects(ctx)
	if err != nil || len(projects) != 10 || projects[0].Path != "/project-11" || projects[9].Path != "/project-02" {
		t.Fatalf("recent ordering or limit: %+v %v", projects, err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM codex_projects").Scan(&count); err != nil || count != 10 {
		t.Fatalf("project history was not bounded: %d %v", count, err)
	}
	if _, err := s.StartCodexRun(ctx, latest.RunID); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.CodexProjects(ctx)
	if err != nil || !reflect.DeepEqual(projects, unchanged) {
		t.Fatalf("repeat confirmation changed recency: %+v %v", unchanged, err)
	}
	again := newCodexRecord(t, s, "start-again", "/project-02")
	if _, err := s.StartCodexRun(ctx, again.RunID); err != nil {
		t.Fatal(err)
	}
	projects, err = s.CodexProjects(ctx)
	if err != nil || len(projects) != 10 || projects[0].Path != "/project-02" || projects[0].LastUsedAt <= unchanged[0].LastUsedAt {
		t.Fatalf("new start did not refresh the project: %+v %v", projects, err)
	}
}

func TestCodexStartAndRecentProjectWriteAreAtomic(t *testing.T) {
	s := testStore(t)
	session := newCodexRecord(t, s, "atomic-start", "/project")
	if _, err := s.db.Exec("CREATE TRIGGER reject_project BEFORE INSERT ON codex_projects BEGIN SELECT RAISE(ABORT, 'project rejected'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartCodexRun(ctx, session.RunID); err == nil {
		t.Fatal("expected failed recent-project write")
	}
	current, err := s.CodexSession(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(current, session) {
		t.Fatalf("failed project write changed lifecycle: %+v %v", current, err)
	}
}
