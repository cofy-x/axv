package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/filelock"
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

func TestTransfersReplayDoNotOverwriteAndPreserveSnapshots(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e, err := f.store.Create(ctx, storage.CreateInput{ParentID: "documents", Name: "Plan.md", Kind: "file", Content: "# Saved"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.store.CreateSnapshot(ctx, e.ID, e.Revision)
	if err != nil {
		t.Fatal(err)
	}
	in := projectfiles.ExportRequest{RequestID: "export-request-one", SnapshotID: d.ID, Path: "Plan.md"}
	w := f.request("POST", f.base()+"/export", in)
	expect(t, w, 200, "")
	first := w.Body.String()
	expect(t, f.request("POST", f.base()+"/export", in), 200, "")
	if got := f.request("POST", f.base()+"/export", in).Body.String(); got != first {
		t.Fatalf("export replay changed %s %s", first, got)
	}
	in.Path = "elsewhere.md"
	expect(t, f.request("POST", f.base()+"/export", in), 409, "idempotency_conflict")
	in.RequestID = "export-request-two"
	in.Path = "Plan.md"
	expect(t, f.request("POST", f.base()+"/export", in), 409, "name_conflict")
	files, err := os.ReadDir(f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if len(file.Name()) > 12 && file.Name()[:12] == ".axv-export-" {
			t.Fatalf("definitive conflict left stage: %s", file.Name())
		}
	}
	data, err := os.ReadFile(filepath.Join(f.root, "Plan.md"))
	if err != nil || string(data) != "# Saved" {
		t.Fatalf("destination changed %q %v", data, err)
	}
	im := projectfiles.ImportRequest{RequestID: "import-request-one", Path: "Plan.md", SHA256: d.SHA256}
	w = f.request("POST", f.base()+"/import", im)
	expect(t, w, 200, "")
	first = w.Body.String()
	var imported struct {
		Entry storage.Entry `json:"entry"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	w = f.request("GET", "/api/documents/"+imported.Entry.ID+"/origins", nil)
	expect(t, w, 200, "")
	var provenance struct {
		Origins []storage.DocumentOrigin `json:"origins"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &provenance); err != nil || len(provenance.Origins) != 1 || provenance.Origins[0].Path != "Plan.md" || provenance.Origins[0].SHA256 != d.SHA256 {
		t.Fatalf("import provenance %s %v", w.Body.String(), err)
	}
	if err = os.WriteFile(filepath.Join(f.root, "Plan.md"), []byte("new external bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	w = f.request("POST", f.base()+"/import", im)
	expect(t, w, 200, "")
	if w.Body.String() != first {
		t.Fatal("import replay reread mutable source")
	}
	im.RequestID = "import-request-two"
	expect(t, f.request("POST", f.base()+"/import", im), 409, "source_changed")
	if _, err = f.store.Trash(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.Delete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	in = projectfiles.ExportRequest{RequestID: "export-after-deletion", SnapshotID: d.ID, Path: "Archive.md"}
	expect(t, f.request("POST", f.base()+"/export", in), 200, "")
}

func TestDirectoryPaginationAndSnapshotHTTP(t *testing.T) {
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
	e, err := f.store.Create(context.Background(), storage.CreateInput{ParentID: "documents", Name: "Notes.md", Kind: "file", Content: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("POST", "/api/documents/"+e.ID+"/snapshots", map[string]int{"revision": e.Revision - 1}), 409, "revision_conflict")
	expect(t, f.request("POST", "/api/documents/"+e.ID+"/snapshots", map[string]int{"revision": e.Revision}), 201, "")
	w = f.request("GET", "/api/documents/"+e.ID+"/snapshots", nil)
	expect(t, w, 200, "")
	if bytes.Contains(w.Body.Bytes(), []byte(`"content"`)) {
		t.Fatal("snapshot list includes document bodies")
	}
	expect(t, f.request("POST", "/api/documents/"+e.ID+"/links", map[string]string{"kind": "project", "projectId": f.project.ID}), 201, "")
	w = f.request("GET", "/api/document-links?projectId="+f.project.ID, nil)
	expect(t, w, 200, "")
	var links struct {
		Links []storage.DocumentLink `json:"links"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &links); err != nil || len(links.Links) != 1 {
		t.Fatalf("reverse link %s %v", w.Body.String(), err)
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

func TestExportConflictReplayPreservesFailureWithoutOriginalProject(t *testing.T) {
	f := setup(t)
	snapshot := exportFixtureSnapshot(t, f)
	filename := filepath.Join(f.root, "Existing.md")
	if err := os.WriteFile(filename, []byte(snapshot.Content), 0600); err != nil {
		t.Fatal(err)
	}
	in := projectfiles.ExportRequest{RequestID: "failed-export-replay", SnapshotID: snapshot.ID, Path: "Existing.md"}
	w := f.request("POST", f.base()+"/export", in)
	expect(t, w, 409, "name_conflict")
	original := w.Body.String()
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	// A new service must honor the committed failure even after its cause goes away.
	mux := http.NewServeMux()
	Register(mux, f.store)
	f.handler = mux
	w = f.request("POST", f.base()+"/export", in)
	expect(t, w, 409, "name_conflict")
	if w.Body.String() != original {
		t.Fatalf("failure replay changed: %s", w.Body.String())
	}
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatalf("failed request was published on replay: %v", err)
	}
	if err := os.Rename(f.root, f.root+"-offline"); err != nil {
		t.Fatal(err)
	}
	w = f.request("POST", f.base()+"/export", in)
	expect(t, w, 409, "name_conflict")
	if w.Body.String() != original {
		t.Fatalf("unavailable project changed committed failure: %s", w.Body.String())
	}
}

func exportFixtureSnapshot(t *testing.T, f *fixture) storage.DocumentSnapshot {
	t.Helper()
	e, err := f.store.Create(context.Background(), storage.CreateInput{ParentID: "documents", Name: "Export.md", Kind: "file", Content: "immutable export"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.store.CreateSnapshot(context.Background(), e.ID, e.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestExportCoordinatesIndependentStoreAndAPIInstances(t *testing.T) {
	f := setup(t)
	snapshot := exportFixtureSnapshot(t, f)
	lockPath, err := f.store.DocumentTransferLockPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := storage.Open(filepath.Join(filepath.Dir(f.root), "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.Close() })
	secondMux := http.NewServeMux()
	Register(secondMux, secondStore)
	other := &fixture{store: secondStore, handler: secondMux, root: f.root, project: f.project, databaseID: f.databaseID}
	if secondLock, err := secondStore.DocumentTransferLockPath(context.Background()); err != nil || secondLock != lockPath {
		t.Fatalf("stores do not share export coordination: %q %q %v", lockPath, secondLock, err)
	}
	owner, err := filelock.TryLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	release := func() { _ = owner.Close() }
	t.Cleanup(release)
	in := projectfiles.ExportRequest{RequestID: "shared-export-request", SnapshotID: snapshot.ID, Path: "Shared.md"}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- other.request("POST", other.base()+"/export", in) }()
	select {
	case result := <-finished:
		release()
		t.Fatalf("second API bypassed active publication lock: %d %s", result.Code, result.Body)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case result := <-finished:
		expect(t, result, 200, "")
	case <-time.After(2 * time.Second):
		t.Fatal("second API did not resume after the publication owner released its lock")
	}
	expect(t, f.request("POST", f.base()+"/export", in), 200, "")
	files, err := os.ReadDir(f.root)
	if err != nil || len(files) != 1 || files[0].Name() != "Shared.md" {
		t.Fatalf("shared publication duplicated files or retained project control state: %+v %v", files, err)
	}
}

func TestIncompleteExportBodyDoesNotBlockAnotherExport(t *testing.T) {
	f := setup(t)
	snapshot := exportFixtureSnapshot(t, f)
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	r := httptest.NewRequest("POST", "http://127.0.0.1:7777"+f.base()+"/export", reader)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set(httpaccess.DatabaseIdentityHeader, f.databaseID)
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	r.Header.Set("Content-Type", "application/json")
	partialDone := make(chan struct{})
	go func() { defer close(partialDone); f.handler.ServeHTTP(httptest.NewRecorder(), r) }()
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		finished <- f.request("POST", f.base()+"/export", projectfiles.ExportRequest{RequestID: "complete-export-request", SnapshotID: snapshot.ID, Path: "Complete.md"})
	}()
	select {
	case result := <-finished:
		expect(t, result, 200, "")
	case <-time.After(2 * time.Second):
		_ = writer.Close()
		t.Fatal("an incomplete request body held export publication ownership")
	}
	_ = writer.Close()
	select {
	case <-partialDone:
	case <-time.After(time.Second):
		t.Fatal("partial request did not finish after body closed")
	}
}

func TestExplicitReplacementRegistrationPreservesOldProjectProvenance(t *testing.T) {
	f := setup(t)
	data := []byte("original project content")
	if err := os.WriteFile(filepath.Join(f.root, "README.md"), data, 0600); err != nil {
		t.Fatal(err)
	}
	w := f.request("POST", f.base()+"/import", projectfiles.ImportRequest{RequestID: "import-original-project", Path: "README.md", SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
	expect(t, w, 200, "")
	var imported struct {
		Entry storage.Entry `json:"entry"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &imported); err != nil {
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
	w = f.request("POST", "/api/projects", map[string]any{"rootPath": f.root, "name": "Replacement"})
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
	links, err := f.store.DocumentLinks(context.Background(), storage.DocumentLink{EntryID: imported.Entry.ID})
	if err != nil || len(links) != 1 || links[0].ProjectID != f.project.ID {
		t.Fatalf("old document link retargeted replacement root: %+v %v", links, err)
	}
	origins, err := f.store.DocumentOrigins(context.Background(), imported.Entry.ID)
	if err != nil || len(origins) != 1 || origins[0].ProjectID != f.project.ID {
		t.Fatalf("old import origin retargeted replacement root: %+v %v", origins, err)
	}
}
