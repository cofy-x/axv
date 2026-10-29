package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
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

func readEntry(t *testing.T, w *httptest.ResponseRecorder) storage.Entry {
	t.Helper()
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var value struct{ Entry storage.Entry }
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value.Entry
}

func TestHTTPFileLifecycle(t *testing.T) {
	h := testAPI(t)
	folder := readEntry(t, request(t, h, "POST", "/api/entries", map[string]any{"parentId": "documents", "name": "Work", "kind": "folder"}))
	e := readEntry(t, request(t, h, "POST", "/api/entries", map[string]any{"parentId": folder.ID, "name": "Hello.txt", "kind": "file", "content": "Initial"}))
	updated := readEntry(t, request(t, h, "PATCH", "/api/entries/"+e.ID, map[string]any{"content": "Saved", "revision": e.Revision}))
	conflict := request(t, h, "PATCH", "/api/entries/"+e.ID, map[string]any{"content": "Stale", "revision": e.Revision})
	if conflict.Code != 409 || !strings.Contains(conflict.Body.String(), "revision_conflict") {
		t.Fatalf("expected conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	readEntry(t, request(t, h, "PATCH", "/api/entries/"+e.ID, map[string]any{"parentId": "desktop", "name": "Moved.txt", "revision": updated.Revision}))
	readEntry(t, request(t, h, "POST", "/api/entries/"+e.ID+"/trash", nil))
	readEntry(t, request(t, h, "POST", "/api/entries/"+e.ID+"/restore", nil))
	download := request(t, h, "GET", "/api/entries/"+updated.ID+"/content?download=1", nil)
	if download.Code != 200 || download.Body.String() != "Saved" || !strings.Contains(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("bad download: %d %s", download.Code, download.Body.String())
	}
	if w := request(t, h, "DELETE", "/api/entries/"+e.ID, nil); w.Code != 409 {
		t.Fatal("permanently deleted a live file")
	}
	readEntry(t, request(t, h, "POST", "/api/entries/"+e.ID+"/trash", nil))
	if w := request(t, h, "DELETE", "/api/entries/"+e.ID, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(t, h, "GET", "/api/entries/"+e.ID, nil); w.Code != 404 {
		t.Fatal("deleted item remained")
	}
}

func TestHTTPUploadSniffsContentAndLimits(t *testing.T) {
	h := testAPI(t)
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name              string
		data              []byte
		preview, editable bool
		code              int
	}{
		{"Picture.dat", pngData.Bytes(), true, false, 201},
		{"False.png", []byte("<html><script>alert(1)</script></html>"), false, true, 201},
		{"Invalid.txt", []byte{0xff, 0xfe, 0x80}, false, false, 201},
		{"Huge.bin", make([]byte, storage.MaxFileSize+1), false, false, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("files", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = part.Write(tc.data); err != nil {
				t.Fatal(err)
			}
			if err = writer.WriteField("parentId", "desktop"); err != nil {
				t.Fatal(err)
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/api/import", &body)
			r.Header.Set(httpaccess.DatabaseIdentityHeader, h.databaseID)
			r.Header.Set("Content-Type", writer.FormDataContentType())
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			if w.Code != 201 {
				return
			}
			var result struct{ Entries []storage.Entry }
			if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			e := result.Entries[0]
			if e.Previewable != tc.preview || e.Editable != tc.editable || e.ParentID != "desktop" {
				t.Fatalf("wrong imported metadata: %+v", e)
			}
			download := request(t, h, "GET", "/api/entries/"+e.ID+"/content", nil)
			if !bytes.Equal(download.Body.Bytes(), tc.data) {
				t.Fatal("content altered")
			}
			want := "attachment"
			if tc.preview {
				want = "inline"
			}
			if !strings.Contains(download.Header().Get("Content-Disposition"), want) {
				t.Fatal("unsafe content disposition")
			}
		})
	}
}

func TestHTTPValidationAndSettings(t *testing.T) {
	h := testAPI(t)
	for _, path := range []string{"/api/health", "/api/bootstrap?clientId=test", "/api/entries"} {
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
	r := httptest.NewRequest("POST", "/api/entries", strings.NewReader(`{"name":"bad"}`))
	r.Header.Set(httpaccess.DatabaseIdentityHeader, h.databaseID)
	r.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("accepted incorrect content type")
	}
	r = httptest.NewRequest("DELETE", "http://localhost:3001/api/trash", nil)
	r.Header.Set(httpaccess.DatabaseIdentityHeader, h.databaseID)
	r.Header.Set("Origin", "https://malicious.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("accepted external origin mutation")
	}
	r = httptest.NewRequest("DELETE", "http://localhost:3001/api/trash", nil)
	r.Header.Set(httpaccess.DatabaseIdentityHeader, h.databaseID)
	r.Header.Set("Origin", "http://localhost:5173")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("rejected loopback development origin")
	}
	r = httptest.NewRequest("POST", "/api/entries", strings.NewReader(fmt.Sprintf(`{"parentId":"documents","name":"a","kind":"file"} %s`, "{}")))
	r.Header.Set(httpaccess.DatabaseIdentityHeader, h.databaseID)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("accepted trailing JSON")
	}
}

func TestHTTPStorageErrorContract(t *testing.T) {
	h := testAPI(t)
	folder := readEntry(t, request(t, h, "POST", "/api/entries", map[string]any{
		"parentId": "documents", "name": "Work", "kind": "folder",
	}))
	file := readEntry(t, request(t, h, "POST", "/api/entries", map[string]any{
		"parentId": "documents", "name": "Draft.txt", "kind": "file",
	}))
	cases := []struct {
		code, method, path string
		body               any
		status             int
	}{
		{"not_found", "GET", "/api/entries/missing", nil, http.StatusNotFound},
		{"invalid_name", "POST", "/api/entries", map[string]any{
			"parentId": "documents", "name": "../invalid", "kind": "file",
		}, http.StatusBadRequest},
		{"invalid_parent", "POST", "/api/entries", map[string]any{
			"parentId": file.ID, "name": "Child.txt", "kind": "file",
		}, http.StatusBadRequest},
		{"invalid_kind", "POST", "/api/entries", map[string]any{
			"parentId": "documents", "name": "Invalid", "kind": "other",
		}, http.StatusBadRequest},
		{"invalid_content", "POST", "/api/entries", map[string]any{
			"parentId": "documents", "name": "Invalid", "kind": "folder", "content": "text",
		}, http.StatusBadRequest},
		{"folder_cycle", "PATCH", "/api/entries/" + folder.ID, map[string]any{
			"parentId": folder.ID, "revision": folder.Revision,
		}, http.StatusBadRequest},
		{"not_editable", "PATCH", "/api/entries/" + folder.ID, map[string]any{
			"content": "text", "revision": folder.Revision,
		}, http.StatusBadRequest},
		{"invalid_text", "PATCH", "/api/entries/" + file.ID, map[string]any{
			"content": "\x00", "revision": file.Revision,
		}, http.StatusBadRequest},
		{"invalid_wallpaper", "PUT", "/api/preferences", map[string]any{
			"wallpaper": "unknown", "iconSnap": true, "clock24Hour": true,
		}, http.StatusBadRequest},
		{"invalid_client", "PUT", "/api/layout", map[string]any{
			"clientId": "", "layout": map[string]any{},
		}, http.StatusBadRequest},
		{"invalid_layout", "PUT", "/api/layout", map[string]any{
			"clientId": "test", "layout": []string{},
		}, http.StatusBadRequest},
		{"name_conflict", "POST", "/api/entries", map[string]any{
			"parentId": "documents", "name": file.Name, "kind": "file",
		}, http.StatusConflict},
		{"unavailable", "PATCH", "/api/entries/root", map[string]any{
			"name": "Renamed",
		}, http.StatusConflict},
		{"revision_conflict", "PATCH", "/api/entries/" + file.ID, map[string]any{
			"content": "stale", "revision": 0,
		}, http.StatusConflict},
		{"system_entry", "POST", "/api/entries/root/trash", nil, http.StatusConflict},
		{"not_trashed", "DELETE", "/api/entries/" + file.ID, nil, http.StatusConflict},
		{"text_too_large", "PATCH", "/api/entries/" + file.ID, map[string]any{
			"content": strings.Repeat("a", storage.MaxTextSize+1), "revision": file.Revision,
		}, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			w := request(t, h, tc.method, tc.path, tc.body)
			if w.Code != tc.status {
				t.Fatalf("want HTTP %d, got %d: %s", tc.status, w.Code, w.Body.String())
			}
			var body struct {
				Code  string `json:"code"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.code || body.Error == "" {
				t.Fatalf("want %s and a readable message, got %+v", tc.code, body)
			}
		})
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
