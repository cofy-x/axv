package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestDatabaseIdentityPersistsAndSeparatesProjects(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "desktop.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Bootstrap(ctx, "browser")
	if err != nil || first.DatabaseID == "" {
		t.Fatalf("missing database identity: %+v %v", first, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reopened, err := s.Bootstrap(ctx, "another-browser")
	if err != nil || reopened.DatabaseID != first.DatabaseID {
		t.Fatalf("identity changed: %+v %v", reopened, err)
	}
	other, err := testStore(t).Bootstrap(ctx, "browser")
	if err != nil || other.DatabaseID == first.DatabaseID {
		t.Fatalf("projects share identity: %+v %v", other, err)
	}
}

func TestRepeatedMigrationPreservesFilesAndClientLayouts(t *testing.T) {
	s := testStore(t)
	identity, err := s.DatabaseID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	custom := create(t, s, "documents", "Keep.txt", "file")
	if _, err := s.Trash(ctx, "readme"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "readme"); err != nil {
		t.Fatal(err)
	}
	layout := json.RawMessage(`{"version":1,"windows":[{"id":"keep"}]}`)
	if err := s.SaveLayout(ctx, "browser", layout); err != nil {
		t.Fatal(err)
	}
	prefs := Preferences{Wallpaper: "solid", IconSnap: false, Clock24Hour: false}
	if err := s.SavePreferences(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := s.Bootstrap(ctx, "browser")
	if err != nil {
		t.Fatal(err)
	}
	if b.DatabaseID != identity || string(b.Layout) != string(layout) || b.Preferences != prefs {
		t.Fatalf("migration lost settings: %+v", b)
	}
	entry(t, s, custom.ID)
	_, _, err = s.Get(ctx, "readme")
	requireCode(t, err, "not_found")
	if err = s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := s.Bootstrap(ctx, "browser")
	if err != nil || again.DatabaseID != b.DatabaseID {
		t.Fatalf("migration is not stable: %+v %v", again, err)
	}
}

func TestDamagedPreferencesArePreserved(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.Exec("UPDATE settings SET value='broken' WHERE key='preferences'"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bootstrap(ctx, "browser"); err == nil {
		t.Fatal("accepted corrupt preferences")
	}
	var stored string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key='preferences'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "broken" {
		t.Fatalf("damaged preferences reset without consent: %q", stored)
	}
	entry(t, s, "readme")
}

func TestMetadataChangesRequireExpectedRevision(t *testing.T) {
	s := testStore(t)
	e := create(t, s, "documents", "Original.txt", "file")
	name, destination, content := "Renamed.txt", "desktop", "Original revision's draft"
	_, err := s.Update(ctx, e.ID, UpdateInput{Name: &name})
	requireCode(t, err, "revision_conflict")
	renamed, err := s.Update(ctx, e.ID, UpdateInput{Name: &name, Revision: &e.Revision})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Update(ctx, e.ID, UpdateInput{ParentID: &destination, Revision: &e.Revision})
	requireCode(t, err, "revision_conflict")
	_, err = s.Update(ctx, e.ID, UpdateInput{Content: &content, Revision: &e.Revision})
	requireCode(t, err, "revision_conflict")
	current, bytes, err := s.Get(ctx, e.ID)
	if err != nil || current.Name != name || current.ParentID != "documents" || len(bytes) != 0 {
		t.Fatalf("stale write changed data: %+v %q %v", current, bytes, err)
	}
	moved, err := s.Update(ctx, e.ID, UpdateInput{ParentID: &destination, Revision: &renamed.Revision})
	if err != nil || moved.ParentID != destination || moved.Revision != e.Revision+2 {
		t.Fatalf("valid move failed: %+v %v", moved, err)
	}
}

func TestDamagedLayoutDoesNotBlockFilesOrResetStoredRecord(t *testing.T) {
	s := testStore(t)
	for _, raw := range []string{"invalid-json", "[]", "null"} {
		if _, err := s.db.Exec("INSERT INTO layouts(client_id,value) VALUES('browser',?) ON CONFLICT(client_id) DO UPDATE SET value=excluded.value", raw); err != nil {
			t.Fatal(err)
		}
		b, err := s.Bootstrap(ctx, "browser")
		if err != nil || b.LayoutError == "" || string(b.Layout) != "null" || len(b.Entries) == 0 {
			t.Fatalf("damaged layout blocked bootstrap: %+v %v", b, err)
		}
		var stored string
		if err := s.db.QueryRow("SELECT value FROM layouts WHERE client_id='browser'").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != raw {
			t.Fatalf("layout reset without consent: %q", stored)
		}
	}
}
