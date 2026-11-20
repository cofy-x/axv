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
		t.Fatal("stale requests changed replacement preferences or layout")
	}
}

func TestDesktopHealthChecksCapturedDatabaseWhenProvided(t *testing.T) {
	h := testAPI(t)
	for _, tc := range []struct {
		name string
		path string
		ids  []string
		want int
	}{
		{"launcher", "/api/health", nil, http.StatusOK},
		{"browser", "/api/health", []string{h.databaseID}, http.StatusOK},
		{"replacement", "/api/health", []string{"replaced"}, http.StatusConflict},
		{"multiple identities", "/api/health", []string{h.databaseID, h.databaseID}, http.StatusBadRequest},
		{"query identity", "/api/health?databaseId=" + h.databaseID, nil, http.StatusBadRequest},
		{"malformed query identity", "/api/health?databaseId=%", nil, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			for _, id := range tc.ids {
				r.Header.Add(httpaccess.DatabaseIdentityHeader, id)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || tc.want == http.StatusConflict && !strings.Contains(w.Body.String(), `"database_changed"`) {
				t.Fatalf("response %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, path := range []string{"/api/preferences", "/api/layout"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, path+"?databaseId="+h.databaseID, unreadBody{t}))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("query identity accepted by %s: %d", path, w.Code)
		}
	}
}
