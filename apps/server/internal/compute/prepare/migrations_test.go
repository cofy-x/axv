package prepare

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestPreparationOwnsAnIndependentSQLBaseline(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	var version, format int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("PRAGMA application_id").Scan(&format); err != nil {
		t.Fatal(err)
	}
	if version != 1 || format != databaseApplicationID {
		t.Fatalf("preparation identity: %d %x", version, format)
	}
}
func TestPreparationRejectsForeignAndPriorFormatsWithoutReset(t *testing.T) {
	for _, tc := range []struct {
		name            string
		version, format int
	}{
		{"prior preparation", 2, 0},
		{"compute", 1, 0x41584331},
		{"desktop", 1, 0x41585631},
		{"unversioned with a view", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			db, err := sql.Open("sqlite", filepath.Join(dir, "preparation.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(fmt.Sprintf("CREATE VIEW kept AS SELECT 'original' AS value; PRAGMA user_version=%d; PRAGMA application_id=%d", tc.version, tc.format)); err != nil {
				t.Fatal(err)
			}
			if s, err := openStore(dir); err == nil {
				s.db.Close()
				t.Fatal("incompatible database was opened")
			}
			var version, format int
			var value string
			if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("PRAGMA application_id").Scan(&format); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT value FROM kept").Scan(&value); err != nil {
				t.Fatal(err)
			}
			if version != tc.version || format != tc.format || value != "original" {
				t.Fatal("rejected database was changed")
			}
		})
	}
}
