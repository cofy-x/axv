package lima

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyArtifactCapturesSparseContentsIndependently(t *testing.T) {
	dir := t.TempDir()
	source, err := os.Create(filepath.Join(dir, "source.raw"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	const tail = 4 << 20
	if _, err = source.WriteAt([]byte("begin"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err = source.WriteAt([]byte("end"), tail); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "snapshot.raw")
	if err = copyArtifact(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err = source.WriteAt([]byte("later"), 0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != tail+3 || string(data[:5]) != "begin" || string(data[tail:]) != "end" || !bytes.Equal(data[5:tail], make([]byte, tail-5)) {
		t.Fatal("the snapshot lost sparse contents or changed with its source")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(destination)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("snapshot is not private: %v", err)
		}
	}
}

func TestCopyArtifactCancellationPreservesPublishedFile(t *testing.T) {
	dir := t.TempDir()
	source, err := os.Create(filepath.Join(dir, "source.raw"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination := filepath.Join(dir, "snapshot.raw")
	if err = os.WriteFile(destination, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyArtifact(ctx, source, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled snapshot did not report cancellation: %v", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "previous" {
		t.Fatalf("cancelled capture replaced the published file: %q %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("cancelled capture left temporary resources: %v %v", entries, err)
	}
}
