//go:build darwin || linux

package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/hostdir"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func decodedRun(t *testing.T, status int, data []byte) Session {
	t.Helper()
	var result struct {
		Session Session `json:"session"`
	}
	if status != 200 || json.Unmarshal(data, &result) != nil || result.Session.RunID == "" {
		t.Fatalf("run response %d %s", status, data)
	}
	return result.Session
}

func waitRunning(t *testing.T, server *httptest.Server, session storage.CodexSession) storage.CodexSession {
	t.Helper()
	eventually(t, func() bool {
		status, data := jsonRequest(t, server, "GET", runPath(session), nil)
		var result struct {
			Session storage.CodexSession `json:"session"`
		}
		if status != 200 || json.Unmarshal(data, &result) != nil {
			return false
		}
		session = result.Session
		return session.Status == "running"
	})
	return session
}

func TestRealCodexResumePreservesHistoryAndOldRunIsolation(t *testing.T) {
	m, server, home := realManager(t)
	first := createSession(t, server, "new-conversation", home)
	if first.ConversationID == "" || first.RunID == first.ID {
		t.Fatalf("missing independent identity: %+v", first)
	}
	conn := connectSession(t, server, first)
	outputUntil(t, conn, "FIXTURE_READY")
	pidBefore, err := m.sessionTmux(context.Background(), executable("tmux"), first, "display-message", "-p", "-t", paneTarget(first), "#{pane_pid}")
	if err != nil {
		t.Fatal(err)
	}
	status, data := jsonRequest(t, server, "PATCH", "/api/host/codex/sessions/"+first.ID, map[string]any{"revision": 1, "title": "Saved conversation", "archived": true})
	metadata := decodedRun(t, status, data)
	status, data = jsonRequest(t, server, "POST", runPath(first)+"/end", nil)
	ended := decodedRun(t, status, data)
	if ended.Status != "ended" || ended.Continuation.State != "available" {
		t.Fatalf("ended continuation: %+v", ended)
	}
	verifyCount := 0
	m.verifyHistory = func(_ context.Context, _, project, history string, _ []string, id string) error {
		verifyCount++
		if project != first.ProjectPath || history != first.HistoryHome || id != first.ConversationID {
			t.Fatalf("wrong history verified: %s %s %s", project, history, id)
		}
		return nil
	}
	body := map[string]any{"requestId": "continue-conversation", "expectedRunId": first.RunID}
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions/"+first.ID+"/resume", body)
	second := waitRunning(t, server, decodedRun(t, status, data).CodexSession)
	if second.ID != first.ID || second.RunID == first.RunID || second.ConversationID != first.ConversationID || second.Title != metadata.Title || second.ArchivedAt != metadata.ArchivedAt || second.MetadataRevision != metadata.MetadataRevision {
		t.Fatalf("resume changed conversation: %+v", second)
	}
	pidAfter, err := m.sessionTmux(context.Background(), executable("tmux"), second, "display-message", "-p", "-t", paneTarget(second), "#{pane_pid}")
	if err != nil || pidAfter == pidBefore {
		t.Fatalf("resume reused old runtime: %s %s %v", pidBefore, pidAfter, err)
	}
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions/"+first.ID+"/resume", body)
	if retried := decodedRun(t, status, data); retried.RunID != second.RunID || verifyCount != 1 {
		t.Fatalf("retry launched or verified again: %+v count=%d", retried, verifyCount)
	}
	status, data = jsonRequest(t, server, "GET", runPath(first), nil)
	old := decodedRun(t, status, data)
	if old.RunID != first.RunID || old.Status != "ended" || old.Continuation.State != "active" || old.Continuation.ActiveRunID != second.RunID {
		t.Fatalf("old view adopted new run: %+v", old)
	}
	status, data = jsonRequest(t, server, "POST", runPath(first)+"/end", nil)
	if decodedRun(t, status, data).RunID != first.RunID {
		t.Fatal("old end targeted new run")
	}
	_ = conn.CloseNow()
	eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.connections[first.RunID]) == 0
	})
	beforeRun := m.run
	m.run = func(context.Context, string, ...string) (string, error) {
		t.Fatal("old run terminal control reached tmux")
		return "", nil
	}
	if err := m.scroll(context.Background(), executable("tmux"), first.RunID, 10); err == nil {
		t.Fatal("old scroll accepted")
	}
	if err := m.waitSize(context.Background(), executable("tmux"), first.RunID, 80, 24); err == nil {
		t.Fatal("old resize accepted")
	}
	m.run = beforeRun
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stale, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+runPath(first)+"/terminal", &websocket.DialOptions{HTTPHeader: map[string][]string{"Origin": {server.URL}}})
	if stale != nil {
		stale.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != 409 {
		t.Fatalf("old attachment reached new run: %v %+v", err, response)
	}
	current := connectSession(t, server, second)
	input(t, current, "continued-input\r")
	outputUntil(t, current, "ACK=continued-input")
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions/"+first.ID+"/resume", map[string]any{"requestId": "duplicate-resume", "expectedRunId": second.RunID})
	if status != 409 || !strings.Contains(string(data), "conversation_active") {
		t.Fatalf("active conversation resumed twice: %d %s", status, data)
	}
}

func TestRealCodexUnavailableHistoryAndFailedResumeNeverCreateReplacement(t *testing.T) {
	m, server, home := realManager(t)
	first := createSession(t, server, "history", home)
	status, data := jsonRequest(t, server, "POST", runPath(first)+"/end", nil)
	decodedRun(t, status, data)
	m.verifyHistory = func(context.Context, string, string, string, []string, string) error {
		return runner.ErrHistoryUnavailable
	}
	body := map[string]any{"requestId": "continue", "expectedRunId": first.RunID}
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions/"+first.ID+"/resume", body)
	if status != 409 || !strings.Contains(string(data), "history_unavailable") {
		t.Fatalf("missing history: %d %s", status, data)
	}
	latest, err := m.store.CodexSession(context.Background(), first.ID)
	if err != nil || latest.RunID != first.RunID || latest.ConversationID != first.ConversationID || latest.Status != "ended" {
		t.Fatalf("missing history created replacement: %+v %v", latest, err)
	}
	_, err = m.store.CodexRequest(context.Background(), "continue")
	var absent *storage.Error
	if !errors.As(err, &absent) || absent.Code != "not_found" {
		t.Fatalf("missing history reserved a continuation request: %v", err)
	}
	m.verifyHistory = func(context.Context, string, string, string, []string, string) error { return nil }
	m.runnerCommand = func(string) ([]string, error) { return nil, fmt.Errorf("fixture runner unavailable") }
	status, data = jsonRequest(t, server, "POST", "/api/host/codex/sessions/"+first.ID+"/resume", body)
	failed := decodedRun(t, status, data)
	if failed.Status != "failed" || failed.RunID == first.RunID || failed.ConversationID != first.ConversationID || failed.Continuation.State != "available" {
		t.Fatalf("failed resume lost conversation: %+v", failed)
	}
	prior, err := m.store.CodexRun(context.Background(), first.ID, first.RunID)
	if err != nil || prior.Status != "ended" || prior.ConversationID != first.ConversationID {
		t.Fatalf("failed resume changed prior run: %+v %v", prior, err)
	}
	attempt, err := m.store.CodexRequest(context.Background(), "continue")
	if err != nil || attempt.RunID != failed.RunID || attempt.PreviousRunID != first.RunID {
		t.Fatalf("failed resume lost its exact request or predecessor: %+v %v", attempt, err)
	}
}

func TestRunnerReadinessRecordsRecentProjectBeforeImmediateEndOrExit(t *testing.T) {
	for _, naturalExit := range []bool{false, true} {
		t.Run(fmt.Sprintf("natural-%t", naturalExit), func(t *testing.T) {
			m, server, home := realManager(t)
			project, err := hostdir.CanonicalDirectory(home)
			if err != nil {
				t.Fatal(err)
			}
			history, err := historyHome()
			if err != nil {
				t.Fatal(err)
			}
			run, _, err := m.store.ReserveCodexRun(context.Background(), storage.CodexRunInput{Mode: "new", RequestID: "ready-before-poll", ProjectPath: project, HistoryHome: history, NamePrefix: m.namePrefix, Socket: m.socket, Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			if err = m.start(context.Background(), probe(context.Background()), run, 80, 24); err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool {
				_, err := os.Stat(filepath.Join(m.runDirectory(run.RunID), "state.json"))
				return err == nil
			})
			before, err := m.store.CodexRunByID(context.Background(), run.RunID)
			if err != nil || before.Status != "starting" || before.ConversationID != "" {
				t.Fatalf("test observed readiness too early: %+v %v", before, err)
			}
			if naturalExit {
				if _, err = m.sessionTmux(context.Background(), executable("tmux"), run, "send-keys", "-t", paneTarget(run), "exit", "Enter"); err != nil {
					t.Fatal(err)
				}
				eventually(t, func() bool {
					output, _ := m.sessionTmux(context.Background(), executable("tmux"), run, "display-message", "-p", "-t", paneTarget(run), "#{pane_dead}")
					return output == "1"
				})
			}
			method, endpoint := "POST", runPath(run)+"/end"
			if naturalExit {
				method, endpoint = "GET", runPath(run)
			}
			status, data := jsonRequest(t, server, method, endpoint, nil)
			ended := decodedRun(t, status, data)
			if ended.Status != "ended" || ended.ConversationID == "" {
				details, detailErr := m.sessionTmux(context.Background(), executable("tmux"), run, "display-message", "-p", "-t", paneTarget(run), "#{pane_dead}:#{pane_dead_status}:#{pane_dead_signal}:#{pane_pid}")
				t.Logf("tmux exit details: %q %v", details, detailErr)
				t.Fatalf("readiness was lost before end: %+v", ended)
			}
			projects, err := m.store.CodexProjects(context.Background())
			if err != nil || len(projects) != 1 || projects[0].Path != project {
				t.Fatalf("confirmed start missing recent project: %+v %v", projects, err)
			}
		})
	}
}

func TestPendingReservationSurvivesConcurrentInspectionAndEnd(t *testing.T) {
	m, server, home := realManager(t)
	run, _, err := m.store.ReserveCodexRun(context.Background(), storage.CodexRunInput{Mode: "new", RequestID: "reserved-before-tmux", ProjectPath: home, HistoryHome: filepath.Join(home, "config"), NamePrefix: m.namePrefix, Socket: m.socket, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	status, data := jsonRequest(t, server, "GET", runPath(run), nil)
	if decodedRun(t, status, data).Status != "starting" {
		t.Fatalf("concurrent inspection abandoned reservation: %s", data)
	}
	status, data = jsonRequest(t, server, "POST", runPath(run)+"/end", nil)
	if status != 409 || !strings.Contains(string(data), "run_starting") {
		t.Fatalf("concurrent end abandoned reservation: %d %s", status, data)
	}
	if err = m.start(context.Background(), probe(context.Background()), run, 80, 24); err != nil {
		t.Fatal(err)
	}
	run = waitRunning(t, server, run)
	if run.ConversationID == "" {
		t.Fatal("launch after concurrent inspection lost identity")
	}
	status, data = jsonRequest(t, server, "POST", runPath(run)+"/end", nil)
	if decodedRun(t, status, data).Status != "ended" {
		t.Fatal("end after startup failed")
	}
}

func TestRealCodexExplicitEndWorksWithCorruptRunnerState(t *testing.T) {
	m, server, home := realManager(t)
	run := createSession(t, server, "corrupt-state", home)
	if err := os.WriteFile(filepath.Join(m.runDirectory(run.RunID), "state.json"), []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	status, data := jsonRequest(t, server, "POST", runPath(run)+"/end", nil)
	ended := decodedRun(t, status, data)
	if ended.Status != "ended" || ended.ConversationID != run.ConversationID {
		t.Fatalf("corrupt metadata blocked explicit end: %+v", ended)
	}
	if _, err := m.sessionTmux(context.Background(), executable("tmux"), run, "has-session", "-t", target(run)); err == nil {
		t.Fatal("explicit end left runtime alive")
	}
}
