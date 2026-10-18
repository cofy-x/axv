package projectfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type fixture struct {
	store   *storage.Store
	handler http.Handler
	root    string
	project storage.ProjectRef
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
	f := &fixture{store: s, handler: mux, root: root}
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
	in := exportRequest{RequestID: "export-request-one", SnapshotID: d.ID, Path: "Plan.md"}
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
	im := importRequest{RequestID: "import-request-one", Path: "Plan.md", SHA256: d.SHA256}
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
	in = exportRequest{RequestID: "export-after-deletion", SnapshotID: d.ID, Path: "Archive.md"}
	expect(t, f.request("POST", f.base()+"/export", in), 200, "")
}

func TestExportRecoversPublicationBeforeReceipt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e, err := f.store.Create(ctx, storage.CreateInput{ParentID: "documents", Name: "Plan.md", Kind: "file", Content: "durable"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.store.CreateSnapshot(ctx, e.ID, e.Revision)
	if err != nil {
		t.Fatal(err)
	}
	in := exportRequest{RequestID: "interrupted-request", SnapshotID: d.ID, Path: "Recovered.md"}
	tx := storage.DocumentTransfer{RequestID: in.RequestID, PayloadHash: payloadHash("export", f.project.ID, in), Kind: "export", ProjectID: f.project.ID, Path: in.Path, SHA256: d.SHA256, ParentIdentity: exportTargetIdentity(t, f, in.Path)}
	tx, err = f.store.ReserveExport(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.root, tx.StagePath), []byte(d.Content), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(filepath.Join(f.root, tx.StagePath), filepath.Join(f.root, in.Path)); err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("POST", f.base()+"/export", in), 200, "")
	if _, err = os.Stat(filepath.Join(f.root, tx.StagePath)); !os.IsNotExist(err) {
		t.Fatalf("completed staging link remains: %v", err)
	}
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
		Entries []fileEntry `json:"entries"`
		Next    string      `json:"nextCursor"`
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

func exportTargetIdentity(t *testing.T, f *fixture, name string) string {
	t.Helper()
	root, err := openProject(f.project)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	parent, identity, err := exportParent(root, name)
	if err != nil {
		t.Fatal(err)
	}
	_ = parent.Close()
	return identity
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
	other := &fixture{store: secondStore, handler: secondMux, root: f.root, project: f.project}
	if secondLock, err := secondStore.DocumentTransferLockPath(context.Background()); err != nil || secondLock != lockPath {
		t.Fatalf("stores do not share export coordination: %q %q %v", lockPath, secondLock, err)
	}
	owner := &API{store: f.store}
	release, err := owner.lockExports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	in := exportRequest{RequestID: "shared-export-request", SnapshotID: snapshot.ID, Path: "Shared.md"}
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
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	r.Header.Set("Content-Type", "application/json")
	partialDone := make(chan struct{})
	go func() { defer close(partialDone); f.handler.ServeHTTP(httptest.NewRecorder(), r) }()
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		finished <- f.request("POST", f.base()+"/export", exportRequest{RequestID: "complete-export-request", SnapshotID: snapshot.ID, Path: "Complete.md"})
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

func TestExportReplayFinishesCleanupAfterReceiptCommit(t *testing.T) {
	f := setup(t)
	snapshot := exportFixtureSnapshot(t, f)
	in := exportRequest{RequestID: "receipt-before-cleanup", SnapshotID: snapshot.ID, Path: "Committed.md"}
	request := storage.DocumentTransfer{RequestID: in.RequestID, PayloadHash: payloadHash("export", f.project.ID, in), Kind: "export", ProjectID: f.project.ID, Path: in.Path, SHA256: snapshot.SHA256, ParentIdentity: exportTargetIdentity(t, f, in.Path)}
	request, err := f.store.ReserveExport(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(f.root, request.StagePath)
	if err := os.WriteFile(stage, []byte(snapshot.Content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(stage, filepath.Join(f.root, in.Path)); err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(map[string]any{"path": in.Path, "sha256": snapshot.SHA256, "snapshotId": snapshot.ID, "projectId": f.project.ID})
	if err := f.store.CompleteExport(context.Background(), request, result); err != nil {
		t.Fatal(err)
	}
	// A crash here leaves the committed receipt and its retained staging link.
	expect(t, f.request("POST", f.base()+"/export", in), 200, "")
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("replay left committed recovery anchor: %v", err)
	}
}

func TestExplicitReplacementRegistrationPreservesOldProjectProvenance(t *testing.T) {
	f := setup(t)
	data := []byte("original project content")
	if err := os.WriteFile(filepath.Join(f.root, "README.md"), data, 0600); err != nil {
		t.Fatal(err)
	}
	w := f.request("POST", f.base()+"/import", importRequest{RequestID: "import-original-project", Path: "README.md", SHA256: digest(data)})
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

func TestInterruptedExportNeverReplaysIntoReplacementParentDirectory(t *testing.T) {
	f := setup(t)
	snapshot := exportFixtureSnapshot(t, f)
	parentPath := filepath.Join(f.root, "nested")
	if err := os.Mkdir(parentPath, 0700); err != nil {
		t.Fatal(err)
	}
	in := exportRequest{RequestID: "interrupted-parent-export", SnapshotID: snapshot.ID, Path: "nested/Report.md"}
	request := storage.DocumentTransfer{RequestID: in.RequestID, PayloadHash: payloadHash("export", f.project.ID, in), Kind: "export", ProjectID: f.project.ID, Path: in.Path, SHA256: snapshot.SHA256, ParentIdentity: exportTargetIdentity(t, f, in.Path)}
	request, err := f.store.ReserveExport(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(f.root, request.StagePath)
	if err := os.WriteFile(stage, []byte(snapshot.Content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(stage, filepath.Join(parentPath, "Report.md")); err != nil {
		t.Fatal(err)
	}
	// Publication happened before a crash; the destination's ancestor was
	// subsequently moved and replaced while the same project root survived.
	if err := os.Rename(parentPath, parentPath+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parentPath, 0700); err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("POST", f.base()+"/export", in), 409, "transfer_unresolved")
	if _, err := os.Stat(filepath.Join(parentPath, "Report.md")); !os.IsNotExist(err) {
		t.Fatalf("retry published into a replacement parent: %v", err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("unresolved retry removed its recovery anchor: %v", err)
	}
	stored, found, err := f.store.DocumentTransfer(context.Background(), in.RequestID, request.PayloadHash)
	if err != nil || !found || len(stored.Result) != 0 || stored.ParentIdentity != request.ParentIdentity {
		t.Fatalf("unresolved retry changed its reserved target: %+v %v %v", stored, found, err)
	}
	if err := os.Remove(parentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parentPath+"-original", parentPath); err != nil {
		t.Fatal(err)
	}
	expect(t, f.request("POST", f.base()+"/export", in), 200, "")
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("recovered request retained its staging link: %v", err)
	}
}

func TestExportLinkUsesCapturedParentHandleAcrossRename(t *testing.T) {
	f := setup(t)
	parentPath := filepath.Join(f.root, "nested")
	if err := os.Mkdir(parentPath, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := openProject(f.project)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	parent, identity, err := exportParent(root, "nested/Report.md")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	sourceDirectory, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDirectory.Close()
	destinationDirectory, err := parent.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer destinationDirectory.Close()
	stage := ".axv-export-owned-stage"
	if err := root.WriteFile(stage, []byte("captured parent"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parentPath, parentPath+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parentPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := linkDirectories(sourceDirectory, stage, destinationDirectory, "Report.md"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(parentPath+"-original", "Report.md")); err != nil || string(data) != "captured parent" {
		t.Fatalf("publication left its captured parent handle: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(parentPath, "Report.md")); !os.IsNotExist(err) {
		t.Fatalf("publication followed a replacement parent path: %v", err)
	}
	var failure *storage.Error
	if err := verifyExportParent(root, storage.DocumentTransfer{Path: "nested/Report.md", ParentIdentity: identity}); !errors.As(err, &failure) || failure.Code != "transfer_unresolved" {
		t.Fatalf("renamed parent was falsely acknowledged at its old path: %v", err)
	}
}
