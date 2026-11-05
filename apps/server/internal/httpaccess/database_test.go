package httpaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDatabaseIdentitySources(t *testing.T) {
	for _, tc := range []struct {
		name, method, query string
		headers             []string
		allowQuery          bool
		status              int
		code                string
	}{
		{"header", "GET", "", []string{"original"}, false, 200, ""},
		{"missing", "GET", "", nil, false, 428, "database_identity_required"},
		{"empty header", "GET", "", []string{""}, false, 400, "invalid_database_identity"},
		{"duplicate header", "GET", "", []string{"original", "original"}, false, 400, "invalid_database_identity"},
		{"combined header", "GET", "", []string{"original,original"}, false, 400, "invalid_database_identity"},
		{"whitespace", "GET", "", []string{" original"}, false, 400, "invalid_database_identity"},
		{"changed", "GET", "", []string{"other"}, false, 409, "database_changed"},
		{"query", "GET", "databaseId=original", nil, true, 200, ""},
		{"head query", "HEAD", "databaseId=original", nil, true, 200, ""},
		{"empty query", "GET", "databaseId=", nil, true, 400, "invalid_database_identity"},
		{"duplicate query", "GET", "databaseId=original&databaseId=original", nil, true, 400, "invalid_database_identity"},
		{"both sources", "GET", "databaseId=original", []string{"original"}, true, 400, "invalid_database_identity"},
		{"query unsupported", "GET", "databaseId=original", nil, false, 400, "invalid_database_identity"},
		{"mutation query", "POST", "databaseId=original", nil, true, 400, "invalid_database_identity"},
		{"malformed query", "GET", "databaseId=%xx", nil, true, 400, "invalid_database_identity"},
		{"changed query", "GET", "databaseId=other", nil, true, 409, "database_changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/?"+tc.query, nil)
			for _, header := range tc.headers {
				r.Header.Add(DatabaseIdentityHeader, header)
			}
			w := httptest.NewRecorder()
			accepted := RequireDatabaseIdentity(w, r, "original", tc.allowQuery)
			if accepted != (tc.status == http.StatusOK) || w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
				t.Fatalf("accepted %t, response %d %s", accepted, w.Code, w.Body.String())
			}
			if !accepted && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("identity failure must not be cached")
			}
		})
	}
}
