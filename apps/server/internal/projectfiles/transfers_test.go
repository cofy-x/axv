package projectfiles

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type transferFixture struct {
	store   *storage.Store
	service *Service
	root    string
	project storage.ProjectRef
}

func setupTransfer(t *testing.T) *transferFixture {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	root := filepath.Join(dir, "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	service := New(store)
	project, err := service.Register(context.Background(), "Example", root)
	if err != nil {
		t.Fatal(err)
	}
	return &transferFixture{store: store, service: service, root: root, project: project}
}

func (f *transferFixture) export(t *testing.T, in ExportRequest, code string) {
	t.Helper()
	_, err := f.service.Export(context.Background(), f.project.ID, in)
	if code == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var failure *storage.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("export error %v, want %s", err, code)
	}
}

func exportFixtureSnapshot(t *testing.T, f *transferFixture) storage.DocumentSnapshot {
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

func exportTargetIdentity(t *testing.T, f *transferFixture, name string) string {
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

func TestExportRecoversPublicationBeforeReceipt(t *testing.T) {
	f := setupTransfer(t)
	ctx := context.Background()
	e, err := f.store.Create(ctx, storage.CreateInput{ParentID: "documents", Name: "Plan.md", Kind: "file", Content: "durable"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.store.CreateSnapshot(ctx, e.ID, e.Revision)
	if err != nil {
		t.Fatal(err)
	}
	in := ExportRequest{RequestID: "interrupted-request", SnapshotID: d.ID, Path: "Recovered.md"}
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
	f.export(t, in, "")
	if _, err = os.Stat(filepath.Join(f.root, tx.StagePath)); !os.IsNotExist(err) {
		t.Fatalf("completed staging link remains: %v", err)
	}
}

func TestExportReplayFinishesCleanupAfterReceiptCommit(t *testing.T) {
	f := setupTransfer(t)
	snapshot := exportFixtureSnapshot(t, f)
	in := ExportRequest{RequestID: "receipt-before-cleanup", SnapshotID: snapshot.ID, Path: "Committed.md"}
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
	f.export(t, in, "")
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("replay left committed recovery anchor: %v", err)
	}
}

func TestInterruptedExportNeverReplaysIntoReplacementParentDirectory(t *testing.T) {
	f := setupTransfer(t)
	snapshot := exportFixtureSnapshot(t, f)
	parentPath := filepath.Join(f.root, "nested")
	if err := os.Mkdir(parentPath, 0700); err != nil {
		t.Fatal(err)
	}
	in := ExportRequest{RequestID: "interrupted-parent-export", SnapshotID: snapshot.ID, Path: "nested/Report.md"}
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
	f.export(t, in, "transfer_unresolved")
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
	f.export(t, in, "")
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("recovered request retained its staging link: %v", err)
	}
}

func TestExportLinkUsesCapturedParentHandleAcrossRename(t *testing.T) {
	f := setupTransfer(t)
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
