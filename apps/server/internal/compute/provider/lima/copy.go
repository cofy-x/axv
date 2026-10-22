package lima

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
)

// copyArtifact reads an already-open, confined source and publishes a flushed,
// private copy. APFS uses an fd-based clone; the portable fallback preserves holes.
func copyArtifact(ctx context.Context, source *os.File, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("artifact source must be a regular file")
	}
	// The caller reserves destination and its fixed staging name inside an
	// owned copy directory. Retry reclaims only this operation's partial file.
	name := destination + ".copy"
	if err := providerutil.RemoveCopyScratch(name); err != nil {
		return err
	}
	defer os.Remove(name)
	if cloneErr := cloneOpenFile(source, name); cloneErr != nil {
		temporary, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer temporary.Close()
		if _, err = source.Seek(0, io.SeekStart); err != nil {
			return err
		}
		buffer, zero := make([]byte, 1<<20), make([]byte, 1<<20)
		for {
			if err = ctx.Err(); err != nil {
				return err
			}
			n, readErr := source.Read(buffer)
			if n > 0 {
				if bytes.Equal(buffer[:n], zero[:n]) {
					_, err = temporary.Seek(int64(n), io.SeekCurrent)
				} else {
					_, err = temporary.Write(buffer[:n])
				}
				if err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		if err = temporary.Truncate(info.Size()); err != nil {
			return err
		}
		if err = temporary.Close(); err != nil {
			return err
		}
	}
	if err = os.Chmod(name, 0600); err != nil {
		return err
	}
	after, err := source.Stat()
	if err != nil {
		return err
	}
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return fmt.Errorf("artifact source changed during capture")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return atomicfile.Commit(name, destination)
}
