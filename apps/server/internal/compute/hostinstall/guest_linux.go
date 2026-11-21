//go:build linux && amd64

package hostinstall

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/artifacts"
	"github.com/cofy-x/axv/apps/server/internal/diskspace"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"golang.org/x/sys/unix"
)

const defaultImagesDir = "/opt/axv/images"

func guestDirectory(release artifacts.FirecrackerGuest) string {
	return filepath.Join(defaultImagesDir, release.Bundle.ID)
}

func defaultGuestImage(release artifacts.FirecrackerGuest) compute.Image {
	bundle, dir := release.Bundle, guestDirectory(release)
	return compute.Image{
		ID: bundle.ID, Name: bundle.Name, Provider: bundle.Provider, Arch: bundle.Arch,
		Rootfs: filepath.Join(dir, bundle.Rootfs.File), RootfsSHA256: bundle.Rootfs.SHA256,
		Kernel: filepath.Join(dir, "vmlinux"), KernelSHA256: release.Kernel.SHA256,
		Guest: bundle.Guest, MinRootGiB: 2,
	}
}

func (fs installFS) prepareGuest(ctx context.Context, release artifacts.FirecrackerGuest, out io.Writer) error {
	if err := release.Bundle.Validate(); err != nil {
		return err
	}
	if release.Bundle.Guest == nil {
		return fmt.Errorf("default image is missing the AXV Guest contract")
	}
	if err := release.Bundle.Guest.Compatible(); err != nil {
		return err
	}
	name := guestDirectory(release)
	if err := fs.directories(defaultImagesDir, true); err != nil {
		return err
	}
	destination := fs.path(name)
	if _, err := os.Lstat(destination); err == nil {
		if err := fs.directories(name, false); err != nil {
			return err
		}
		return fs.verifyGuest(ctx, destination, release)
	} else if !os.IsNotExist(err) {
		return err
	}

	parent := fs.path(defaultImagesDir)
	if err := diskspace.Require(parent, release.RootfsBytes+release.Archive.Bytes+release.Kernel.Bytes); err != nil {
		return fmt.Errorf("prepare default image in %s: %w", parent, err)
	}
	stage, err := os.MkdirTemp(parent, ".axv-guest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, "release.tar.gz")
	if err = downloadGuestArtifact(ctx, release.Archive, archive, out); err != nil {
		return fmt.Errorf("download default Guest: %w", err)
	}
	kernel := filepath.Join(stage, "vmlinux")
	if err = downloadGuestArtifact(ctx, release.Kernel, kernel, out); err != nil {
		return fmt.Errorf("download Guest kernel: %w", err)
	}
	fmt.Fprintln(out, "Extracting and checking the Guest disk…")
	rootfs := release.Bundle.Rootfs.File
	if err = extractArchive(ctx, archive, stage, release.RootfsBytes+(32<<20), map[string]archiveFile{
		"manifest.json": {"manifest.json", "", 64 << 10, 0600},
		rootfs:          {rootfs, "", release.RootfsBytes, 0600},
	}); err != nil {
		return err
	}
	if err = verifyGuestDisk(ctx, stage, release); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = syncDirectory(stage); err != nil {
		return err
	}
	if err = unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return syncDirectory(parent)
}

func downloadGuestArtifact(ctx context.Context, artifact artifactcache.Artifact, path string, out io.Writer) error {
	fmt.Fprintf(out, "Downloading %s…\n", artifact.Name)
	last := time.Now()
	return artifactcache.FetchArtifact(ctx, nil, artifact, path, path+".part", "", func(done, _ int64) {
		if time.Since(last) >= 5*time.Second {
			fmt.Fprintf(out, "  %.0f / %.0f MiB\n", float64(done)/(1<<20), float64(artifact.Bytes)/(1<<20))
			last = time.Now()
		}
	})
}

func (fs installFS) verifyGuest(ctx context.Context, dir string, release artifacts.FirecrackerGuest) error {
	for _, name := range []string{"manifest.json", release.Bundle.Rootfs.File, "release.tar.gz", "vmlinux"} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if err = fs.trusted(path, info); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("expected a regular image file at %s", path)
		}
	}
	if err := artifactcache.VerifyFile(ctx, filepath.Join(dir, "release.tar.gz"), release.Archive.SHA256, release.Archive.MaxBytes); err != nil {
		return err
	}
	if err := artifactcache.VerifyFile(ctx, filepath.Join(dir, "vmlinux"), release.Kernel.SHA256, release.Kernel.MaxBytes); err != nil {
		return err
	}
	return verifyGuestDisk(ctx, dir, release)
}

func verifyGuestDisk(ctx context.Context, dir string, release artifacts.FirecrackerGuest) error {
	actual, err := guestimage.LoadBundle(ctx, filepath.Join(dir, "manifest.json"), "firecracker", "amd64")
	if err != nil {
		return err
	}
	info, err := os.Stat(actual.RootfsPath)
	if err != nil {
		return err
	}
	actual.RootfsPath = ""
	if info.Size() != release.RootfsBytes || !reflect.DeepEqual(actual, release.Bundle) {
		return fmt.Errorf("the unpacked Guest does not match the reviewed release")
	}
	return nil
}
