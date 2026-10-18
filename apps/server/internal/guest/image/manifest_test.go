package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func TestBundleBindsArtifactAndRejectsIncompatibleOrEscapingImages(t *testing.T) {
	dir := t.TempDir()
	data := []byte("immutable image fixture")
	digest := sha256.Sum256(data)
	sum := hex.EncodeToString(digest[:])
	if err := os.WriteFile(filepath.Join(dir, "root.qcow2"), data, 0600); err != nil {
		t.Fatal(err)
	}
	original := Bundle{Version: Version, ID: "axv-test", Name: "AXV test", Provider: "lima", Arch: "arm64", Guest: &Contract{ImageVersion: Version, ProtocolVersion: protocol.Version, Capabilities: []string{"exec"}, BinarySHA256: strings.Repeat("a", 64)}, Rootfs: Artifact{File: "root.qcow2", SHA256: sum}}
	for _, tc := range []struct {
		name     string
		change   func(*Bundle)
		rejected bool
	}{
		{name: "matching bundle", change: func(*Bundle) {}},
		{name: "invalid identity", change: func(b *Bundle) { b.ID = ".." }, rejected: true},
		{name: "display controls", change: func(b *Bundle) { b.Name = "image\nsecond line" }, rejected: true},
		{name: "older image", change: func(b *Bundle) { b.Guest.ImageVersion = 0 }, rejected: true},
		{name: "other protocol", change: func(b *Bundle) { b.Guest.ProtocolVersion++ }},
		{name: "wrong provider", change: func(b *Bundle) { b.Provider = "firecracker" }, rejected: true},
		{name: "wrong architecture", change: func(b *Bundle) { b.Arch = "amd64" }, rejected: true},
		{name: "path escape", change: func(b *Bundle) { b.Rootfs.File = "../root.qcow2" }, rejected: true},
		{name: "changed artifact", change: func(b *Bundle) { b.Rootfs.SHA256 = strings.Repeat("b", 64) }, rejected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := original
			contract := *original.Guest
			b.Guest = &contract
			tc.change(&b)
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "manifest.json")
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadBundle(context.Background(), path, "lima", "arm64")
			if (err != nil) != tc.rejected {
				t.Fatalf("error=%v, rejected=%t", err, tc.rejected)
			}
			if err == nil && got.RootfsPath != filepath.Join(dir, "root.qcow2") {
				t.Fatal("bundle lost its verified artifact path")
			}
		})
	}
}

func TestManifestRejectsOversizedTrailingBytesAndUnusedKernelClaims(t *testing.T) {
	for _, raw := range []string{`{}` + strings.Repeat(" ", maxManifestBytes) + `{}`, `{"kernel":{"file":"../unverified-kernel","sha256":"ignored"}}`} {
		path := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		var bundle Bundle
		if err := decode(path, &bundle); err == nil {
			t.Fatal("accepted oversized or unsupported manifest metadata")
		}
	}
}

func TestCompatibilityDependsOnProtocolAndCapabilitiesNotApplicationDigest(t *testing.T) {
	for _, digest := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64), ""} {
		contract := Contract{ImageVersion: Version, ProtocolVersion: protocol.Version, Capabilities: []string{"exec"}, BinarySHA256: digest}
		if err := contract.Validate(); err != nil {
			t.Fatal(err)
		}
		if err := contract.Compatible(); err != nil {
			t.Fatalf("image-local binary digest changed compatibility: %v", err)
		}
	}
	contract := Contract{ImageVersion: Version, ProtocolVersion: protocol.Version + 1, Capabilities: []string{"exec"}}
	if err := contract.Validate(); err != nil {
		t.Fatal("structurally valid future metadata must retain basic VM use", err)
	}
	if err := contract.Compatible(); err == nil {
		t.Fatal("unknown enhanced protocol accepted")
	}
	contract.Capabilities = []string{"exec", "exec"}
	if err := contract.Validate(); err == nil {
		t.Fatal("duplicate capability accepted")
	}
}
