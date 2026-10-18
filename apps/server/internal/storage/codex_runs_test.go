package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func reserveRun(t *testing.T, s *Store, request string) CodexSession {
	t.Helper()
	run, created, err := s.ReserveCodexRun(ctx, CodexRunInput{Mode: "new", RequestID: request, ProjectPath: "/project", HistoryHome: "/history", NamePrefix: "axv-test", Socket: "/tmp/test-socket", Cols: 80, Rows: 24})
	if err != nil || !created {
		t.Fatalf("reserve: %+v %v %v", run, created, err)
	}
	return run
}

func resumableRun(t *testing.T, s *Store) CodexSession {
	t.Helper()
	run := reserveRun(t, s, "initial")
	run, err := s.BindCodexConversation(ctx, run.RunID, "01234567-89ab-cdef-0123-456789abcdef", "/history")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartCodexRun(ctx, run.RunID); err != nil {
		t.Fatal(err)
	}
	run, err = s.UpdateCodexRun(ctx, run.RunID, "ended", "Ended by user.")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestCodexResumeKeepsConversationAndExactHistoricalRun(t *testing.T) {
	s := testStore(t)
	first := resumableRun(t, s)
	if first.PreviousRunID != "" || first.RunMode() != "new" {
		t.Fatalf("initial run unexpectedly has a predecessor: %+v", first)
	}
	title, archive := "Kept conversation", true
	meta, err := s.PatchCodexMetadata(ctx, first.ID, CodexMetadataPatch{Revision: 1, Title: &title, Archived: &archive})
	if err != nil {
		t.Fatal(err)
	}
	in := CodexRunInput{Mode: "resume", RequestID: "continue", SessionID: first.ID, ExpectedRunID: first.RunID, NamePrefix: "axv-next", Socket: "/tmp/next", Cols: 100, Rows: 30}
	second, created, err := s.ReserveCodexRun(ctx, in)
	if err != nil || !created || second.ID != first.ID || second.RunID == first.RunID || second.TmuxSession == first.TmuxSession || second.ConversationID != first.ConversationID || second.HistoryHome != first.HistoryHome || second.Title != title || second.ArchivedAt != meta.ArchivedAt || second.MetadataRevision != 2 {
		t.Fatalf("continuation lost identity or metadata: %+v %v %v", second, created, err)
	}
	if second.PreviousRunID != first.RunID || second.RunMode() != "resume" {
		t.Fatalf("continuation lost its predecessor: %+v", second)
	}
	retried, created, err := s.ReserveCodexRun(ctx, in)
	if err != nil || created || retried.RunID != second.RunID {
		t.Fatalf("retry duplicated continuation: %+v %v %v", retried, created, err)
	}
	changed := in
	changed.Cols++
	if _, _, err = s.ReserveCodexRun(ctx, changed); errorCode(err) != "request_conflict" {
		t.Fatalf("changed retry accepted: %v", err)
	}
	if _, err = s.UpdateCodexRun(ctx, first.RunID, "running", "revive"); err != nil {
		t.Fatal(err)
	}
	old, err := s.CodexRun(ctx, first.ID, first.RunID)
	if err != nil || old.Status != "ended" || old.EndedAt != first.EndedAt || old.TmuxSession != first.TmuxSession || old.TmuxSocket != first.TmuxSocket {
		t.Fatalf("historical run changed: %+v %v", old, err)
	}
	latest, err := s.CodexSession(ctx, first.ID)
	if err != nil || latest.RunID != second.RunID || latest.Status != "starting" {
		t.Fatalf("latest run changed: %+v %v", latest, err)
	}
	if _, err = s.CodexRun(ctx, "other-session", second.RunID); errorCode(err) != "not_found" {
		t.Fatalf("run escaped its conversation: %v", err)
	}
	if _, err = s.BindCodexConversation(ctx, second.RunID, "different-thread", "/history"); errorCode(err) != "history_identity_conflict" {
		t.Fatalf("continuation changed conversation: %v", err)
	}
	if _, err = s.UpdateCodexRun(ctx, second.RunID, "failed", "Resume failed."); err != nil {
		t.Fatal(err)
	}
	latest, err = s.CodexSession(ctx, first.ID)
	if err != nil || latest.ConversationID != first.ConversationID || latest.Status != "failed" {
		t.Fatalf("failed resume lost history: %+v %v", latest, err)
	}
	thirdInput := in
	thirdInput.RequestID, thirdInput.ExpectedRunID = "retry-failed-resume", second.RunID
	third, _, err := s.ReserveCodexRun(ctx, thirdInput)
	if err != nil || third.RunID == second.RunID || third.ConversationID != first.ConversationID {
		t.Fatalf("retry after failure: %+v %v", third, err)
	}
	oldRetry, created, err := s.ReserveCodexRun(ctx, in)
	if err != nil || created || oldRetry.RunID != second.RunID || oldRetry.Status != "failed" {
		t.Fatalf("late retry changed operation: %+v %v %v", oldRetry, created, err)
	}
}

func errorCode(err error) string {
	var problem *Error
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}

func TestCodexReservationSerializesAcrossDatabaseConnections(t *testing.T) {
	for _, sameRequest := range []bool{false, true} {
		t.Run(fmt.Sprintf("same-request-%t", sameRequest), func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "desktop.sqlite")
			firstStore, err := Open(filename)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { firstStore.Close() })
			secondStore, err := Open(filename)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { secondStore.Close() })
			first := resumableRun(t, firstStore)
			type result struct {
				run     CodexSession
				created bool
				err     error
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			for i, store := range []*Store{firstStore, secondStore} {
				go func() {
					<-start
					request := fmt.Sprintf("resume-%d", i)
					if sameRequest {
						request = "resume"
					}
					run, created, err := store.ReserveCodexRun(ctx, CodexRunInput{Mode: "resume", RequestID: request, SessionID: first.ID, ExpectedRunID: first.RunID, NamePrefix: "axv-concurrent-test", Cols: 80, Rows: 24})
					results <- result{run, created, err}
				}()
			}
			close(start)
			createdCount, rejectedCount, successfulID := 0, 0, ""
			for range 2 {
				result := <-results
				if result.err != nil {
					if sameRequest || errorCode(result.err) != "conversation_active" {
						t.Fatalf("unexpected concurrent error: %v", result.err)
					}
					rejectedCount++
					continue
				}
				if successfulID != "" && successfulID != result.run.RunID {
					t.Fatal("same request created distinct runs")
				}
				successfulID = result.run.RunID
				if result.created {
					createdCount++
				}
			}
			if createdCount != 1 || (!sameRequest && rejectedCount != 1) {
				t.Fatalf("created=%d rejected=%d", createdCount, rejectedCount)
			}
		})
	}
}

func TestCodexCapacityReservationIncludesStartingAndArchivedRuns(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	a, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	archived := reserveRun(t, a, "archived-starting")
	archive := true
	if _, err := a.PatchCodexMetadata(ctx, archived.ID, CodexMetadataPatch{Revision: 1, Archived: &archive}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store := a
			if i%2 == 0 {
				store = b
			}
			_, _, err := store.ReserveCodexRun(ctx, CodexRunInput{Mode: "new", RequestID: fmt.Sprintf("capacity-%d", i), ProjectPath: "/project", HistoryHome: "/history", NamePrefix: "axv-capacity-test", Cols: 80, Rows: 24})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	created := 0
	for err := range results {
		if err == nil {
			created++
		} else if errorCode(err) != "codex_limit" {
			t.Fatalf("unexpected capacity error: %v", err)
		}
	}
	if created != 3 {
		t.Fatalf("reserved %d additional runs, want 3", created)
	}
}

func TestCodexStartRequiresConfirmedConversation(t *testing.T) {
	s := testStore(t)
	run := reserveRun(t, s, "unconfirmed")
	if _, err := s.StartCodexRun(ctx, run.RunID); errorCode(err) != "history_unavailable" {
		t.Fatalf("unconfirmed run started: %v", err)
	}
	kept, err := s.CodexRun(ctx, run.ID, run.RunID)
	if err != nil || !reflect.DeepEqual(kept, run) {
		t.Fatalf("failed start changed run: %+v %v", kept, err)
	}
	projects, err := s.CodexProjects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("unconfirmed start changed recency: %+v %v", projects, err)
	}
}

func TestCodexReservationRejectsInvalidIntentWithoutPartialRecords(t *testing.T) {
	s := testStore(t)
	for _, in := range []CodexRunInput{
		{Mode: "unknown"},
		{Mode: "new", SessionID: "existing"},
		{Mode: "new", ExpectedRunID: "previous"},
		{Mode: "resume", SessionID: "existing"},
		{Mode: "resume", ExpectedRunID: "previous"},
		{Mode: "new", HistoryHome: "/history"},
		{Mode: "new", NamePrefix: "axv-test"},
		{Mode: "resume", SessionID: "existing", ExpectedRunID: "previous"},
	} {
		if _, created, err := s.ReserveCodexRun(ctx, in); errorCode(err) != "invalid_session" || created {
			t.Fatalf("accepted invalid intent %+v: created=%v error=%v", in, created, err)
		}
	}
	for _, dimensions := range [][2]int{{1, 24}, {501, 24}, {80, 0}, {80, 201}} {
		_, created, err := s.ReserveCodexRun(ctx, CodexRunInput{Mode: "new", RequestID: "invalid-size", ProjectPath: "/project", HistoryHome: "/history", NamePrefix: "axv-size-test", Cols: dimensions[0], Rows: dimensions[1]})
		if err == nil || created {
			t.Fatalf("accepted invalid dimensions %v: created=%v error=%v", dimensions, created, err)
		}
	}
	var sessions, runs int
	if err := s.db.QueryRow("SELECT (SELECT count(*) FROM codex_sessions),(SELECT count(*) FROM codex_runs)").Scan(&sessions, &runs); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || runs != 0 {
		t.Fatalf("failed reservation retained %d conversations and %d runs", sessions, runs)
	}
}

func TestCodexSchemaEnforcesRuntimeReferencesAndUniqueness(t *testing.T) {
	s := testStore(t)
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO codex_sessions(id,project_path,created_at,history_home,latest_run_id) VALUES('orphan','/project','created','/history','missing-run')")
		if err != nil {
			t.Fatalf("latest-run reference was not deferred: %v", err)
		}
		return nil
	})
	if err == nil {
		t.Fatal("committed a conversation without its latest run")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM codex_sessions").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed commit retained orphan conversation: %d %v", count, err)
	}
	first := reserveRun(t, s, "first")
	if _, err := s.db.Exec("UPDATE codex_sessions SET history_home='' WHERE id=?", first.ID); err == nil {
		t.Fatal("accepted an empty conversation history directory")
	}
	if _, err := s.db.Exec("UPDATE codex_runs SET tmux_session='' WHERE id=?", first.RunID); err == nil {
		t.Fatal("accepted an empty tmux target")
	}
	if _, err := s.db.Exec("UPDATE codex_sessions SET latest_run_id='missing-run' WHERE id=?", first.ID); err == nil {
		t.Fatal("accepted a missing latest run")
	}
	if _, err := s.db.Exec(`INSERT INTO codex_runs(id,session_id,request_id,previous_run_id,request_cols,request_rows,status,created_at,tmux_session,tmux_socket)
		VALUES('missing-predecessor',?,'missing-predecessor','missing-run',80,24,'ended','created','target','socket')`, first.ID); err == nil {
		t.Fatal("accepted a missing predecessor")
	}
	if _, err := s.db.Exec(`INSERT INTO codex_runs(id,session_id,request_id,request_cols,request_rows,status,created_at,tmux_session,tmux_socket)
		VALUES('second-active',?,'second-active',80,24,'running','created','target','socket')`, first.ID); err == nil {
		t.Fatal("accepted two active runs for one conversation")
	}
	kept, err := s.CodexSession(ctx, first.ID)
	if err != nil || !reflect.DeepEqual(kept, first) {
		t.Fatalf("constraint failure changed the active run: %+v %v", kept, err)
	}
	if _, err := s.BindCodexConversation(ctx, first.RunID, "confirmed-thread", first.HistoryHome); err != nil {
		t.Fatal(err)
	}
	second := reserveRun(t, s, "second")
	if _, err := s.BindCodexConversation(ctx, second.RunID, "confirmed-thread", second.HistoryHome); errorCode(err) != "history_identity_conflict" {
		t.Fatalf("accepted a duplicate provider identity: %v", err)
	}
	if _, err := s.db.Exec("UPDATE codex_sessions SET conversation_id='confirmed-thread' WHERE id=?", second.ID); err == nil {
		t.Fatal("schema accepted a duplicate provider identity")
	}
}

func TestCodexHistoryPersistsAcrossReopen(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	first := resumableRun(t, s)
	second, created, err := s.ReserveCodexRun(ctx, CodexRunInput{Mode: "resume", RequestID: "continue", SessionID: first.ID, ExpectedRunID: first.RunID, NamePrefix: "axv-resumed", Socket: "/tmp/resumed", Cols: 101, Rows: 33})
	if err != nil || !created {
		t.Fatalf("reserve continuation: %+v %v %v", second, created, err)
	}
	second, err = s.StartCodexRun(ctx, second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	latest, err := reopened.CodexSession(ctx, first.ID)
	if err != nil || !reflect.DeepEqual(latest, second) {
		t.Fatalf("latest run changed after reopening: %+v %v", latest, err)
	}
	historical, err := reopened.CodexRun(ctx, first.ID, first.RunID)
	if err != nil || !reflect.DeepEqual(historical, first) {
		t.Fatalf("historical run changed after reopening: %+v %v", historical, err)
	}
	for request, want := range map[string]CodexSession{"initial": first, "continue": second} {
		got, err := reopened.CodexRequest(ctx, request)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("request %q changed after reopening: %+v %v", request, got, err)
		}
	}
}
