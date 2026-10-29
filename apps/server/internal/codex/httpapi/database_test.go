package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) {
	b.t.Fatal("database rejection read the request body")
	return 0, nil
}

func TestDatabaseReplacementRejectsCodexBeforeExecution(t *testing.T) {
	_, original := testHandler(t)
	handler, replacement := testHandler(t)
	mux := http.NewServeMux()
	handler.Register(mux)
	for _, route := range []struct{ method, path string }{
		{"GET", "/projects"}, {"GET", "/sessions"}, {"POST", "/sessions"},
		{"GET", "/sessions/original"}, {"PATCH", "/sessions/original"},
		{"POST", "/sessions/original/resume"}, {"GET", "/sessions/original/runs/original"},
		{"POST", "/sessions/original/runs/original/activity/review"},
		{"GET", "/sessions/original/runs/original/context"},
		{"POST", "/sessions/original/runs/original/context"},
		{"DELETE", "/sessions/original/runs/original/context/batch"},
		{"POST", "/sessions/original/runs/original/end"},
		{"GET", "/sessions/original/runs/original/terminal"},
	} {
		for _, id := range []string{"", original.DatabaseID()} {
			t.Run(route.method+route.path+id, func(t *testing.T) {
				r := httptest.NewRequest(route.method, "http://localhost:3001/api/host/codex"+route.path, unreadBody{t})
				r.RemoteAddr = "127.0.0.1:3456"
				r.Header.Set("Origin", "http://localhost:3001")
				status, code := 428, "database_identity_required"
				if id != "" {
					r.Header.Set(httpaccess.DatabaseIdentityHeader, id)
					status, code = 409, "database_changed"
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != status || !strings.Contains(w.Body.String(), code) {
					t.Fatalf("response %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
	if sessions, err := replacement.List(context.Background()); err != nil || len(sessions) != 0 {
		t.Fatalf("stale requests reserved a replacement run: %v %v", sessions, err)
	}
}

func TestCodexWebSocketDatabaseIdentityPrecedesAttachment(t *testing.T) {
	handler, manager := testHandler(t)
	mux := http.NewServeMux()
	handler.Register(mux)
	for _, tc := range []struct {
		query, header string
		status        int
		code          string
	}{
		{"databaseId=other&cols=80&rows=24", "", 409, "database_changed"},
		{"databaseId=", "", 400, "invalid_database_identity"},
		{"databaseId=other&databaseId=other", "", 400, "invalid_database_identity"},
		{"databaseId=" + manager.DatabaseID(), manager.DatabaseID(), 400, "invalid_database_identity"},
		{"databaseId=" + manager.DatabaseID() + "&cols=80&rows=24", "", 404, "not_found"},
		{"databaseId=" + manager.DatabaseID() + "&cols=0", "", 400, "invalid_terminal_size"},
	} {
		r := httptest.NewRequest("GET", "http://localhost:3001/api/host/codex/sessions/original/runs/original/terminal?"+tc.query, nil)
		r.RemoteAddr = "127.0.0.1:3456"
		r.Header.Set("Origin", "http://localhost:3001")
		if tc.header != "" {
			r.Header.Set(httpaccess.DatabaseIdentityHeader, tc.header)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("query %s: %d %s", tc.query, w.Code, w.Body.String())
		}
	}
}
