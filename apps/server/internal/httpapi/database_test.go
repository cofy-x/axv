package httpapi

import (
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

func TestDatabaseReplacementRejectsEveryDesktopResource(t *testing.T) {
	original, replacement := testAPI(t), testAPI(t)
	before := request(t, replacement, "GET", "/api/bootstrap?clientId=original", nil).Body.String()
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/entries"}, {"POST", "/api/entries"},
		{"GET", "/api/entries/readme"}, {"PATCH", "/api/entries/readme"},
		{"GET", "/api/entries/readme/content"}, {"HEAD", "/api/entries/readme/content"},
		{"POST", "/api/entries/readme/duplicate"}, {"POST", "/api/entries/readme/trash"},
		{"POST", "/api/entries/readme/restore"}, {"DELETE", "/api/entries/readme"},
		{"DELETE", "/api/trash"}, {"POST", "/api/import"},
		{"PUT", "/api/preferences"}, {"PUT", "/api/layout"},
	} {
		for _, id := range []string{"", original.databaseID} {
			t.Run(route.method+route.path+id, func(t *testing.T) {
				r := httptest.NewRequest(route.method, route.path, unreadBody{t})
				want, code := http.StatusPreconditionRequired, "database_identity_required"
				if id != "" {
					r.Header.Set(httpaccess.DatabaseIdentityHeader, id)
					want, code = http.StatusConflict, "database_changed"
				}
				w := httptest.NewRecorder()
				replacement.ServeHTTP(w, r)
				if w.Code != want || !strings.Contains(w.Body.String(), code) {
					t.Fatalf("response %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
	if after := request(t, replacement, "GET", "/api/bootstrap?clientId=original", nil).Body.String(); after != before {
		t.Fatal("stale requests changed replacement entries, preferences or layout")
	}
}

func TestContentNavigationRequiresItsOriginalDatabase(t *testing.T) {
	h := testAPI(t)
	for _, method := range []string{"GET", "HEAD"} {
		for _, id := range []string{h.databaseID, "replaced"} {
			r := httptest.NewRequest(method, "/api/entries/readme/content?preview=1&databaseId="+id, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := http.StatusOK
			if id != h.databaseID {
				want = http.StatusConflict
			}
			if w.Code != want {
				t.Fatalf("content %s: %d %s", method, w.Code, w.Body.String())
			}
		}
	}
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/entries"}, {"PUT", "/api/layout"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(route.method, route.path+"?databaseId="+h.databaseID, unreadBody{t}))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("query identity accepted by %s: %d", route.path, w.Code)
		}
	}
}
