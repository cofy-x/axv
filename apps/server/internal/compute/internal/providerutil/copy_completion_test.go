package providerutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/filedigest"
)

func TestCopyCompletionVerifiesCapturedIdentityAndBytes(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "disk"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("original"); err != nil {
		t.Fatal(err)
	}
	digest, err := filedigest.Sum(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "copy-complete")
	if err := WriteCopyCompletion(marker, "source", "target", 1, digest); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCopyCompletion(context.Background(), marker, "source", "target", 1, file); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCopyCompletion(context.Background(), marker, "other-source", "target", 1, file); err == nil {
		t.Fatal("changed source accepted")
	}
	if _, err := file.WriteAt([]byte("modified"), 0); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCopyCompletion(context.Background(), marker, "source", "target", 1, file); err == nil {
		t.Fatal("same-size changed bytes accepted")
	}
	for _, data := range []string{"target", "{}", `{"version":1,"sourceId":"source","targetId":"target","sizeGiB":1,"sha256":"bad"}`} {
		if err := os.WriteFile(marker, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := VerifyCopyCompletion(context.Background(), marker, "source", "target", 1, file); err == nil {
			t.Fatalf("invalid marker accepted: %s", data)
		}
	}
}
