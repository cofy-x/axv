//go:build linux && amd64

package hostinstall

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
)

const firecrackerVersion = "1.17.0"
const runtimeSuffix = "-v" + firecrackerVersion + "-x86_64"
const runtimePrefix = "release" + runtimeSuffix + "/"

// These amd64 inputs are the reviewed combination from the native Firecracker
// acceptance. Preserve the archive digest as well as each installed binary.
var runtimeArchive = artifactcache.Artifact{
	ID: "firecracker-amd64", Name: "Firecracker " + firecrackerVersion + " for Linux amd64",
	URL:    "https://github.com/firecracker-microvm/firecracker/releases/download/v" + firecrackerVersion + "/firecracker" + runtimeSuffix + ".tgz",
	SHA256: "06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558",
	Bytes:  7464385, MaxBytes: 7464385,
}

type archiveFile struct {
	name, digest string
	limit        int64
	mode         os.FileMode
}

var runtimeFiles = map[string]archiveFile{
	runtimePrefix + "firecracker" + runtimeSuffix: {"firecracker", "99ad0f5cd0514a88aad0e9ae8cfdb3cc3b4ab9d190e1194602406c786b5de7a5", 16 << 20, 0755},
	runtimePrefix + "jailer" + runtimeSuffix:      {"jailer", "65ef226e96f0ceda55ba643f445801ef2cc0ea667ef67cad8ac4f406c9c8434f", 16 << 20, 0755},
}

func stageRuntime(ctx context.Context, dir string) error {
	return stageRuntimeRelease(ctx, dir, nil, runtimeArchive, runtimeFiles)
}

// dir is private to the current installation. Only verified executables are
// returned to the installer. The compressed download and receipts are temporary.
func stageRuntimeRelease(ctx context.Context, dir string, client *http.Client, archive artifactcache.Artifact, files map[string]archiveFile) error {
	tmp, err := os.MkdirTemp(dir, ".runtime-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	path := filepath.Join(tmp, "release.tgz")
	if err = artifactcache.FetchArtifact(ctx, client, archive, path, path+".part", "", nil); err != nil {
		return fmt.Errorf("download Firecracker %s: %w", firecrackerVersion, err)
	}
	return extractArchive(ctx, path, dir, 64<<20, files)
}

// Extract only reviewed members and never use an archive-supplied output path,
// permissions or symlink. The caller has already verified the full download.
func extractArchive(ctx context.Context, path, dir string, expandedLimit int64, files map[string]archiveFile) (resultErr error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	bounded := &io.LimitedReader{R: gz, N: expandedLimit + 1}
	tr := tar.NewReader(bounded)
	written := make(map[string]bool, len(files))
	defer func() {
		if resultErr != nil {
			for name := range written {
				os.Remove(filepath.Join(dir, name))
			}
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tr.Next()
		if bounded.N == 0 {
			return fmt.Errorf("archive exceeds the unpacked size limit")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		file, selected := files[header.Name]
		if !selected {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > file.limit || written[file.name] {
			return fmt.Errorf("invalid or duplicate archive member %q", header.Name)
		}
		path := filepath.Join(dir, file.name)
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		written[file.name] = true
		_, err = artifactcache.CopyProgress(ctx, out, tr, 0, header.Size, header.Size, nil)
		if err == nil {
			err = out.Chmod(file.mode)
		}
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if file.digest != "" {
			err = artifactcache.VerifyFile(ctx, path, file.digest, file.limit)
		}
		if err != nil {
			return fmt.Errorf("verify %s: %w", file.name, err)
		}
	}
	if len(written) != len(files) {
		return fmt.Errorf("archive is missing a required file")
	}
	return nil
}
