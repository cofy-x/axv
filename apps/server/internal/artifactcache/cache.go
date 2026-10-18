package artifactcache

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
)

const Format = 1
const namespace = "artifacts/v1"

type Cache struct{ Home string }
type Status struct {
	SHA256    string `json:"sha256"`
	State     string `json:"state"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	Name      string `json:"name,omitempty"`
}

func New(home string) (*Cache, error) {
	if !filepath.IsAbs(home) || strings.ContainsAny(home, "\x00\r\n") {
		return nil, fmt.Errorf("AXV_CACHE_HOME must be an absolute directory")
	}
	return &Cache{Home: filepath.Clean(home)}, nil
}

func privateDirectory(info os.FileInfo) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 || !owned(info) {
		return fmt.Errorf("cache directories must be real, owned by the current user and mode 0700")
	}
	return nil
}

func openDirectoryIn(r *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		if err := r.MkdirAll(name, 0700); err != nil {
			return nil, err
		}
	}
	before, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err = privateDirectory(before); err != nil {
		return nil, err
	}
	child, err := r.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := child.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		child.Close()
		return nil, fmt.Errorf("cache directory changed while opening")
	}
	return child, nil
}

func checkedFile(r *os.Root, name string, mode os.FileMode) (*os.File, error) {
	before, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || !owned(before) || before.Mode().Perm() != mode || !singleLink(before) {
		return nil, fmt.Errorf("cache file must be an owned regular file with mode %04o and one link", mode)
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	entry, ee := r.Lstat(name)
	if err != nil || ee != nil || !os.SameFile(before, after) || !os.SameFile(entry, after) {
		f.Close()
		return nil, fmt.Errorf("cache file changed while opening")
	}
	return f, nil
}

func openLock(r *os.Root, name string) (*os.File, error) {
	f, err := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if os.IsExist(err) {
		before, e := r.Lstat(name)
		if e != nil {
			return nil, e
		}
		if !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || !owned(before) || !singleLink(before) {
			return nil, fmt.Errorf("unsafe cache lock file")
		}
		f, err = r.OpenFile(name, os.O_RDWR, 0600)
		if err == nil {
			after, e := f.Stat()
			entry, ee := r.Lstat(name)
			if e != nil || ee != nil || !os.SameFile(before, after) || !os.SameFile(entry, after) {
				f.Close()
				return nil, fmt.Errorf("cache lock changed while opening")
			}
		}
	}
	return f, err
}

func acquire(ctx context.Context, r *os.Root, name string, wait bool) (*os.File, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	f, err := openLock(r, name)
	if err != nil {
		return nil, false, err
	}
	for {
		ok, e := tryLock(f)
		if e != nil {
			f.Close()
			return nil, false, e
		}
		if ok {
			info, statErr := f.Stat()
			entry, entryErr := r.Lstat(name)
			if statErr != nil || entryErr != nil || !entry.Mode().IsRegular() || !os.SameFile(info, entry) || !owned(info) || !singleLink(info) || info.Mode().Perm() != 0600 {
				f.Close()
				return nil, false, fmt.Errorf("cache lock changed while acquiring")
			}
			return f, true, nil
		}
		if !wait {
			f.Close()
			return nil, false, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (c *Cache) prepare(ctx context.Context) (*os.Root, error) {
	if err := os.MkdirAll(c.Home, 0700); err != nil {
		return nil, err
	}
	before, err := os.Lstat(c.Home)
	if err != nil {
		return nil, err
	}
	if err = privateDirectory(before); err != nil {
		return nil, fmt.Errorf("%s: %w", c.Home, err)
	}
	r, err := os.OpenRoot(c.Home)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*os.Root, error) { r.Close(); return nil, e }
	after, err := r.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		return fail(fmt.Errorf("cache root changed while opening"))
	}
	lock, _, err := acquire(ctx, r, ".init.lock", true)
	if err != nil {
		return fail(err)
	}
	defer lock.Close()
	marker, err := checkedFile(r, "format.json", 0600)
	if os.IsNotExist(err) {
		d, e := r.Open(".")
		if e != nil {
			return fail(e)
		}
		entries, e := d.ReadDir(-1)
		d.Close()
		if e != nil {
			return fail(e)
		}
		for _, entry := range entries {
			if entry.Name() != ".init.lock" {
				return fail(fmt.Errorf("cache root is nonempty without an AXV format marker; choose another AXV_CACHE_HOME"))
			}
		}
		if err = atomicJSON(r, "format.json", struct {
			Version int `json:"version"`
		}{Format}); err != nil {
			return fail(err)
		}
	} else if err != nil {
		return fail(err)
	} else {
		var saved struct {
			Version int `json:"version"`
		}
		decoder := json.NewDecoder(io.LimitReader(marker, 1024))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&saved)
		if err == nil && decoder.Decode(new(any)) != io.EOF {
			err = fmt.Errorf("cache marker must contain one object")
		}
		marker.Close()
		if err != nil || saved.Version != Format {
			return fail(fmt.Errorf("unsupported or damaged AXV cache format in %s", c.Home))
		}
	}
	for _, dir := range []string{"artifacts", namespace, namespace + "/sha256", namespace + "/locks"} {
		child, e := openDirectoryIn(r, dir, true)
		if e != nil {
			return fail(e)
		}
		child.Close()
	}
	return r, nil
}

func (c *Cache) path(sum string) string {
	return filepath.Join(c.Home, namespace, "sha256", sum, "artifact")
}

func (c *Cache) damaged(sum string, err error) error {
	return fmt.Errorf("cache entry %s is invalid: %w; repair explicitly with axv cache remove %s (cache: %s)", sum, err, sum, c.Home)
}

func checkEntry(r *os.Root) error {
	d, err := r.Open(".")
	if err != nil {
		return err
	}
	entries, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		mode := os.FileMode(0600)
		if entry.Name() == "artifact" {
			mode = 0444
		}
		switch entry.Name() {
		case "artifact", "source.json", "download.part", "download.part.json":
		default:
			if !metadataFile(entry.Name()) {
				return fmt.Errorf("unrecognized file in cache entry")
			}
		}
		f, e := checkedFile(r, entry.Name(), mode)
		if e != nil {
			return e
		}
		f.Close()
	}
	return nil
}

func (c *Cache) statusIn(ctx context.Context, r *os.Root, a Artifact) (Status, error) {
	result := Status{SHA256: a.SHA256, State: "missing", Path: c.path(a.SHA256), Name: a.Name}
	entry, err := openDirectoryIn(r, namespace+"/sha256/"+a.SHA256, false)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, c.damaged(a.SHA256, err)
	}
	defer entry.Close()
	if err = checkEntry(entry); err != nil {
		return result, c.damaged(a.SHA256, err)
	}
	var source Artifact
	if metadataErr := readJSON(entry, "source.json", &source); metadataErr == nil {
		if metadataErr = source.Validate(); metadataErr != nil || source.SHA256 != a.SHA256 {
			return result, c.damaged(a.SHA256, fmt.Errorf("cache source metadata does not match the pinned artifact"))
		}
	} else if !os.IsNotExist(metadataErr) {
		return result, c.damaged(a.SHA256, metadataErr)
	} else {
		for _, name := range []string{"artifact", "download.part"} {
			if _, existsErr := entry.Lstat(name); existsErr == nil {
				return result, c.damaged(a.SHA256, fmt.Errorf("cache source metadata is missing"))
			}
		}
	}
	f, err := checkedFile(entry, "artifact", 0444)
	if os.IsNotExist(err) {
		if _, e := entry.Lstat("download.part"); e == nil {
			result.State = "partial"
		}
		return result, nil
	}
	if err != nil {
		return result, c.damaged(a.SHA256, err)
	}
	defer f.Close()
	if err = verifyOpen(ctx, f, a.SHA256, a.MaxBytes); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, c.damaged(a.SHA256, err)
	}
	info, err := f.Stat()
	if err != nil {
		return result, err
	}
	result.State, result.SizeBytes = "ready", info.Size()
	return result, nil
}

func (c *Cache) Status(ctx context.Context, a Artifact) (Status, error) {
	if err := a.Validate(); err != nil {
		return Status{}, err
	}
	r, err := c.prepare(ctx)
	if err != nil {
		return Status{}, err
	}
	defer r.Close()
	lock, ok, err := acquire(ctx, r, namespace+"/locks/"+a.SHA256+".lock", false)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{SHA256: a.SHA256, State: "in_use", Path: c.path(a.SHA256), Name: a.Name}, nil
	}
	defer lock.Close()
	return c.statusIn(ctx, r, a)
}

// CopyTo serializes one digest across processes, verifies it after acquiring the
// lock, then copies independent bytes into the profile's existing destination.
// source may only be supplied by a caller that matched a reviewed official digest.
func (c *Cache) CopyTo(ctx context.Context, client *http.Client, a Artifact, destination, partial, source string, progress Progress) (result Status, resultErr error) {
	logger := artifactLogger(ctx, a)
	ctx = diagnostics.Context(ctx, logger)
	logger.Info("artifact.cache.request", "profileCopy", destination != "")
	defer func() {
		if resultErr != nil {
			logger.Error("artifact.cache.failed", resultErr)
		} else {
			logger.Info("artifact.cache.complete", "state", result.State, "profileCopy", destination != "")
		}
	}()
	if err := a.Validate(); err != nil {
		return Status{}, err
	}
	r, err := c.prepare(ctx)
	if err != nil {
		return Status{}, err
	}
	defer r.Close()
	if destination != "" {
		if !filepath.IsAbs(destination) {
			return Status{}, fmt.Errorf("profile destination must be absolute")
		}
		directory, pathErr := resolveDirectory(filepath.Dir(destination))
		cacheRoot, cacheErr := filepath.EvalSymlinks(c.Home)
		if pathErr != nil {
			return Status{}, pathErr
		}
		if cacheErr != nil {
			return Status{}, cacheErr
		}
		rel, pathErr := filepath.Rel(cacheRoot, directory)
		if pathErr != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return Status{}, fmt.Errorf("profile destination must be outside AXV_CACHE_HOME, including symlinked paths")
		}
	}
	lock, _, err := acquire(ctx, r, namespace+"/locks/"+a.SHA256+".lock", true)
	if err != nil {
		return Status{}, err
	}
	defer lock.Close()
	status, err := c.statusIn(ctx, r, a)
	if err != nil {
		return status, err
	}
	if status.State == "ready" {
		logger.Info("artifact.cache.hit", "bytes", status.SizeBytes)
	} else {
		logger.Info("artifact.cache.miss", "state", status.State)
	}
	if status.State != "ready" {
		entry, e := openDirectoryIn(r, namespace+"/sha256/"+a.SHA256, true)
		if e != nil {
			return status, e
		}
		defer entry.Close()
		if source == "" {
			if _, _, e = resumeDownload(entry, a, "download.part"); e != nil {
				if errors.Is(e, ErrIntegrity) {
					return status, c.damaged(a.SHA256, e)
				}
				return status, e
			}
		}
		if e = atomicJSON(entry, "source.json", a); e != nil {
			return status, e
		}
		if source != "" {
			e = copyInto(ctx, entry, source, "artifact", "download.part", a, progress)
		} else {
			e = fetchInto(ctx, client, a, entry, "artifact", "download.part", progress)
		}
		if e != nil {
			if errors.Is(e, ErrIntegrity) {
				return status, c.damaged(a.SHA256, e)
			}
			return status, e
		}
		f, e := entry.Open("artifact")
		if e != nil {
			return status, e
		}
		e = f.Chmod(0444)
		if e == nil {
			e = f.Sync()
		}
		f.Close()
		if e != nil {
			return status, e
		}
		status, err = c.statusIn(ctx, r, a)
		if err != nil {
			return status, err
		}
	}
	if destination == "" {
		return status, nil
	}
	// Hold both directory handles through validation and copying. Reopening an
	// absolute path after validation would allow an ancestor replacement to redirect it.
	output, err := openDirectory(destination, partial)
	if err != nil {
		return status, err
	}
	defer output.Close()
	outputInfo, err := output.Stat(".")
	if err != nil {
		return status, err
	}
	namedOutput, err := os.Stat(filepath.Dir(destination))
	if err != nil || !os.SameFile(outputInfo, namedOutput) {
		return status, fmt.Errorf("profile output directory changed while opening")
	}
	resolvedOutput, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return status, err
	}
	cacheInfo, err := r.Stat(".")
	if err != nil {
		return status, err
	}
	namedCache, err := os.Stat(c.Home)
	if err != nil || !os.SameFile(cacheInfo, namedCache) {
		return status, fmt.Errorf("cache directory changed during profile copy")
	}
	resolvedCache, err := filepath.EvalSymlinks(c.Home)
	if err != nil {
		return status, err
	}
	rel, err := filepath.Rel(resolvedCache, resolvedOutput)
	if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return status, fmt.Errorf("profile output resolves inside AXV_CACHE_HOME")
	}
	entry, err := openDirectoryIn(r, namespace+"/sha256/"+a.SHA256, false)
	if err != nil {
		return status, err
	}
	defer entry.Close()
	payload, err := checkedFile(entry, "artifact", 0444)
	if err != nil {
		return status, c.damaged(a.SHA256, err)
	}
	defer payload.Close()
	if existing, statErr := output.Lstat(filepath.Base(destination)); statErr == nil {
		if !existing.Mode().IsRegular() || !owned(existing) || !singleLink(existing) {
			return status, fmt.Errorf("existing profile artifact must be an independently owned regular file")
		}
		return status, verifyIn(ctx, output, filepath.Base(destination), a.SHA256, a.MaxBytes)
	} else if !os.IsNotExist(statErr) {
		return status, statErr
	}
	return status, copyFileInto(ctx, output, payload, filepath.Base(destination), filepath.Base(partial), a, progress)
}

func (c *Cache) List(ctx context.Context) ([]Status, error) {
	r, err := c.prepare(ctx)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	d, err := r.Open(namespace + "/sha256")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	result := []Status{}
	for _, entry := range entries {
		sum := entry.Name()
		if !validDigest(sum) {
			return nil, fmt.Errorf("unrecognized cache entry in %s", c.Home)
		}
		lock, ok, e := acquire(ctx, r, namespace+"/locks/"+sum+".lock", false)
		if e != nil {
			return nil, e
		}
		if !ok {
			result = append(result, Status{SHA256: sum, State: "in_use", Path: c.path(sum)})
			continue
		}
		er, e := openDirectoryIn(r, namespace+"/sha256/"+sum, false)
		if e != nil {
			lock.Close()
			return nil, c.damaged(sum, e)
		}
		var a Artifact
		e = checkEntry(er)
		if e == nil {
			e = readJSON(er, "source.json", &a)
		}
		er.Close()
		if e == nil && a.SHA256 != sum {
			e = fmt.Errorf("cache metadata digest does not match its directory")
		}
		if e == nil {
			e = a.Validate()
		}
		if e != nil {
			lock.Close()
			return nil, c.damaged(sum, e)
		}
		status, e := c.statusIn(ctx, r, a)
		lock.Close()
		if e != nil {
			return nil, e
		}
		result = append(result, status)
	}
	return result, nil
}

// Remove never unlinks lock files. A busy entry remains owned by its downloader.
func (c *Cache) Remove(ctx context.Context, sum string) error {
	if !validDigest(sum) {
		return fmt.Errorf("supply the full lowercase SHA-256")
	}
	r, err := c.prepare(ctx)
	if err != nil {
		return err
	}
	defer r.Close()
	lock, ok, err := acquire(ctx, r, namespace+"/locks/"+sum+".lock", false)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("cache entry is in use; retry explicit cleanup later")
	}
	defer lock.Close()
	name := namespace + "/sha256/" + sum
	entry, err := openDirectoryIn(r, name, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer entry.Close()
	d, err := entry.Open(".")
	if err != nil {
		return err
	}
	entries, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return err
	}
	for _, file := range entries {
		switch file.Name() {
		case "artifact", "source.json", "download.part", "download.part.json":
		default:
			if !metadataFile(file.Name()) {
				return fmt.Errorf("cache entry contains unknown files; automatic removal refused")
			}
		}
		info, e := entry.Lstat(file.Name())
		if e != nil {
			return e
		}
		if (!info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0) || !owned(info) {
			return fmt.Errorf("cache entry contains unowned or unsupported files; automatic removal refused")
		}
	}
	for _, file := range entries {
		if err = entry.Remove(file.Name()); err != nil {
			return err
		}
	}
	return r.Remove(name)
}

func metadataFile(name string) bool {
	suffix, ok := strings.CutPrefix(name, ".metadata-")
	if !ok || len(suffix) != 32 || suffix != strings.ToLower(suffix) {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}

func resolveDirectory(path string) (string, error) {
	ancestor := path
	for {
		info, err := os.Stat(ancestor)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("profile output parent is not a directory")
			}
			real, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(ancestor, path)
			if err != nil {
				return "", err
			}
			return filepath.Join(real, rel), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", err
		}
		ancestor = parent
	}
}
