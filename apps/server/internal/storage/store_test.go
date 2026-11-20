package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
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
