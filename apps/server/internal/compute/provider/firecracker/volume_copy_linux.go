//go:build linux

package firecracker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	"github.com/cofy-x/axv/apps/server/internal/diskspace"
	"github.com/cofy-x/axv/apps/server/internal/filedigest"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

// CopyVolume implements only offline copies; it does not enable arbitrary
// Firecracker attachment editing or disk resizing.
func (d *Driver) CopyVolume(ctx context.Context, source, target compute.Volume) error {
	if source.ID == target.ID || source.Provider != "firecracker" || target.Provider != source.Provider || target.SizeGiB != source.SizeGiB {
		return fmt.Errorf("an offline Firecracker copy requires independent, equally sized volume identities")
	}
	if source.MachineID != "" {
		observed, err := d.Inspect(ctx, compute.Machine{ID: source.MachineID})
		if err != nil {
			return err
		}
		if observed.Status != "stopped" {
			return fmt.Errorf("the source VM is not confirmed stopped")
		}
	}
	info, err := d.projectVolumeDisk(source.ID)
	if err != nil {
		return err
	}
	if info.Size() != source.SizeGiB<<30 {
		return fmt.Errorf("the source project disk does not match its reserved size")
	}
	sourceRoot, err := os.OpenRoot(filepath.Join(d.root, "volumes", source.ID))
	if err != nil {
		return err
	}
	defer sourceRoot.Close()
	from, err := sourceRoot.Open("disk.ext4")
	if err != nil {
		return err
	}
	defer from.Close()
	opened, err := from.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return fmt.Errorf("the source disk changed while opening it: %v", err)
	}
	if source.IsProjectBackup() {
		if err := providerutil.VerifyCopyCompletion(ctx, filepath.Join(d.root, "volumes", source.ID, "copy-complete"), source.Backup.SourceVolumeID, source.ID, source.SizeGiB, from); err != nil {
			return err
		}
		if _, err := from.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	dir := filepath.Join(d.root, "volumes", target.ID)
	if err := owneddir.Ensure(dir, d.owner+":"+target.ID); err != nil {
		return err
	}
	if err := providerutil.PinJSON(filepath.Join(dir, "copy.json"), []any{source.ID, target.SizeGiB}); err != nil {
		return err
	}
	if _, exists, err := providerutil.ReadCopyCompletion(filepath.Join(dir, "copy-complete"), source.ID, target.ID, target.SizeGiB); err != nil {
		return err
	} else if exists {
		copied, err := d.projectVolumeDisk(target.ID)
		if err != nil {
			return err
		}
		if copied.Size() != info.Size() {
			return fmt.Errorf("the completed volume copy changed; no replacement was created")
		}
		file, err := os.Open(filepath.Join(dir, "disk.ext4"))
		if err != nil {
			return err
		}
		defer file.Close()
		return providerutil.VerifyCopyCompletion(ctx, filepath.Join(dir, "copy-complete"), source.ID, target.ID, target.SizeGiB, file)
	}
	if info, err := os.Lstat(filepath.Join(dir, "disk.ext4")); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("an incomplete disk copy must be a regular owned file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	// This name belongs to the pinned copy intent. A killed controller leaves
	// at most this one staging file, which the same operation can reclaim.
	copyPath := filepath.Join(dir, "copy-disk.ext4")
	if err := providerutil.RemoveCopyScratch(copyPath); err != nil {
		return err
	}
	if err := diskspace.Require(d.root, source.SizeGiB<<30); err != nil {
		return err
	}
	to, err := os.OpenFile(copyPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer to.Close()
	defer os.Remove(to.Name())
	buffer, zero := make([]byte, 1<<20), make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := from.Read(buffer)
		if n > 0 {
			if bytes.Equal(buffer[:n], zero[:n]) {
				_, err = to.Seek(int64(n), io.SeekCurrent)
			} else {
				_, err = to.Write(buffer[:n])
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
	if err := to.Truncate(info.Size()); err != nil {
		return err
	}
	after, err := from.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("the source disk changed during capture")
	}
	want, err := filedigest.Sum(ctx, from)
	if err != nil {
		return err
	}
	got, err := filedigest.Sum(ctx, to)
	if err != nil {
		return err
	}
	if want != got {
		return fmt.Errorf("the copied project disk failed checksum verification")
	}
	if err := to.Close(); err != nil {
		return err
	}
	if err := atomicfile.Commit(to.Name(), filepath.Join(dir, "disk.ext4")); err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(dir, "created"), []byte(target.ID), 0600); err != nil {
		return err
	}
	return providerutil.WriteCopyCompletion(filepath.Join(dir, "copy-complete"), source.ID, target.ID, target.SizeGiB, got)
}
