package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
	"github.com/cofy-x/axv/apps/server/internal/projectfiles"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type fixture struct {
	store      *storage.Store
	handler    http.Handler
	root       string
	project    storage.ProjectRef
	databaseID string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	root := filepath.Join(dir, "project")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, s)
	identity, err := s.DatabaseID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{store: s, handler: mux, root: root, databaseID: identity}
	r := f.request("POST", "/api/projects", map[string]any{"name": "Example", "rootPath": root})
	if r.Code != 201 {
		t.Fatalf("register: %d %s", r.Code, r.Body.String())
	}
	var result struct {
		Project storage.ProjectRef `json:"project"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	f.project, err = s.Project(context.Background(), result.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) request(method, target string, payload any) *httptest.ResponseRecorder {
	var data []byte
	if payload != nil {
		data, _ = json.Marshal(payload)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:7777"+target, bytes.NewReader(data))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set(httpaccess.DatabaseIdentityHeader, f.databaseID)
	if payload != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Origin", "http://127.0.0.1:7777")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f *fixture) base() string { return "/api/projects/" + f.project.ID }
func expect(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
	if code != "" {
		var result struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		if result.Code != code {
			t.Fatalf("code %q want %q: %s", result.Code, code, w.Body.String())
		}
	}
}

func TestProjectScopePreviewAndAccess(t *testing.T) {
	f := setup(t)
	if err := os.WriteFile(filepath.Join(f.root, "README.md"), []byte("# Project"), 0600); err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("GET", f.base()+"/file?path=README.md", nil), 200, "")
	for _, name := range []string{"../desktop.sqlite", "/etc/passwd", "folder/../../desktop.sqlite", "a\\b"} {
		expect(t, f.request("GET", f.base()+"/file?path="+url.QueryEscape(name), nil), 400, "invalid_path")
	}
	if err := os.Symlink(filepath.Dir(f.root), filepath.Join(f.root, "outside")); err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("GET", f.base()+"/file?path=outside/desktop.sqlite", nil), 503, "file_unavailable")
	if err := os.Symlink("README.md", filepath.Join(f.root, "link.md")); err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("GET", f.base()+"/file?path=link.md", nil), 400, "not_file")
	if err := os.WriteFile(filepath.Join(f.root, "binary"), []byte{0, 1, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("GET", f.base()+"/file?path=binary", nil), 400, "not_text")
	w := f.request("GET", f.base()+"/download?path=binary", nil)
	expect(t, w, 200, "")
	if !bytes.Equal(w.Body.Bytes(), []byte{0, 1, 2}) || w.Header().Get("Content-Disposition") == "" {
		t.Fatal("binary download changed bytes or disposition")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:7777/api/projects", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	expect(t, w, 403, "host_forbidden")
	if err := os.Rename(f.root, f.root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.root, 0700); err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("GET", f.base()+"/files", nil), 409, "project_changed")
}

func TestProjectFilesHideReservedStagingPaths(t *testing.T) {
	f := setup(t)
	const visible = "README.md"
	const staged = ".axv-export-pending"
	for _, name := range []string{visible, staged} {
		if err := os.WriteFile(filepath.Join(f.root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w := f.request("GET", f.base()+"/files", nil)
	expect(t, w, http.StatusOK, "")
	var listing projectfiles.Listing
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != visible {
		t.Fatalf("unexpected project listing: %+v", listing.Entries)
	}
	for _, route := range []string{"file", "download"} {
		target := f.base() + "/" + route + "?path="
		expect(t, f.request("GET", target+visible, nil), http.StatusOK, "")
		expect(t, f.request("GET", target+staged, nil), http.StatusBadRequest, "invalid_path")
	}
	data, err := os.ReadFile(filepath.Join(f.root, staged))
	if err != nil || string(data) != staged {
		t.Fatalf("staging file changed: %q %v", data, err)
	}
}

func TestDirectoryPagination(t *testing.T) {
	f := setup(t)
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(f.root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w := f.request("GET", f.base()+"/files?limit=2", nil)
	expect(t, w, 200, "")
	var page struct {
		Entries []projectfiles.Entry `json:"entries"`
		Next    string               `json:"nextCursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Entries) != 2 || page.Next != "2" {
		t.Fatalf("first page %s %v", w.Body.String(), err)
	}
	w = f.request("GET", f.base()+"/files?limit=2&cursor="+page.Next, nil)
	expect(t, w, 200, "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Entries) != 1 || page.Entries[0].Name != "c.md" {
		t.Fatalf("last page %s %v", w.Body.String(), err)
	}
}

func TestDirectoryQueryValidation(t *testing.T) {
	f := setup(t)
	for _, tc := range []struct {
		query string
		code  string
	}{
		{"limit=", "invalid_request"},
		{"limit=0", "invalid_request"},
		{"limit=-1", "invalid_request"},
		{"limit=201", "invalid_request"},
		{"limit=invalid", "invalid_request"},
		{"limit=999999999999999999999999999999", "invalid_request"},
		{"limit=1&limit=2", "invalid_request"},
		{"unexpected=1", "invalid_request"},
		{"cursor=invalid", "invalid_cursor"},
		{"path=..&limit=invalid", "invalid_path"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			expect(t, f.request("GET", f.base()+"/files?"+tc.query, nil), 400, tc.code)
		})
	}
	w := f.request("GET", f.base()+"/files?limit=200", nil)
	expect(t, w, 200, "")
	var result projectfiles.Listing
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Project.ID != f.project.ID || result.Entries == nil || len(result.Entries) != 0 || result.NextCursor != "" || result.Truncated {
		t.Fatalf("empty directory contract changed: %s", w.Body.String())
	}
}

func TestExplicitReplacementRegistrationPreservesOldProjectReference(t *testing.T) {
	f := setup(t)
	data := []byte("original project content")
	if err := os.WriteFile(filepath.Join(f.root, "README.md"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.root, f.root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "README.md"), []byte("replacement project content"), 0600); err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("GET", f.base()+"/file?path=README.md", nil), 409, "project_changed")
	w := f.request("POST", "/api/projects", map[string]any{"rootPath": f.root, "name": "Replacement"})
	expect(t, w, 201, "")
	var registered struct {
		Project storage.ProjectRef `json:"project"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	if registered.Project.ID == f.project.ID {
		t.Fatal("replacement directory silently inherited the old project's identity")
	}
	newID := registered.Project.ID
	w = f.request("POST", "/api/projects", map[string]any{"rootPath": f.root})
	expect(t, w, 201, "")
	if err := json.Unmarshal(w.Body.Bytes(), &registered); err != nil || registered.Project.ID != newID {
		t.Fatalf("same replacement directory did not reuse its own reference: %s %v", w.Body, err)
	}
	w = f.request("GET", "/api/projects/"+newID+"/file?path=README.md", nil)
	expect(t, w, 200, "")
	if !bytes.Contains(w.Body.Bytes(), []byte("replacement project content")) {
		t.Fatal("new project reference did not read its replacement root")
	}
	expect(t, f.request("GET", f.base()+"/file?path=README.md", nil), 409, "project_changed")
	original, err := f.store.Project(context.Background(), f.project.ID)
	if err != nil || original.RootIdentity != f.project.RootIdentity {
		t.Fatalf("old project reference was changed: %+v %v", original, err)
	}
}
