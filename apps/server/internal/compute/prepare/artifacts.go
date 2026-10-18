package prepare

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

type progressFunc = artifactcache.Progress
type downloadReceipt = artifactcache.DownloadReceipt

func downloadClient() *http.Client { return artifactcache.DownloadClient() }
func verifyFile(ctx context.Context, path, sum string, limit int64) error {
	return artifactcache.VerifyFile(ctx, path, sum, limit)
}
func fetchArtifact(ctx context.Context, client *http.Client, a artifact, destination, partial, source string, progress progressFunc) error {
	return artifactcache.FetchArtifact(ctx, client, a, destination, partial, source, progress)
}
func copyPinned(ctx context.Context, source, destination, partial string, a artifact, progress progressFunc) error {
	return artifactcache.CopyPinned(ctx, source, destination, partial, a, progress)
}

type installedFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Link   string `json:"link,omitempty"`
	Mode   uint32 `json:"mode"`
}
type installationReceipt struct {
	ArchiveSHA256 string          `json:"archiveSha256"`
	Files         []installedFile `json:"files"`
}

func (e *Engine) installLima(ctx context.Context, id string, a artifact, root, arch string, progress progressFunc) (string, error) {
	if !within(filepath.Join(e.options.Home, "tools"), root) || len(a.SHA256) != 64 || a.MaxBytes <= 0 {
		return "", fmt.Errorf("invalid reviewed Lima installation")
	}
	binary := filepath.Join(root, "bin", "limactl")
	if _, err := os.Lstat(root); err == nil {
		if err = verifyInstallation(ctx, root, a.SHA256); err != nil {
			return "", err
		}
		return binary, validateLima(ctx, binary, arch)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	tools := filepath.Join(e.options.Home, "tools")
	if err := owneddir.Ensure(tools, e.owner+":tools"); err != nil {
		return "", err
	}
	cache := filepath.Join(e.dir, "downloads")
	if err := os.MkdirAll(cache, 0700); err != nil {
		return "", err
	}
	archive := filepath.Join(cache, "lima-"+a.SHA256+".tar.gz")
	if _, err := e.cache.CopyTo(ctx, e.client, a, archive, filepath.Join(cache, id+"-lima.part"), "", progress); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return "", err
	}
	stage := root + ".staging-" + id
	if _, err := os.Lstat(stage); err == nil {
		if err = owneddir.Check(stage, e.owner+":"+id); err != nil {
			return "", err
		}
		if err = os.RemoveAll(stage); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := owneddir.Ensure(stage, e.owner+":"+id); err != nil {
		return "", err
	}
	receipt, err := extractLima(ctx, archive, stage, a.SHA256)
	if err != nil {
		return "", err
	}
	if err = validateLima(ctx, filepath.Join(stage, "bin", "limactl"), arch); err != nil {
		return "", err
	}
	if err = jsonfile.Write(filepath.Join(stage, "installation.json"), receipt, 0600); err != nil {
		return "", err
	}
	if _, err = os.Lstat(root); err == nil {
		return "", fmt.Errorf("private Lima destination appeared during installation; existing files were preserved")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err = os.Rename(stage, root); err != nil {
		return "", err
	}
	dir, err := os.Open(filepath.Dir(root))
	if err != nil {
		return "", err
	}
	err = dir.Sync()
	_ = dir.Close()
	return binary, err
}

// extractLima preserves the complete official prefix while confining every
// directory, file and relative symlink to its task-owned staging directory.
func extractLima(ctx context.Context, archive, destination, digest string) (installationReceipt, error) {
	receipt := installationReceipt{ArchiveSHA256: digest, Files: []installedFile{}}
	realDestination, resolveErr := filepath.EvalSymlinks(destination)
	if resolveErr != nil {
		return receipt, resolveErr
	}
	f, err := os.Open(archive)
	if err != nil {
		return receipt, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return receipt, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	for count := 0; ; count++ {
		if err = ctx.Err(); err != nil {
			return receipt, err
		}
		header, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return receipt, nextErr
		}
		if count > 30000 {
			return receipt, fmt.Errorf("Lima archive contains too many entries")
		}
		name := strings.TrimPrefix(header.Name, "./")
		name = path.Clean(name)
		if name == "." {
			continue
		}
		if name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) || strings.ContainsAny(name, "\\\x00") || name == ".axv-owner" || name == "installation.json" {
			return receipt, fmt.Errorf("unsafe Lima archive path %q", header.Name)
		}
		if seen[name] {
			return receipt, fmt.Errorf("duplicate Lima archive path %q", name)
		}
		seen[name] = true
		target := filepath.Join(destination, filepath.FromSlash(name))
		if !within(destination, target) {
			return receipt, fmt.Errorf("Lima archive path escapes staging")
		}
		if err = ensureRealParents(destination, filepath.Dir(target)); err != nil {
			return receipt, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0755); err != nil {
				return receipt, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > 256<<20 || total+header.Size > 512<<20 {
				return receipt, fmt.Errorf("Lima archive exceeds its unpacked size limit")
			}
			total += header.Size
			mode := os.FileMode(header.Mode) & 0777
			mode &^= 0022
			out, openErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if openErr != nil {
				return receipt, openErr
			}
			h := sha256.New()
			n, copyErr := artifactcache.CopyProgress(ctx, io.MultiWriter(out, h), io.LimitReader(tr, header.Size), 0, header.Size, header.Size, nil)
			if syncErr := out.Sync(); copyErr == nil {
				copyErr = syncErr
			}
			if closeErr := out.Close(); copyErr == nil {
				copyErr = closeErr
			}
			if copyErr != nil {
				return receipt, copyErr
			}
			if n != header.Size {
				return receipt, io.ErrUnexpectedEOF
			}
			receipt.Files = append(receipt.Files, installedFile{Path: name, SHA256: hex.EncodeToString(h.Sum(nil)), Mode: uint32(mode)})
		case tar.TypeSymlink:
			link := header.Linkname
			if path.IsAbs(link) || strings.ContainsAny(link, "\\\x00") {
				return receipt, fmt.Errorf("unsafe Lima archive symlink")
			}
			resolved := path.Clean(path.Join(path.Dir(name), link))
			if resolved == ".." || strings.HasPrefix(resolved, "../") {
				return receipt, fmt.Errorf("Lima archive symlink escapes staging")
			}
			if err = os.Symlink(link, target); err != nil {
				return receipt, err
			}
			receipt.Files = append(receipt.Files, installedFile{Path: name, Link: link})
		default:
			return receipt, fmt.Errorf("unsupported Lima archive entry type %d", header.Typeflag)
		}
	}
	if len(receipt.Files) == 0 {
		return receipt, fmt.Errorf("Lima archive is empty")
	}
	for _, file := range receipt.Files {
		if file.Link != "" {
			real, err := filepath.EvalSymlinks(filepath.Join(destination, filepath.FromSlash(file.Path)))
			if err != nil || !within(realDestination, real) {
				return receipt, fmt.Errorf("Lima archive contains a broken or escaping symlink")
			}
		}
	}
	return receipt, nil
}

func ensureRealParents(root, parent string) error {
	rel, err := filepath.Rel(root, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive parent escapes staging")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err = os.Mkdir(current, 0755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive parent is not a real directory")
		}
	}
	return nil
}

func verifyInstallation(ctx context.Context, root, digest string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private Lima installation is not a real directory")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(root, "installation.json"))
	if err != nil {
		return err
	}
	if len(b) > 8<<20 {
		return fmt.Errorf("Lima installation receipt exceeds its limit")
	}
	var receipt installationReceipt
	if err = json.Unmarshal(b, &receipt); err != nil {
		return err
	}
	if receipt.ArchiveSHA256 != digest || len(receipt.Files) == 0 {
		return fmt.Errorf("private Lima installation does not match the pinned archive")
	}
	for _, file := range receipt.Files {
		target := filepath.Join(root, filepath.FromSlash(file.Path))
		if !within(root, target) {
			return fmt.Errorf("invalid Lima installation receipt")
		}
		if file.Link != "" {
			link, err := os.Readlink(target)
			if err != nil || link != file.Link {
				return fmt.Errorf("private Lima symlink changed")
			}
			real, err := filepath.EvalSymlinks(target)
			if err != nil || !within(realRoot, real) {
				return fmt.Errorf("private Lima symlink escapes installation")
			}
		} else {
			if err = verifyFile(ctx, target, file.SHA256, 256<<20); err != nil {
				return err
			}
			info, err := os.Stat(target)
			if err != nil || uint32(info.Mode().Perm()) != file.Mode {
				return fmt.Errorf("private Lima file permissions changed")
			}
		}
	}
	return nil
}
