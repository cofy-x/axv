package httpaccess

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const DatabaseIdentityHeader = "X-AXV-Database-ID"

// RequireDatabaseIdentity binds a request to the store captured by its owner.
// Call this after access checks and before reading a body or touching resources.
// Only browser content navigation and WebSocket upgrades allow query identity.
func RequireDatabaseIdentity(w http.ResponseWriter, r *http.Request, current string, allowQuery bool) bool {
	fail := func(status int, code, message string) bool {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "error": message})
		return false
	}
	invalid := func() bool {
		return fail(http.StatusBadRequest, "invalid_database_identity", "Provide one nonempty desktop database identity using the supported request source.")
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return invalid()
	}
	headers := r.Header.Values(DatabaseIdentityHeader)
	values, inQuery := query["databaseId"]
	if inQuery && (!allowQuery || (r.Method != http.MethodGet && r.Method != http.MethodHead) || len(headers) != 0) {
		return invalid()
	}
	if inQuery {
		headers = values
	}
	if len(headers) == 0 {
		return fail(http.StatusPreconditionRequired, "database_identity_required", "This page must identify its original AXV desktop database. Keep it open to preserve unsaved work and open AXV in a new window.")
	}
	if len(headers) != 1 {
		return invalid()
	}
	expected := headers[0]
	if expected == "" || len(expected) > 128 || !utf8.ValidString(expected) || strings.TrimSpace(expected) != expected || strings.ContainsAny(expected, ",\x00\r\n") {
		return invalid()
	}
	if expected != current {
		return fail(http.StatusConflict, "database_changed", "The AXV desktop database changed. Keep this page open to preserve unsaved work and open AXV in a new window to use the current database.")
	}
	return true
}
