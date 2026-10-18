package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var ctx = context.Background()

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func create(t *testing.T, s *Store, parentID, name, kind string) Entry {
	t.Helper()
	e, err := s.Create(ctx, CreateInput{ParentID: parentID, Name: name, Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func entry(t *testing.T, s *Store, id string) Entry {
	t.Helper()
	e, _, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestPersistenceSeedsAndBrowserLayouts(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Trash(ctx, "readme"); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, "readme"); err != nil {
		t.Fatal(err)
	}
	custom := create(t, s, "documents", "Persistent.txt", "file")
	text := "Stored across process restarts."
	if _, err = s.Update(ctx, custom.ID, UpdateInput{Content: &text, Revision: &custom.Revision}); err != nil {
		t.Fatal(err)
	}
	prefs := Preferences{Wallpaper: "solid", IconSnap: false, Clock24Hour: true}
	if err = s.SavePreferences(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveLayout(ctx, "browser-one", json.RawMessage(`{"windows":[{"id":"files","x":12}]}`)); err != nil {
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
	_, content, err := s.Get(ctx, custom.ID)
	if err != nil || string(content) != text {
		t.Fatalf("persisted content %q: %v", content, err)
	}
	_, _, err = s.Get(ctx, "readme")
	requireCode(t, err, "not_found")
	b, err := s.Bootstrap(ctx, "browser-one")
	if err != nil {
		t.Fatal(err)
	}
	if b.Preferences != prefs || !bytes.Contains(b.Layout, []byte("files")) {
		t.Fatalf("preferences/layout lost: %+v", b)
	}
	b, err = s.Bootstrap(ctx, "browser-two")
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Layout) != "null" {
		t.Fatalf("layout leaked between browsers: %s", b.Layout)
	}
}

func TestOptimisticRevisionDoesNotOverwrite(t *testing.T) {
	s := testStore(t)
	e := create(t, s, "documents", "Revision.txt", "file")
	first, second := "First browser", "Second browser"
	updated, err := s.Update(ctx, e.ID, UpdateInput{Content: &first, Revision: &e.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != e.Revision+1 {
		t.Fatal("revision did not advance")
	}
	_, err = s.Update(ctx, e.ID, UpdateInput{Content: &second, Revision: &e.Revision})
	requireCode(t, err, "revision_conflict")
	_, content, err := s.Get(ctx, e.ID)
	if err != nil || string(content) != first {
		t.Fatalf("conflict overwrote data: %q %v", content, err)
	}
	_, err = s.Update(ctx, e.ID, UpdateInput{Content: &second})
	requireCode(t, err, "revision_conflict")
}

func TestConcurrentWritesHaveOneWinner(t *testing.T) {
	s := testStore(t)
	e := create(t, s, "documents", "Concurrent.txt", "file")
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for _, value := range []string{"One", "Two"} {
		workers.Go(func() {
			_, err := s.Update(ctx, e.ID, UpdateInput{Content: &value, Revision: &e.Revision})
			errs <- err
		})
	}
	workers.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			requireCode(t, err, "revision_conflict")
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("want one commit and one conflict, got %d/%d", success, conflict)
	}
}

func TestNamesMovesAndSystemFolders(t *testing.T) {
	s := testStore(t)
	folder := create(t, s, "documents", "Folder", "folder")
	child := create(t, s, folder.ID, "Child", "folder")
	_, err := s.Update(ctx, folder.ID, UpdateInput{ParentID: &child.ID, Revision: &folder.Revision})
	requireCode(t, err, "folder_cycle")
	_, err = s.Create(ctx, CreateInput{ParentID: "documents", Name: "folder", Kind: "file"})
	requireCode(t, err, "name_conflict")
	for _, name := range []string{"", " ", "..", "a/b", "a\\b", "bad\nname", strings.Repeat("x", 256)} {
		_, err = s.Create(ctx, CreateInput{ParentID: "documents", Name: name, Kind: "file"})
		requireCode(t, err, "invalid_name")
	}
	file := create(t, s, "documents", "file.txt", "file")
	_, err = s.Create(ctx, CreateInput{ParentID: file.ID, Name: "child", Kind: "file"})
	requireCode(t, err, "invalid_parent")
	err = s.Delete(ctx, file.ID)
	requireCode(t, err, "not_trashed")
	_, err = s.Trash(ctx, "documents")
	requireCode(t, err, "system_entry")
	_, err = s.Update(ctx, "desktop", UpdateInput{Name: ptr("renamed")})
	requireCode(t, err, "unavailable")
}

func TestTrashRestorePreservesSeparatelyTrashedDescendants(t *testing.T) {
	s := testStore(t)
	folder := create(t, s, "documents", "Projects", "folder")
	live := create(t, s, folder.ID, "Active.txt", "file")
	separate := create(t, s, folder.ID, "Old.txt", "file")
	if _, err := s.Trash(ctx, separate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Trash(ctx, folder.ID); err != nil {
		t.Fatal(err)
	}
	if entry(t, s, live.ID).TrashedAt == "" {
		t.Fatal("descendant was not trashed")
	}
	if _, err := s.Restore(ctx, folder.ID); err != nil {
		t.Fatal(err)
	}
	if entry(t, s, live.ID).TrashedAt != "" {
		t.Fatal("descendant was not restored")
	}
	if entry(t, s, separate.ID).TrashedAt == "" {
		t.Fatal("independently trashed child was unexpectedly restored")
	}
	if _, err := s.Trash(ctx, folder.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, folder.ID); err != nil {
		t.Fatal(err)
	}
	remaining := entry(t, s, separate.ID)
	if remaining.ParentID != "documents" || remaining.TrashedAt == "" {
		t.Fatalf("separate trash was lost: %+v", remaining)
	}
	_, _, err := s.Get(ctx, live.ID)
	requireCode(t, err, "not_found")
	if _, err = s.Restore(ctx, separate.ID); err != nil {
		t.Fatal(err)
	}
	if entry(t, s, separate.ID).TrashedAt != "" {
		t.Fatal("separate item could not be restored")
	}
}

func TestRestoreFallbackAndNameConflict(t *testing.T) {
	s := testStore(t)
	folder := create(t, s, "documents", "Projects", "folder")
	file := create(t, s, folder.ID, "Notes.txt", "file")
	create(t, s, "documents", "Notes.txt", "file")
	if _, err := s.Trash(ctx, folder.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := s.Restore(ctx, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ParentID != "documents" || restored.Name != "Notes (2).txt" {
		t.Fatalf("unexpected fallback: %+v", restored)
	}
	create(t, s, "documents", "Projects", "folder")
	restored, err = s.Restore(ctx, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Name != "Projects (2)" {
		t.Fatalf("restore name collision: %+v", restored)
	}
}

func TestImportLimitsTypesAndAtomicBatch(t *testing.T) {
	s := testStore(t)
	first, err := s.Import(ctx, "documents", "Text.txt", []byte("hello"))
	if err != nil || !first.Editable {
		t.Fatalf("text import: %+v %v", first, err)
	}
	second, err := s.Import(ctx, "documents", "Text.txt", []byte("next"))
	if err != nil || second.Name != "Text (2).txt" {
		t.Fatalf("unique name: %+v %v", second, err)
	}
	binary, err := s.Import(ctx, "documents", "Fake.png", []byte{0xff, 0xfe, 0, 1})
	if err != nil || binary.Editable || binary.Previewable {
		t.Fatalf("extension affected classification: %+v %v", binary, err)
	}
	large, err := s.Import(ctx, "documents", "Large.txt", bytes.Repeat([]byte("a"), MaxTextSize+1))
	if err != nil || large.Editable {
		t.Fatalf("large text editing enabled: %+v %v", large, err)
	}
	limit, err := s.Import(ctx, "documents", "Limit.bin", make([]byte, MaxFileSize))
	if err != nil || limit.Size != MaxFileSize {
		t.Fatalf("10 MiB boundary failed: %+v %v", limit, err)
	}
	_, err = s.Import(ctx, "documents", "TooLarge.bin", make([]byte, MaxFileSize+1))
	requireCode(t, err, "file_too_large")
	before, _ := s.List(ctx)
	_, err = s.ImportMany(ctx, "documents", []ImportFile{{Name: "Valid.txt", Data: []byte("valid")}, {Name: "../invalid", Data: []byte("invalid")}})
	requireCode(t, err, "invalid_name")
	after, _ := s.List(ctx)
	if len(before) != len(after) {
		t.Fatal("failed import partially committed")
	}
	text := strings.Repeat("a", MaxTextSize)
	saved, err := s.Update(ctx, first.ID, UpdateInput{Content: &text, Revision: &first.Revision})
	if err != nil {
		t.Fatal(err)
	}
	text += "x"
	_, err = s.Update(ctx, first.ID, UpdateInput{Content: &text, Revision: &saved.Revision})
	requireCode(t, err, "text_too_large")
}

func TestDuplicateCopiesLiveTreeAndEmptyTrash(t *testing.T) {
	s := testStore(t)
	folder := create(t, s, "documents", "Original", "folder")
	file, err := s.Create(ctx, CreateInput{ParentID: folder.ID, Name: "Original.txt", Kind: "file", Content: "shared content"})
	if err != nil {
		t.Fatal(err)
	}
	old := create(t, s, folder.ID, "Deleted.txt", "file")
	if _, err = s.Trash(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	copy, err := s.Duplicate(ctx, folder.ID, "")
	if err != nil || copy.Name != "Original (2)" {
		t.Fatalf("duplicate: %+v %v", copy, err)
	}
	entries, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	children := 0
	for _, e := range entries {
		if e.ParentID == copy.ID {
			children++
			_, content, err := s.Get(ctx, e.ID)
			if err != nil || string(content) != "shared content" || e.ID == file.ID {
				t.Fatalf("bad copied content: %q %v", content, err)
			}
		}
	}
	if children != 1 {
		t.Fatalf("copied %d children, want one live child", children)
	}
	if err = s.EmptyTrash(ctx); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Get(ctx, old.ID)
	requireCode(t, err, "not_found")
	entry(t, s, file.ID)
}

func TestCorruptAndFutureDatabaseAreNotReset(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "broken.sqlite")
	original := []byte("This is not a database.")
	if err := os.WriteFile(filename, original, 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(filename); err == nil {
		s.Close()
		t.Fatal("opened corrupt database")
	}
	after, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatalf("corrupt database was changed: %q %v", after, err)
	}
	s := testStore(t)
	if _, err = s.db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if err = s.migrate(ctx); err == nil {
		t.Fatal("accepted future schema")
	}
}

func ptr[T any](value T) *T { return &value }
