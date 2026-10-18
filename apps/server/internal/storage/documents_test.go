package storage

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestExportReceiptCompletionIsImmutableAndRequiresReservation(t *testing.T) {
	s := testStore(t)
	p, err := s.RegisterProject(ctx, "Project", "/example/project", "device:inode")
	if err != nil {
		t.Fatal(err)
	}
	request := DocumentTransfer{RequestID: "export-atomic-receipt", PayloadHash: "payload", Kind: "export", ProjectID: p.ID, Path: "Report.md", SHA256: "digest", ParentIdentity: "device:inode"}
	request, err = s.ReserveExport(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	success := json.RawMessage(`{"path":"Report.md","sha256":"digest"}`)
	if err := s.CompleteExport(ctx, request, success); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteExport(ctx, request, success); err != nil {
		t.Fatalf("matching completion lost idempotency: %v", err)
	}
	conflict := json.RawMessage(`{"code":"name_conflict","error":"A different file exists"}`)
	requireCode(t, s.CompleteExport(ctx, request, conflict), "transfer_unresolved")
	receipt, found, err := s.DocumentTransfer(ctx, request.RequestID, request.PayloadHash)
	if err != nil || !found || !bytes.Equal(receipt.Result, success) {
		t.Fatalf("a losing writer changed an acknowledged receipt: %+v %v %v", receipt, found, err)
	}
	request.RequestID = "unreserved-export"
	requireCode(t, s.CompleteExport(ctx, request, success), "not_found")
}

func TestSnapshotChecksRevisionAndOutlivesSource(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "documents.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Create(ctx, CreateInput{ParentID: "documents", Name: "Plan.md", Kind: "file", Content: "# Original"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateSnapshot(ctx, e.ID, 0)
	requireCode(t, err, "revision_conflict")
	d, err := s.CreateSnapshot(ctx, e.ID, e.Revision)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateSnapshot(ctx, e.ID, e.Revision)
	if err != nil || again.ID != d.ID {
		t.Fatalf("duplicate snapshot %+v %v", again, err)
	}
	changed := "# New"
	_, err = s.Update(ctx, e.ID, UpdateInput{Content: &changed, Revision: &e.Revision})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateSnapshot(ctx, e.ID, e.Revision)
	requireCode(t, err, "revision_conflict")
	if _, err = s.Trash(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Snapshot(ctx, d.ID)
	if err != nil || got != d || got.Content != "# Original" || len(got.SHA256) != 64 || got.DatabaseID == "" {
		t.Fatalf("immutable snapshot lost: %+v %v", got, err)
	}
}

func TestProjectsLinksAndAtomicImportReceipts(t *testing.T) {
	s := testStore(t)
	p, err := s.RegisterProject(ctx, "Project", "/example/project", "device:inode")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.RegisterProject(ctx, "Other title", "/example/project", "device:inode")
	if err != nil || again.ID != p.ID {
		t.Fatalf("registration changed %+v %v", again, err)
	}
	replacement, err := s.RegisterProject(ctx, "Project", "/example/project", "replacement")
	if err != nil || replacement.ID == p.ID {
		t.Fatalf("explicit replacement registration reused old project identity: %+v %v", replacement, err)
	}
	again, err = s.RegisterProject(ctx, "Other replacement name", "/example/project", "replacement")
	if err != nil || again.ID != replacement.ID {
		t.Fatalf("replacement registration did not retain its new identity: %+v %v", again, err)
	}
	e := create(t, s, "documents", "Link.md", "file")
	l, err := s.AddDocumentLink(ctx, e.ID, DocumentLink{Kind: "project", ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	againLink, err := s.AddDocumentLink(ctx, e.ID, DocumentLink{Kind: "project", ProjectID: p.ID})
	if err != nil || againLink.ID != l.ID {
		t.Fatalf("duplicate link %+v %v", againLink, err)
	}
	_, err = s.AddDocumentLink(ctx, e.ID, DocumentLink{Kind: "machine", MachineID: "vm-one"})
	requireCode(t, err, "invalid_link")
	_, err = s.AddDocumentLink(ctx, e.ID, DocumentLink{Kind: "conversation", ConversationID: "missing"})
	requireCode(t, err, "not_found")
	_, err = s.AddDocumentLink(ctx, e.ID, DocumentLink{Kind: "machine", HostID: "host-original", MachineID: "vm-one"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.DocumentLinks(ctx, DocumentLink{HostID: "host-replacement", MachineID: "vm-one"})
	if err != nil || len(other) != 0 {
		t.Fatalf("machine link changed hosts: %+v %v", other, err)
	}
	tx := DocumentTransfer{RequestID: "import-request-one", PayloadHash: "payload", Kind: "import", ProjectID: p.ID, Path: "README.md", SHA256: "digest"}
	_, err = s.ImportProjectFile(ctx, tx, "missing", "README.md", []byte("hello"))
	requireCode(t, err, "not_found")
	_, found, err := s.DocumentTransfer(ctx, tx.RequestID, tx.PayloadHash)
	if err != nil || found {
		t.Fatalf("failed import left receipt: %v %v", found, err)
	}
	result, err := s.ImportProjectFile(ctx, tx, "documents", "README.md", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.ImportProjectFile(ctx, tx, "documents", "README.md", []byte("changed"))
	if err != nil || string(replay) != string(result) {
		t.Fatalf("replay duplicated copy: %s %v", replay, err)
	}
	var response struct {
		Entry Entry `json:"entry"`
	}
	if err = json.Unmarshal(result, &response); err != nil {
		t.Fatal(err)
	}
	links, err := s.DocumentLinks(ctx, DocumentLink{ProjectID: p.ID})
	if err != nil || len(links) != 2 {
		t.Fatalf("import provenance link missing %+v %v", links, err)
	}
	_, _, err = s.DocumentTransfer(ctx, tx.RequestID, "different")
	requireCode(t, err, "idempotency_conflict")
	if _, err = s.Trash(ctx, response.Entry.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, response.Entry.ID); err != nil {
		t.Fatal(err)
	}
	links, err = s.DocumentLinks(ctx, DocumentLink{EntryID: response.Entry.ID})
	if err != nil || len(links) != 0 {
		t.Fatalf("dangling live document links %+v %v", links, err)
	}
}
