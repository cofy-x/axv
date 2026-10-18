// Package filedigest verifies regular files against their SHA-256 digests.
package filedigest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Verifier caches successful checks by path, digest, size and modification time.
// Its zero value is ready for concurrent use.
type Verifier struct {
	mu       sync.Mutex
	verified map[string]bool
}

// Verify checks a regular file, reusing an unchanged successful verification.
func (v *Verifier) Verify(path, digest string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("image artifact must be a regular file")
	}
	key := fmt.Sprintf("%s:%s:%d:%d", path, digest, info.Size(), info.ModTime().UnixNano())
	if v.verified[key] {
		return nil
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.ToLower(digest) {
		return fmt.Errorf("SHA-256 mismatch for %s", filepath.Base(path))
	}
	if v.verified == nil {
		v.verified = map[string]bool{}
	}
	v.verified[key] = true
	return nil
}
