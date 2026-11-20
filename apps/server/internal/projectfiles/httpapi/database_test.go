package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) {
	b.t.Fatal("database rejection read the request body")
	return 0, nil
}

func TestDatabaseReplacementRejectsProjectOperations(t *testing.T) {
	original, replacement := setup(t), setup(t)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/projects"}, {"POST", "/api/projects"},
		{"GET", replacement.base() + "/files"}, {"GET", replacement.base() + "/file?path=secret"},
		{"GET", replacement.base() + "/download?path=secret"},
	} {
		for _, id := range []string{"", original.databaseID} {
			t.Run(route.method+route.path+id, func(t *testing.T) {
				r := httptest.NewRequest(route.method, "http://127.0.0.1:7777"+route.path, unreadBody{t})
				r.RemoteAddr = "127.0.0.1:12345"
				r.Header.Set("Origin", "http://127.0.0.1:7777")
				status, code := 428, "database_identity_required"
				if id != "" {
					r.Header.Set(httpaccess.DatabaseIdentityHeader, id)
					status, code = 409, "database_changed"
				}
				w := httptest.NewRecorder()
				replacement.handler.ServeHTTP(w, r)
				expect(t, w, status, code)
			})
		}
	}
	if entries, err := os.ReadDir(replacement.root); err != nil || len(entries) != 0 {
		t.Fatalf("stale request modified host project: %v %v", entries, err)
	}
	projects, err := replacement.store.Projects(context.Background())
	if err != nil || len(projects) != 1 || projects[0].ID != replacement.project.ID {
		t.Fatalf("stale requests changed project registrations: %+v %v", projects, err)
	}
	// Access errors take precedence over the missing identity precondition.
	r := httptest.NewRequest("GET", "http://127.0.0.1:7777/api/projects", nil)
	r.RemoteAddr = "203.0.113.1:12345"
	w := httptest.NewRecorder()
	replacement.handler.ServeHTTP(w, r)
	expect(t, w, http.StatusForbidden, "host_forbidden")
}
