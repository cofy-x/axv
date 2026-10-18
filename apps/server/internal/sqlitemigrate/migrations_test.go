package sqlitemigrate

import (
	"context"
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsDiscoversAndOrdersFiles(t *testing.T) {
	files := fstest.MapFS{}
	for version := 12; version > 0; version-- {
		name := fmt.Sprintf("migrations/%04d_change.sql", version)
		files[name] = &fstest.MapFile{Data: []byte(fmt.Sprintf("SELECT %d;", version))}
	}
	migrations, err := loadMigrations(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != len(files) {
		t.Fatalf("discovered %d migrations, want %d", len(migrations), len(files))
	}
	for i, migration := range migrations {
		version := i + 1
		if migration.version != version || migration.sql != fmt.Sprintf("SELECT %d;", version) {
			t.Fatalf("migration order or contents changed at version %d: %+v", version, migration)
		}
	}
}

func TestLoadMigrationsRejectsInvalidCatalog(t *testing.T) {
	for _, tt := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"empty", nil, "no database migrations"},
		{"missing version", map[string]string{"initial.sql": "SELECT 1;"}, "filename"},
		{"unpadded version", map[string]string{"1_initial.sql": "SELECT 1;"}, "filename"},
		{"zero version", map[string]string{"0000_initial.sql": "SELECT 1;"}, "expected 1"},
		{"missing initial migration", map[string]string{"0002_next.sql": "SELECT 2;"}, "expected 1"},
		{"gap", map[string]string{"0001_initial.sql": "SELECT 1;", "0003_next.sql": "SELECT 3;"}, "expected 2"},
		{"duplicate", map[string]string{"0001_initial.sql": "SELECT 1;", "0001_other.sql": "SELECT 2;"}, "expected 2"},
		{"empty script", map[string]string{"0001_initial.sql": " \n\t"}, "is empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := fstest.MapFS{}
			for name, contents := range tt.files {
				files["migrations/"+name] = &fstest.MapFile{Data: []byte(contents)}
			}
			if _, err := loadMigrations(files); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestInitializationIsAtomicAndRunsOnlyOnce(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	files := fstest.MapFS{"migrations/0001_initial.sql": {Data: []byte("PRAGMA application_id=1234; CREATE TABLE kept(value TEXT);")}}
	calls := 0
	initialize := func(ctx context.Context, tx *sql.Tx) error {
		calls++
		_, err := tx.ExecContext(ctx, "INSERT INTO kept VALUES('initial')")
		return err
	}
	if err = Apply(context.Background(), db, files, 1234, func(ctx context.Context, tx *sql.Tx) error {
		if err := initialize(ctx, tx); err != nil {
			return err
		}
		return fmt.Errorf("initializer failed")
	}); err == nil {
		t.Fatal("initializer failure was ignored")
	}
	var version, format, objects int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("PRAGMA application_id").Scan(&format); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM sqlite_schema").Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if version != 0 || format != 0 || objects != 0 {
		t.Fatal("partial initialization was committed")
	}
	if err = Apply(context.Background(), db, files, 1234, initialize); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE kept SET value='user-edited'"); err != nil {
		t.Fatal(err)
	}
	if err = Apply(context.Background(), db, files, 1234, initialize); err != nil {
		t.Fatal(err)
	}
	var kept string
	if err = db.QueryRow("SELECT value FROM kept").Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || kept != "user-edited" {
		t.Fatal("opening a database reseeded user data")
	}
}

func TestSQLMarkerMustMatchItsOwnerBeforeCommit(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	files := fstest.MapFS{"migrations/0001_initial.sql": {Data: []byte("PRAGMA application_id=4321; CREATE TABLE wrong_owner(value TEXT);")}}
	if err = Apply(context.Background(), db, files, 1234, nil); err == nil || !strings.Contains(err.Error(), "format marker") {
		t.Fatal("mismatched SQL ownership was accepted", err)
	}
	var count, format int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_schema").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("PRAGMA application_id").Scan(&format); err != nil {
		t.Fatal(err)
	}
	if count != 0 || format != 0 {
		t.Fatal("wrong owner schema was partially published")
	}
}
