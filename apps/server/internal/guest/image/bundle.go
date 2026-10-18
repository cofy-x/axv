package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/cofy-x/axv/apps/server/internal/filedigest"
)

const MaxRootfsBytes int64 = 8 << 30

var imageID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

type Artifact struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type Bundle struct {
	Version    int       `json:"version"`
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Provider   string    `json:"provider"`
	Arch       string    `json:"arch"`
	Guest      *Contract `json:"guest,omitempty"`
	Rootfs     Artifact  `json:"rootfs"`
	RootfsPath string    `json:"-"`
}

func ValidatePlatform(provider, arch string) error {
	if (provider != "lima" && provider != "firecracker") || (arch != "amd64" && arch != "arm64") {
		return fmt.Errorf("image platform requires lima or firecracker and amd64 or arm64")
	}
	return nil
}

func (b Bundle) Validate() error {
	if b.Version != Version || !imageID.MatchString(b.ID) || strings.TrimSpace(b.Name) == "" || len(b.Name) > 128 || strings.IndexFunc(b.Name, unicode.IsControl) >= 0 {
		return fmt.Errorf("invalid AXV image bundle identity or version")
	}
	if err := ValidatePlatform(b.Provider, b.Arch); err != nil {
		return err
	}
	if b.Guest != nil {
		if err := b.Guest.Validate(); err != nil {
			return err
		}
	}
	if b.Rootfs.File == "." || filepath.Base(b.Rootfs.File) != b.Rootfs.File || b.Rootfs.File == "" || strings.ContainsAny(b.Rootfs.File, "\\\x00\r\n") {
		return fmt.Errorf("bundle rootfs must be a filename beside the manifest")
	}
	if digest, err := hex.DecodeString(b.Rootfs.SHA256); err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("invalid bundle rootfs digest")
	}
	return nil
}

// LoadBundle accepts a local administrator-selected bundle, never an arbitrary
// URL or host path supplied by a VM create request. All artifacts stay beside
// their manifest, and the reviewed rootfs digest is checked before publication.
func LoadBundle(ctx context.Context, path, provider, arch string) (Bundle, error) {
	var b Bundle
	if !filepath.IsAbs(path) {
		return b, fmt.Errorf("select an absolute AXV image bundle manifest path")
	}
	if err := decode(path, &b); err != nil {
		return b, fmt.Errorf("AXV image bundle: %w", err)
	}
	if err := b.Validate(); err != nil {
		return b, err
	}
	if b.Provider != provider || b.Arch != arch || (arch != "amd64" && arch != "arm64") {
		return b, fmt.Errorf("image bundle provider or architecture does not match")
	}
	b.RootfsPath = filepath.Join(filepath.Dir(path), b.Rootfs.File)
	info, err := os.Lstat(b.RootfsPath)
	if err != nil {
		return b, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxRootfsBytes {
		return b, fmt.Errorf("bundle rootfs must be a regular file")
	}
	if err = ctx.Err(); err != nil {
		return b, err
	}
	var verifier filedigest.Verifier
	if err = verifier.Verify(b.RootfsPath, b.Rootfs.SHA256); err != nil {
		return b, err
	}
	if err = ctx.Err(); err != nil {
		return b, err
	}
	return b, nil
}
