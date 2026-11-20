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

func TestRepeatedMigrationPreservesProjectsAndClientLayouts(t *testing.T) {
	s := testStore(t)
	identity, err := s.DatabaseID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.RegisterProject(ctx, "Main", "/project", "device:inode")
	if err != nil {
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
	got, err := s.Project(ctx, project.ID)
	if err != nil || got != project {
		t.Fatalf("migration changed registered project: %+v %v", got, err)
	}
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
}

func TestDamagedLayoutDoesNotBlockBootstrapOrResetStoredRecord(t *testing.T) {
	s := testStore(t)
	for _, raw := range []string{"invalid-json", "[]", "null"} {
		if _, err := s.db.Exec("INSERT INTO layouts(client_id,value) VALUES('browser',?) ON CONFLICT(client_id) DO UPDATE SET value=excluded.value", raw); err != nil {
			t.Fatal(err)
		}
		b, err := s.Bootstrap(ctx, "browser")
		if err != nil || b.LayoutError == "" || string(b.Layout) != "null" || b.DatabaseID == "" {
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
