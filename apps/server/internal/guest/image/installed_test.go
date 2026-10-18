package image

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func TestInstalledImageChecksItsOwnBinaryAndPlatform(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "axv-guest")
	data := []byte("the binary installed in this image")
	if err := os.WriteFile(executable, data, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	original := Installed{Version: Version, Provider: "lima", Arch: runtime.GOARCH,
		Guest: Contract{ImageVersion: Version, ProtocolVersion: protocol.Version, Capabilities: Capabilities(), BinarySHA256: hex.EncodeToString(digest[:])}}
	for _, tc := range []struct {
		name   string
		change func(*Installed)
		fail   bool
	}{
		{name: "matching binary", change: func(*Installed) {}},
		{name: "unverified binary allowed", change: func(i *Installed) { i.Guest.BinarySHA256 = "" }},
		{name: "changed binary", change: func(i *Installed) { i.Guest.BinarySHA256 = strings.Repeat("a", 64) }, fail: true},
		{name: "wrong architecture", change: func(i *Installed) { i.Arch = "other" }, fail: true},
		{name: "unknown provider", change: func(i *Installed) { i.Provider = "other" }, fail: true},
		{name: "image format", change: func(i *Installed) { i.Version++ }, fail: true},
		{name: "image contract", change: func(i *Installed) { i.Guest.ImageVersion++ }, fail: true},
		{name: "protocol", change: func(i *Installed) { i.Guest.ProtocolVersion++ }, fail: true},
		{name: "invalid capabilities", change: func(i *Installed) { i.Guest.Capabilities = []string{"exec", "exec"} }, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := original
			tc.change(&image)
			raw, err := json.Marshal(image)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "image.json")
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			resolved := false
			got, err := readInstalled(path, func() (string, error) {
				resolved = true
				return executable, nil
			})
			if (err != nil) != tc.fail {
				t.Fatalf("readInstalled() = %+v, %v; want failure %v", got, err, tc.fail)
			}
			if image.Guest.BinarySHA256 == "" && resolved {
				t.Fatal("optional binary verification resolved an executable")
			}
		})
	}
}

func TestInstalledImagePreservesExecutableResolutionFailure(t *testing.T) {
	image := Installed{Version: Version, Provider: "firecracker", Arch: runtime.GOARCH,
		Guest: Contract{ImageVersion: Version, ProtocolVersion: protocol.Version, BinarySHA256: strings.Repeat("a", 64)}}
	raw, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "image.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("executable unavailable")
	if _, err = readInstalled(path, func() (string, error) { return "", want }); !errors.Is(err, want) {
		t.Fatalf("resolution error = %v", err)
	}
}

func TestCapabilityListsDoNotShareMutableState(t *testing.T) {
	declared := Capabilities()
	declared[0] = "changed"
	if got := Capabilities(); !Has(got, "exec") || Has(got, "changed") {
		t.Fatalf("one image changed the release capability list: %v", got)
	}
}
