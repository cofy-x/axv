package storage

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func baselineMigrationFiles(t *testing.T) fstest.MapFS {
	t.Helper()
	initial, err := migrationFiles.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{
		"migrations/0001_initial.sql": {Data: initial},
	}
}

func TestMigrationFailureRollsBackEarlierFiles(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateFrom(ctx, baselineMigrationFiles(t)); err != nil {
		t.Fatal(err)
	}
	identity, err := s.DatabaseID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files := baselineMigrationFiles(t)
	files["migrations/0002_probe.sql"] = &fstest.MapFile{
		Data: []byte("CREATE TABLE migration_probe (value TEXT); INSERT INTO migration_probe VALUES ('kept');"),
	}
	files["migrations/0003_next.sql"] = &fstest.MapFile{
		Data: []byte("INSERT INTO missing_table VALUES ('fail');"),
	}
	if err := s.migrateFrom(ctx, files); err == nil || !strings.Contains(err.Error(), "0003_next.sql") {
		t.Fatalf("want the failing migration filename, got %v", err)
	}
	var version, tables int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='migration_probe'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 1 || tables != 0 {
		t.Fatalf("earlier migration committed: version=%d tables=%d", version, tables)
	}
	entry(t, s, "readme")

	// The failed files are synthetic future changes, not production migrations.
	// Retrying after fixing the script must apply the entire pending sequence.
	files["migrations/0003_next.sql"] = &fstest.MapFile{
		Data: []byte("ALTER TABLE migration_probe ADD COLUMN note TEXT NOT NULL DEFAULT 'added';"),
	}
	if err := s.migrateFrom(ctx, files); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	var value, note string
	if err := s.db.QueryRow("SELECT value, note FROM migration_probe").Scan(&value, &note); err != nil {
		t.Fatal(err)
	}
	if version != 3 || value != "kept" || note != "added" {
		t.Fatalf("retry did not complete the upgrade: version=%d value=%q note=%q", version, value, note)
	}
	if err := s.migrateFrom(ctx, files); err != nil {
		t.Fatal(err)
	}
	b, err := s.Bootstrap(ctx, "browser")
	if err != nil || b.DatabaseID != identity {
		t.Fatalf("upgrade changed database identity: %+v %v", b, err)
	}
}

func TestInitialMigrationAndSeedFailureRollBack(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })

	files := baselineMigrationFiles(t)
	files["migrations/0002_reject_seed.sql"] = &fstest.MapFile{
		Data: []byte("CREATE TRIGGER reject_seed BEFORE INSERT ON entries BEGIN SELECT RAISE(ABORT, 'seed rejected'); END;"),
	}
	if err := s.migrateFrom(ctx, files); err == nil || !strings.Contains(err.Error(), "seed database documents") {
		t.Fatalf("want document seeding failure, got %v", err)
	}
	var version, applicationID, objects int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA application_id").Scan(&applicationID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema").Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if version != 0 || applicationID != 0 || objects != 0 {
		t.Fatalf("failed initialization left a partial database: version=%d application=%d objects=%d", version, applicationID, objects)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	entry(t, s, "readme")
}

func TestMigrationRejectsNonemptyUnversionedDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if _, err := db.Exec("CREATE TABLE unrelated (value TEXT); INSERT INTO unrelated VALUES ('keep')"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err == nil || !strings.Contains(err.Error(), "nonempty database") {
		t.Fatalf("want unversioned database rejection, got %v", err)
	}
	var version, objects int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'").Scan(&objects); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := db.QueryRow("SELECT value FROM unrelated").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if version != 0 || objects != 1 || value != "keep" {
		t.Fatalf("unversioned database changed: version=%d objects=%d value=%q", version, objects, value)
	}
}

func TestMigrationRejectsNegativeVersion(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.Exec("PRAGMA user_version=-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err == nil || !strings.Contains(err.Error(), "invalid database schema version") {
		t.Fatalf("want invalid version rejection, got %v", err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != -1 {
		t.Fatalf("invalid schema version was replaced with %d", version)
	}
	entry(t, s, "readme")
}

func TestInitialSchemaContainsOnlyCurrentCodexModel(t *testing.T) {
	s := testStore(t)
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil || len(names) != 1 || names[0] != "migrations/0001_initial.sql" {
		t.Fatalf("expected one consolidated baseline: %+v %v", names, err)
	}
	var version, applicationID int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("PRAGMA application_id").Scan(&applicationID); err != nil {
		t.Fatal(err)
	}
	if version != 1 || applicationID != databaseApplicationID {
		t.Fatalf("baseline identity: version=%d application=%d", version, applicationID)
	}
	for table, want := range map[string][]string{
		"codex_sessions": {"id", "project_path", "title", "archived_at", "metadata_revision", "created_at", "conversation_id", "history_home", "latest_run_id"},
		"codex_runs":     {"id", "session_id", "request_id", "previous_run_id", "request_cols", "request_rows", "status", "created_at", "ended_at", "reason", "tmux_session", "tmux_socket"},
		"codex_projects": {"path", "last_used_at"},
	} {
		rows, err := s.db.Query("SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			got = append(got, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s columns: %v %v", table, got, err)
		}
	}
	var sessions, runs, projects int
	if err := s.db.QueryRow("SELECT (SELECT count(*) FROM codex_sessions),(SELECT count(*) FROM codex_runs),(SELECT count(*) FROM codex_projects)").Scan(&sessions, &runs, &projects); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || runs != 0 || projects != 0 {
		t.Fatalf("initialization invented Codex history: %d sessions, %d runs, %d projects", sessions, runs, projects)
	}
}

func TestIncompatibleDatabaseFormatIsRejectedWithoutChanges(t *testing.T) {
	for _, tc := range []struct {
		name          string
		version, mark int
	}{
		{"unmarked version one", 1, 0},
		{"other format version one", 1, 1234},
		{"other format unversioned", 0, 1234},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "desktop.sqlite")
			db, err := sql.Open("sqlite", filename)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(fmt.Sprintf("CREATE TABLE kept (value TEXT); INSERT INTO kept VALUES ('original'); PRAGMA user_version=%d; PRAGMA application_id=%d", tc.version, tc.mark)); err != nil {
				t.Fatal(err)
			}
			if opened, err := Open(filename); err == nil {
				opened.Close()
				t.Fatal("opened an incompatible database")
			} else if !strings.Contains(err.Error(), "incompatible development database format") {
				t.Fatalf("wrong error: %v", err)
			}
			var version, applicationID, tables int
			if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("PRAGMA application_id").Scan(&applicationID); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table'").Scan(&tables); err != nil {
				t.Fatal(err)
			}
			var value string
			if err := db.QueryRow("SELECT value FROM kept").Scan(&value); err != nil {
				t.Fatal(err)
			}
			if version != tc.version || applicationID != tc.mark || tables != 1 || value != "original" {
				t.Fatalf("incompatible database changed: version=%d application=%d tables=%d value=%q", version, applicationID, tables, value)
			}
		})
	}
}
