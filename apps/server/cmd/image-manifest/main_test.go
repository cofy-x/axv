package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
)

func TestPublishedBundleIdentityBindsRootfsAndRemainsImportable(t *testing.T) {
	dir := t.TempDir()
	guest := filepath.Join(dir, "axv-guest")
	if err := os.WriteFile(guest, []byte("fixture guest executable"), 0600); err != nil {
		t.Fatal(err)
	}
	var previous string
	for _, content := range []string{"first immutable disk", "different immutable disk"} {
		rootfs := filepath.Join(dir, "root.qcow2")
		output := filepath.Join(dir, "manifest.json")
		if err := os.WriteFile(rootfs, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{"--provider", "lima", "--arch", "arm64", "--guest", guest, "--rootfs", rootfs, "--name", "Fixture image", "--output", output}); err != nil {
			t.Fatal(err)
		}
		bundle, err := guestimage.LoadBundle(context.Background(), output, "lima", "arm64")
		if err != nil || bundle.ID == previous {
			t.Fatalf("changed bytes reused an immutable identity: %+v %v", bundle, err)
		}
		previous = bundle.ID
		if err := os.Remove(output); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvalidBuildMetadataDoesNotPublishManifest(t *testing.T) {
	dir := t.TempDir()
	guest := filepath.Join(dir, "axv-guest")
	if err := os.WriteFile(guest, []byte("fixture guest"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--provider", "other", "--arch", "arm64"}, {"--provider", "lima", "--arch", "other"}, {"--provider", "lima", "--arch", "arm64", "--rootfs", guest, "--name", "image", "--id", "bad/id"}} {
		output := filepath.Join(dir, "invalid.json")
		if err := run(append(args, "--guest", guest, "--output", output)); err == nil {
			t.Fatal("accepted invalid build metadata", args)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("invalid metadata published an output", err)
		}
	}
	output := filepath.Join(dir, "image.json")
	args := []string{"--provider", "firecracker", "--arch", "arm64", "--guest", guest, "--output", output}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	if err := run(args); err == nil {
		t.Fatal("overwrote an existing manifest")
	}
	var installed guestimage.Installed
	data, _ := os.ReadFile(output)
	if err := json.Unmarshal(data, &installed); err != nil || installed.Guest.BinarySHA256 == "" {
		t.Fatal("lost installed executable integrity", err)
	}
}
