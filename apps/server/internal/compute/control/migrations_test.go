package control

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeSQLBaselineIdentityPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	identity := s.HostID
	var version, format int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("PRAGMA application_id").Scan(&format); err != nil {
		t.Fatal(err)
	}
	if version != 1 || format != databaseApplicationID || identity == "" {
		t.Fatalf("baseline identity: %d %x %q", version, format, identity)
	}
	if _, err = s.db.Exec("INSERT INTO identity(singleton,id) VALUES(2,'another-host')"); err == nil {
		t.Fatal("a second host identity was accepted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.HostID != identity {
		t.Fatal("opening a database replaced its host identity")
	}
}

func TestComputeRejectsForeignAndOldBaselinesWithoutReset(t *testing.T) {
	for _, tc := range []struct {
		name            string
		version, format int
	}{
		{"prior unmarked baseline", 4, 0},
		{"desktop database", 1, 0x41585631},
		{"preparation database", 1, 0x41585031},
		{"nonempty unversioned", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "compute.sqlite")
			db, err := sql.Open("sqlite", file)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(fmt.Sprintf("CREATE TABLE kept(value TEXT); INSERT INTO kept VALUES('original'); PRAGMA user_version=%d; PRAGMA application_id=%d", tc.version, tc.format)); err != nil {
				t.Fatal(err)
			}
			if store, err := OpenStore(dir); err == nil {
				store.Close()
				t.Fatal("incompatible database was opened")
			} else if !strings.Contains(err.Error(), "database") {
				t.Fatal(err)
			}
			var version, format int
			var kept string
			if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("PRAGMA application_id").Scan(&format); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT value FROM kept").Scan(&kept); err != nil {
				t.Fatal(err)
			}
			if version != tc.version || format != tc.format || kept != "original" {
				t.Fatal("rejected database was changed")
			}
		})
	}
}
