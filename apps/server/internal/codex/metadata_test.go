package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func metadataRequest(t *testing.T, m *Manager, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	m.Register(mux)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr, r.Host = "127.0.0.1:44", "localhost:3001"
	r.Header.Set("Origin", "http://localhost:3001")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-AXV-Database-ID", m.databaseID)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestMetadataEndpointsNeverInspectOrStartRuntime(t *testing.T) {
	m := testManager(t)
	m.probe = func(context.Context) Info { t.Fatal("metadata request probed the host"); return Info{} }
	m.run = func(context.Context, string, ...string) (string, error) {
		t.Fatal("metadata request executed tmux")
		return "", nil
	}
	session, _, err := m.store.ReserveCodexRun(context.Background(), storage.CodexRunInput{RequestID: "metadata", ProjectPath: "/project", HistoryHome: "/history", NamePrefix: m.namePrefix, Socket: m.socket, Mode: "new", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	w := metadataRequest(t, m, "GET", "/api/host/codex/projects", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"projects":[]}` || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("empty projects: %d %s", w.Code, w.Body.String())
	}
	w = metadataRequest(t, m, "PATCH", "/api/host/codex/sessions/"+session.ID, `{"revision":1,"title":"  中文 🚀  ","archived":true}`)
	var result struct {
		Session Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Session.Title != "中文 🚀" || result.Session.MetadataRevision != 2 || result.Session.ArchivedAt == "" || result.Session.Status != "starting" || result.Session.AttachCommand == "" {
		t.Fatalf("metadata response: %d %s %v", w.Code, w.Body.String(), err)
	}
	w = metadataRequest(t, m, "PATCH", "/api/host/codex/sessions/"+session.ID, `{"revision":1,"title":"stale","archived":false}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"metadata_conflict"`) {
		t.Fatalf("stale patch: %d %s", w.Code, w.Body.String())
	}
	w = metadataRequest(t, m, "PATCH", "/api/host/codex/sessions/missing", `{"revision":1,"title":"new"}`)
	if w.Code != 404 {
		t.Fatalf("missing session: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(m.settings.WorkspaceDir); !os.IsNotExist(err) {
		t.Fatalf("metadata request created a workspace: %v", err)
	}
}

func TestMetadataEndpointRejectsInvalidPatchWithoutPartialWrite(t *testing.T) {
	m := testManager(t)
	session, _, err := m.store.ReserveCodexRun(context.Background(), storage.CodexRunInput{RequestID: "metadata", ProjectPath: "/project", HistoryHome: "/history", NamePrefix: m.namePrefix, Socket: m.socket, Mode: "new", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`null`, `[]`, `{}`, `{"revision":1}`, `{"title":"name"}`, `{"revision":0,"title":"name"}`,
		`{"revision":1.5,"title":"name"}`, `{"revision":"1","title":"name"}`, `{"revision":null,"title":"name"}`,
		`{"revision":1,"title":null}`, `{"revision":1,"title":3}`, `{"revision":1,"archived":null}`,
		`{"revision":1,"archived":"true"}`, `{"revision":1,"archived":1}`, `{"revision":1,"status":"ended","title":"name"}`,
		`{"revision":1,"title":"name"} {}`, `{"revision":1,"title":"line\nbreak","archived":true}`,
		`{"revision":1,"title":"` + strings.Repeat("界", 121) + `","archived":true}`,
		`{"revision":1,"title":"` + string([]byte{0xff}) + `","archived":true}`,
		`{"revision":1,"title":"` + strings.Repeat("x", 17<<10) + `","archived":true}`,
	} {
		w := metadataRequest(t, m, "PATCH", "/api/host/codex/sessions/"+session.ID, body)
		if w.Code != 400 {
			t.Fatalf("invalid patch accepted: %.100q: %d %s", body, w.Code, w.Body.String())
		}
	}
	kept, err := m.store.CodexSession(context.Background(), session.ID)
	if err != nil || kept != session {
		t.Fatalf("invalid patch changed metadata: %+v %v", kept, err)
	}
	w := metadataRequest(t, m, "PATCH", "/api/host/codex/sessions/"+session.ID, `{"revision":1,"title":"`+strings.Repeat("界", 120)+`"}`)
	if w.Code != 200 {
		t.Fatalf("120 Unicode characters rejected: %d %s", w.Code, w.Body.String())
	}
	kept, err = m.store.CodexSession(context.Background(), session.ID)
	if err != nil || kept.MetadataRevision != 2 {
		t.Fatalf("valid patch missing: %+v %v", kept, err)
	}
}

func TestMetadataLifecycleUpdatesPreserveIndependentRevision(t *testing.T) {
	m := testManager(t)
	session, _, err := m.store.ReserveCodexRun(context.Background(), storage.CodexRunInput{RequestID: "metadata", ProjectPath: "/project", HistoryHome: "/history", NamePrefix: m.namePrefix, Socket: m.socket, Mode: "new", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.UpdateCodexRun(context.Background(), session.RunID, "ended", "Codex exited."); err != nil {
		t.Fatal(err)
	}
	w := metadataRequest(t, m, "PATCH", "/api/host/codex/sessions/"+session.ID, `{"revision":1,"archived":true}`)
	var result struct {
		Session storage.CodexSession `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Session.Status != "ended" || result.Session.EndedAt == "" || result.Session.Reason != "Codex exited." || result.Session.MetadataRevision != 2 {
		t.Fatalf("archive changed ended state: %d %s %v", w.Code, w.Body.String(), err)
	}
}
