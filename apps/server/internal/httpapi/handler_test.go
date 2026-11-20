package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type apiFixture struct {
	http.Handler
	databaseID string
}

func testAPI(t *testing.T) *apiFixture {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	identity, err := s.DatabaseID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return &apiFixture{Handler: New(s, http.NotFoundHandler()), databaseID: identity}
}

func request(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	r := httptest.NewRequest(method, path, reader)
	if f, ok := h.(*apiFixture); ok {
		r.Header.Set(httpaccess.DatabaseIdentityHeader, f.databaseID)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHTTPValidationAndSettings(t *testing.T) {
	h := testAPI(t)
	for _, path := range []string{"/api/health", "/api/bootstrap?clientId=test"} {
		if w := request(t, h, "GET", path, nil); w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := request(t, h, "GET", "/api/missing", nil); w.Code != 404 {
		t.Fatal("missing API fell through to UI")
	}
	if w := request(t, h, "PUT", "/api/preferences", map[string]any{"wallpaper": "dither", "iconSnap": false, "clock24Hour": true}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(t, h, "PUT", "/api/layout", map[string]any{"clientId": "test", "layout": map[string]any{"windows": []any{}}}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, layout := range []any{nil, "invalid", []string{"invalid"}} {
		if w := request(t, h, "PUT", "/api/layout", map[string]any{"clientId": "test", "layout": layout}); w.Code != 400 {
			t.Fatalf("accepted invalid layout: %d", w.Code)
		}
	}
	w := request(t, h, "PUT", "/api/preferences", map[string]any{"wallpaper": "unknown", "iconSnap": true, "clock24Hour": true})
	if w.Code != 400 {
		t.Fatalf("invalid wallpaper: %s", w.Body.String())
	}
	r := httptest.NewRequest("PUT", "/api/preferences", strings.NewReader(`{"wallpaper":"dither"}`))
	r.Header.Set(httpaccess.DatabaseIdentityHeader, h.databaseID)
	r.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("accepted incorrect content type")
	}
	r = httptest.NewRequest("PUT", "http://localhost:3001/api/preferences", strings.NewReader(`{"wallpaper":"dither"}`))
	r.Header.Set(httpaccess.DatabaseIdentityHeader, h.databaseID)
	r.Header.Set("Origin", "https://malicious.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("accepted external origin mutation")
	}
	r = httptest.NewRequest("PUT", "http://localhost:3001/api/preferences", strings.NewReader(`{"wallpaper":"dither"}`))
	r.Header.Set(httpaccess.DatabaseIdentityHeader, h.databaseID)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost:5173")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("rejected loopback development origin")
	}
	r = httptest.NewRequest("PUT", "/api/preferences", strings.NewReader(`{"wallpaper":"dither"} {}`))
	r.Header.Set(httpaccess.DatabaseIdentityHeader, h.databaseID)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("accepted trailing JSON")
	}
}

func TestHTTPInternalErrorsArePrivate(t *testing.T) {
	s, err := storage.Open(filepath.Join(t.TempDir(), "closed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	h := New(s, http.NotFoundHandler())
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	w := request(t, h, "GET", "/api/health", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want HTTP 500, got %d", w.Code)
	}
	var body struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "internal_error" || body.Error != "The operation could not be saved. Please try again." {
		t.Fatalf("internal database failure exposed: %+v", body)
	}
}
