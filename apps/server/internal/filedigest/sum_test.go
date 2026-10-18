package filedigest

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSumCapturesTheWholeOpenFileAndRetainsCallerOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("complete artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.Seek(4, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	actual, err := Sum(context.Background(), f)
	if err != nil || actual != digest("complete artifact") {
		t.Fatalf("open-file digest = %q, %v", actual, err)
	}
	position, err := f.Seek(0, io.SeekCurrent)
	if err != nil || position != int64(len("complete artifact")) {
		t.Fatalf("file position = %d, %v; caller must retain the open file at EOF", position, err)
	}
}

func TestSumReturnsCancellationWithoutACompletedDigest(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "artifact-")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.WriteString("artifact"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	actual, err := Sum(ctx, f)
	if !errors.Is(err, context.Canceled) || actual != "" {
		t.Fatalf("canceled digest = %q, %v", actual, err)
	}
}
