package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryHomeCanonicalizesMissingPathsWithoutCreatingThem(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "missing", "history")
	t.Setenv("CODEX_HOME", home)
	before, err := historyHome()
	if err != nil || before != filepath.Join(canonicalRoot, "missing", "history") {
		t.Fatalf("missing history path: %q %v", before, err)
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("history lookup created its missing parent: %v", err)
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	after, err := historyHome()
	if err != nil || after != before {
		t.Fatalf("creating history changed its identity: %q -> %q %v", before, after, err)
	}
}

func TestHistoryHomeKeepsIdentityAcrossCreationUnderSymlink(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(actual, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	home := filepath.Join(link, "missing", "history")
	t.Setenv("CODEX_HOME", home)
	before, err := historyHome()
	canonicalActual, canonicalErr := filepath.EvalSymlinks(actual)
	if err != nil || canonicalErr != nil || before != filepath.Join(canonicalActual, "missing", "history") {
		t.Fatalf("symlinked history: %q %v %v", before, err, canonicalErr)
	}
	if _, err := os.Stat(filepath.Join(actual, "missing")); !os.IsNotExist(err) {
		t.Fatalf("history lookup created directories: %v", err)
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	after, err := historyHome()
	if err != nil || after != before {
		t.Fatalf("creating symlinked history changed its identity: %q -> %q %v", before, after, err)
	}
}

func TestHistoryHomeRejectsUnresolvableExistingComponents(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(file, "history"))
	if home, err := historyHome(); err == nil {
		t.Fatalf("accepted a file as history parent: %q", home)
	}
	link := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing-target"), link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(link, "history"))
	if home, err := historyHome(); err == nil {
		t.Fatalf("guessed a dangling history parent: %q", home)
	}
}
