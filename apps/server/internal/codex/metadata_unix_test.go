//go:build darwin || linux

package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func TestRealCodexMetadataKeepsRuntimeAndRecentProjectIdentity(t *testing.T) {
	m, server, home := realManager(t)
	project := filepath.Join(home, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "project-link")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	session := createSession(t, server, "metadata", alias)
	path := executable("tmux")
	pid, err := m.sessionTmux(context.Background(), path, session, "display-message", "-p", "-t", target(session)+":0.0", "#{pane_pid}")
	if err != nil || pid == "" {
		t.Fatalf("pane PID: %q %v", pid, err)
	}
	projects, err := m.store.CodexProjects(context.Background())
	if err != nil || len(projects) != 1 || projects[0].Path != session.ProjectPath || projects[0].Path == alias {
		t.Fatalf("recent canonical project: %+v %v", projects, err)
	}
	conn := connectSession(t, server, session)
	outputUntil(t, conn, "FIXTURE_READY")
	status, data := jsonRequest(t, server, "PATCH", "/api/host/codex/sessions/"+session.ID, map[string]any{"revision": 1, "title": "A named session", "archived": true})
	var result struct {
		Session storage.CodexSession `json:"session"`
	}
	if err := json.Unmarshal(data, &result); err != nil || status != 200 || result.Session.Connections != 1 || result.Session.MetadataRevision != 2 || result.Session.ArchivedAt == "" || result.Session.Status != "running" || result.Session.TmuxSession != session.TmuxSession {
		t.Fatalf("live metadata update: %d %s %v", status, data, err)
	}
	input(t, conn, "still-running\r")
	outputUntil(t, conn, "ACK=still-running")
	currentPID, err := m.sessionTmux(context.Background(), path, session, "display-message", "-p", "-t", target(session)+":0.0", "#{pane_pid}")
	if err != nil || currentPID != pid {
		t.Fatalf("metadata restarted the CLI: %q -> %q %v", pid, currentPID, err)
	}
	if again := createSession(t, server, "metadata", project); again.ID != session.ID || again.Title != "A named session" || again.ArchivedAt == "" {
		t.Fatalf("idempotent retry lost metadata: %+v", again)
	}
	status, data = jsonRequest(t, server, "GET", "/api/host/codex/sessions", nil)
	if status != 200 || !strings.Contains(string(data), `"archivedAt"`) || !strings.Contains(string(data), session.ID) {
		t.Fatalf("list excluded archived session: %d %s", status, data)
	}
	status, data = jsonRequest(t, server, "GET", "/api/host/codex/projects", nil)
	var recent struct {
		Projects []storage.CodexProject `json:"projects"`
	}
	if err := json.Unmarshal(data, &recent); err != nil || status != 200 || !reflect.DeepEqual(recent.Projects, projects) {
		t.Fatalf("reads or retry changed project recency: %d %s %v", status, data, err)
	}
	_ = conn.CloseNow()
	reconnected := connectSession(t, server, session)
	input(t, reconnected, "reattached\r")
	outputUntil(t, reconnected, "ACK=reattached")
	unchanged, err := m.store.CodexProjects(context.Background())
	if err != nil || !reflect.DeepEqual(unchanged, projects) {
		t.Fatalf("reattachment changed project recency: %+v %v", unchanged, err)
	}
	status, data = jsonRequest(t, server, "PATCH", "/api/host/codex/sessions/"+session.ID, map[string]any{"revision": 2, "archived": false})
	if status != 200 || strings.Contains(string(data), `"archivedAt"`) || !strings.Contains(string(data), `"title":"A named session"`) {
		t.Fatalf("unarchive: %d %s", status, data)
	}
	other := createSession(t, server, "new-in-same-project", project)
	if other.ID == session.ID {
		t.Fatal("explicit new start reused the old session")
	}
	updated, err := m.store.CodexProjects(context.Background())
	if err != nil || len(updated) != 1 || updated[0].Path != projects[0].Path || updated[0].LastUsedAt <= projects[0].LastUsedAt {
		t.Fatalf("new start did not update recent project: %+v %v", updated, err)
	}
}
