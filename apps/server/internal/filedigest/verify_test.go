package filedigest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestVerifierRejectsWrongDigestAndAcceptsUppercase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	var verifier Verifier
	if err := verifier.Verify(path, digest("other")); err == nil {
		t.Fatal("accepted a mismatched digest")
	}
	if err := verifier.Verify(path, strings.ToUpper(digest("original"))); err != nil {
		t.Fatalf("uppercase digest: %v", err)
	}
	if err := verifier.Verify(path, digest("original")); err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(filepath.Dir(path), digest("original")); err == nil {
		t.Fatal("accepted a directory")
	}
}

func TestVerifierRechecksChangedFileAndDoesNotReuseRemovedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	var verifier Verifier
	if err := verifier.Verify(path, digest("original")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the size unchanged and move mtime explicitly beyond filesystem precision.
	if err = os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	modified := info.ModTime().Add(2 * time.Second)
	if err = os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	if err = verifier.Verify(path, digest("original")); err == nil {
		t.Fatal("reused an outdated verification after the file changed")
	}
	if err = verifier.Verify(path, digest("modified")); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = verifier.Verify(path, digest("modified")); !os.IsNotExist(err) {
		t.Fatalf("removed file verification = %v, want not-exist error", err)
	}
}

func TestVerifierSupportsConcurrentReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	var verifier Verifier
	var group sync.WaitGroup
	errors := make(chan error, 16)
	for range 16 {
		group.Go(func() { errors <- verifier.Verify(path, digest("original")) })
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}
