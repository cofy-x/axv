// Package artifactcache owns verified immutable downloads independently of AXV
// profiles, virtual machines and desktop state.
package artifactcache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/diskspace"
	"github.com/cofy-x/axv/apps/server/internal/filedigest"
)

var ErrIntegrity = errors.New("artifact integrity mismatch")

type Artifact struct {
	ID, Name, URL, SHA256 string
	OriginURL             string `json:",omitempty"`
	MaxBytes, Bytes       int64
}

type Progress func(done, total int64)

type DownloadReceipt struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	ETag   string `json:"etag,omitempty"`
}

func DownloadClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Hour, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 30 * time.Second}, CheckRedirect: checkRedirect}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" || req.URL.Hostname() == "" || req.URL.User != nil || len(via) >= 5 {
		return fmt.Errorf("unsafe or excessive artifact download redirects")
	}
	return nil
}

func validDigest(sum string) bool {
	b, e := hex.DecodeString(sum)
	return e == nil && len(b) == 32 && sum == strings.ToLower(sum)
}

func (a Artifact) Validate() error {
	u, err := url.Parse(a.URL)
	if !validDigest(a.SHA256) || a.MaxBytes <= 0 || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("artifact requires a pinned SHA-256, bounded size and credential-free HTTPS source")
	}
	return nil
}

func VerifyFile(ctx context.Context, path, sum string, maxBytes int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("artifact must be a regular file: %s", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, actual) {
		return fmt.Errorf("artifact changed while opening")
	}
	return verifyOpen(ctx, f, sum, maxBytes)
}

func verifyOpen(ctx context.Context, f *os.File, sum string, maxBytes int64) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return fmt.Errorf("artifact is not a bounded regular file")
	}
	actual, err := filedigest.Sum(ctx, f)
	if err != nil {
		return err
	}
	if actual != sum {
		return fmt.Errorf("%w: SHA-256 mismatch for %s; existing bytes were preserved", ErrIntegrity, filepath.Base(f.Name()))
	}
	return nil
}

func verifyIn(ctx context.Context, r *os.Root, name, sum string, limit int64) error {
	before, err := r.Lstat(name)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("artifact must be a regular file")
	}
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) {
		return fmt.Errorf("artifact changed while opening")
	}
	return verifyOpen(ctx, f, sum, limit)
}

func openDirectory(destination, partial string) (*os.Root, error) {
	if filepath.Clean(destination) == filepath.Clean(partial) || filepath.Dir(destination) != filepath.Dir(partial) {
		return nil, fmt.Errorf("artifact staging must be beside its destination")
	}
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	before, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("artifact directory must be a real directory")
	}
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	after, err := r.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		r.Close()
		return nil, fmt.Errorf("artifact directory changed while opening")
	}
	return r, nil
}

func FetchArtifact(ctx context.Context, client *http.Client, a Artifact, destination, partial, source string, progress Progress) error {
	r, err := openDirectory(destination, partial)
	if err != nil {
		return err
	}
	defer r.Close()
	if _, err = r.Lstat(filepath.Base(destination)); err == nil {
		return verifyIn(ctx, r, filepath.Base(destination), a.SHA256, a.MaxBytes)
	} else if !os.IsNotExist(err) {
		return err
	}
	if source != "" {
		return copyInto(ctx, r, source, filepath.Base(destination), filepath.Base(partial), a, progress)
	}
	return fetchInto(ctx, client, a, r, filepath.Base(destination), filepath.Base(partial), progress)
}

func CopyPinned(ctx context.Context, source, destination, partial string, a Artifact, progress Progress) error {
	r, err := openDirectory(destination, partial)
	if err != nil {
		return err
	}
	defer r.Close()
	if _, err = r.Lstat(filepath.Base(destination)); err == nil {
		return verifyIn(ctx, r, filepath.Base(destination), a.SHA256, a.MaxBytes)
	} else if !os.IsNotExist(err) {
		return err
	}
	return copyInto(ctx, r, source, filepath.Base(destination), filepath.Base(partial), a, progress)
}

func atomicJSON(r *os.Root, name string, value any) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := ".metadata-" + hex.EncodeToString(nonce[:])
	f, err := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	if err = json.NewEncoder(f).Encode(value); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return r.Rename(tmp, name)
}

func readJSON(r *os.Root, name string, target any) error {
	before, err := r.Lstat(name)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("artifact metadata is not a regular file")
	}
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return fmt.Errorf("artifact metadata changed while opening")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("artifact metadata must contain one JSON object")
	}
	return nil
}

func openPartial(r *os.Root, name string, offset int64, source os.FileInfo) (*os.File, error) {
	before, err := r.Lstat(name)
	if os.IsNotExist(err) {
		if offset != 0 {
			return nil, fmt.Errorf("download partial disappeared")
		}
		return r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	}
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || !owned(before) || !singleLink(before) || before.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("download partial must be an independently owned regular file with mode 0600")
	}
	f, err := r.OpenFile(name, os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	after, e := f.Stat()
	entry, ee := r.Lstat(name)
	if e != nil || ee != nil || !entry.Mode().IsRegular() || !os.SameFile(before, after) || !os.SameFile(entry, after) || !owned(after) || !singleLink(after) || after.Mode().Perm() != 0600 {
		f.Close()
		return nil, fmt.Errorf("download partial changed while opening")
	}
	if source != nil && os.SameFile(source, after) {
		f.Close()
		return nil, fmt.Errorf("import source must be separate from artifact staging")
	}
	if offset == 0 {
		err = f.Truncate(0)
	} else if after.Size() != offset {
		err = fmt.Errorf("download partial changed size before resuming")
	}
	if err == nil {
		_, err = f.Seek(offset, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func publish(ctx context.Context, r *os.Root, partial, destination, sum string, limit int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Linking only publishes a newly copied file within this one directory. Cache
	// payloads are never hard-linked to profiles; CopyPinned creates a new inode.
	if err := r.Link(partial, destination); err != nil {
		if !os.IsExist(err) {
			return err
		}
		return verifyIn(ctx, r, destination, sum, limit)
	}
	if err := r.Remove(partial); err != nil {
		return err
	}
	d, err := r.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func copyInto(ctx context.Context, r *os.Root, source, destination, partial string, a Artifact, progress Progress) error {
	before, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("artifact source must be a regular file")
	}
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return fmt.Errorf("artifact source changed while opening")
	}
	return copyFileInto(ctx, r, f, destination, partial, a, progress)
}

func copyFileInto(ctx context.Context, r *os.Root, f *os.File, destination, partial string, a Artifact, progress Progress) (resultErr error) {
	logger := artifactLogger(ctx, a)
	logger.Info("artifact.copy.start")
	defer func() {
		if resultErr != nil {
			logger.Error("artifact.copy.failed", resultErr)
		} else {
			logger.Info("artifact.copy.complete")
		}
	}()
	progress = loggedProgress(logger, "artifact.copy.progress", progress)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := verifyOpen(ctx, f, a.SHA256, a.MaxBytes); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	out, err := openPartial(r, partial, 0, info)
	if err != nil {
		return err
	}
	_, err = CopyProgress(ctx, out, f, 0, info.Size(), a.MaxBytes, progress)
	if e := out.Sync(); err == nil {
		err = e
	}
	if e := out.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = verifyIn(ctx, r, partial, a.SHA256, a.MaxBytes); err != nil {
		return err
	}
	return publish(ctx, r, partial, destination, a.SHA256, a.MaxBytes)
}

func resumeDownload(r *os.Root, a Artifact, partial string) (DownloadReceipt, int64, error) {
	receipt := DownloadReceipt{URL: a.URL, SHA256: a.SHA256}
	var offset int64
	if info, err := r.Lstat(partial); err == nil {
		if !info.Mode().IsRegular() || info.Size() > a.MaxBytes {
			return receipt, 0, fmt.Errorf("%w: invalid download partial", ErrIntegrity)
		}
		var saved DownloadReceipt
		if err = readJSON(r, partial+".json", &saved); err != nil || saved.URL != a.URL || saved.SHA256 != a.SHA256 {
			return receipt, 0, fmt.Errorf("%w: download partial belongs to another source or digest; explicitly remove the inactive cache entry before switching sources", ErrIntegrity)
		}
		receipt = saved
		offset = info.Size()
	} else if !os.IsNotExist(err) {
		return receipt, 0, err
	}
	return receipt, offset, nil
}

func fetchInto(ctx context.Context, client *http.Client, a Artifact, r *os.Root, destination, partial string, progress Progress) (resultErr error) {
	logger := artifactLogger(ctx, a)
	logger.Info("artifact.download.start")
	defer func() {
		if resultErr != nil {
			logger.Error("artifact.download.failed", resultErr)
		} else {
			logger.Info("artifact.download.complete")
		}
	}()
	progress = loggedProgress(logger, "artifact.download.progress", progress)
	if err := a.Validate(); err != nil {
		return err
	}
	if client == nil {
		client = DownloadClient()
		defer client.CloseIdleConnections()
	}
	// Keep caller transport and cancellation settings, while retaining the artifact
	// redirect policy even when a client supplies its own certificate roots.
	guardedClient := *client
	guardedClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := checkRedirect(req, via); err != nil {
			return err
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		return nil
	}
	receipt, offset, err := resumeDownload(r, a, partial)
	if err != nil {
		return err
	}
	if offset > 0 {
		logger.Info("artifact.download.resume", "bytes", offset)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return fmt.Errorf("invalid HTTPS artifact source")
	}
	request.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		if receipt.ETag != "" {
			request.Header.Set("If-Range", receipt.ETag)
		}
	}
	response, err := guardedClient.Do(request)
	if err != nil {
		logger.Error("artifact.download.transport_failed", err)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("artifact download failed; check HTTPS connectivity, unsafe redirects and proxy configuration")
	}
	defer response.Body.Close()
	logger.Info("artifact.download.response", "status", response.StatusCode)
	var total int64
	switch response.StatusCode {
	case http.StatusOK:
		offset = 0
		if response.ContentLength > 0 {
			total = response.ContentLength
		}
	case http.StatusPartialContent:
		if offset == 0 {
			return fmt.Errorf("unexpected partial response for a new artifact")
		}
		var start, end int64
		if _, err = fmt.Sscanf(response.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start != offset || end < start || total <= end || total > a.MaxBytes {
			return fmt.Errorf("invalid artifact Content-Range")
		}
		if response.ContentLength >= 0 && response.ContentLength != end-start+1 {
			return fmt.Errorf("artifact range length disagrees with Content-Length")
		}
	case http.StatusRequestedRangeNotSatisfiable:
		if offset > 0 && verifyIn(ctx, r, partial, a.SHA256, a.MaxBytes) == nil {
			return publish(ctx, r, partial, destination, a.SHA256, a.MaxBytes)
		}
		return fmt.Errorf("artifact server rejected the retained partial; explicitly remove this cache entry before restarting")
	default:
		return fmt.Errorf("artifact download returned HTTP %d", response.StatusCode)
	}
	if total > a.MaxBytes {
		return fmt.Errorf("artifact download exceeds its size limit")
	}
	needed := total - offset
	if needed <= 0 {
		needed = min(a.MaxBytes, 512<<20)
	}
	if err = diskspace.Require(r.Name(), needed); err != nil {
		return err
	}
	receipt.ETag = response.Header.Get("ETag")
	if err = atomicJSON(r, partial+".json", receipt); err != nil {
		return err
	}
	f, err := openPartial(r, partial, offset, nil)
	if err != nil {
		return err
	}
	if progress != nil {
		progress(offset, total)
	}
	done, err := CopyProgress(ctx, f, response.Body, offset, total, a.MaxBytes, progress)
	if e := f.Sync(); err == nil {
		err = e
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if total > 0 && done != total {
		return fmt.Errorf("artifact download ended before declared length; retry resumes the saved partial")
	}
	if err = verifyIn(ctx, r, partial, a.SHA256, a.MaxBytes); err != nil {
		return err
	}
	if err = publish(ctx, r, partial, destination, a.SHA256, a.MaxBytes); err != nil {
		return err
	}
	_ = r.Remove(partial + ".json")
	return nil
}

func artifactLogger(ctx context.Context, a Artifact) *diagnostics.Logger {
	return diagnostics.FromContext(ctx).With("artifactId", a.ID, "sha256", a.SHA256, "source", a.URL)
}

func loggedProgress(logger *diagnostics.Logger, event string, progress Progress) Progress {
	if logger == nil {
		return progress
	}
	var last time.Time
	return func(done, total int64) {
		if progress != nil {
			progress(done, total)
		}
		now := time.Now()
		if now.Sub(last) >= time.Second {
			logger.Info(event, "bytes", done, "totalBytes", total)
			last = now
		}
	}
}

func CopyProgress(ctx context.Context, w io.Writer, r io.Reader, done, total, limit int64, progress Progress) (int64, error) {
	buf := make([]byte, 256<<10)
	last := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		n, readErr := r.Read(buf)
		if n > 0 {
			if done+int64(n) > limit {
				return done, fmt.Errorf("artifact exceeds its size limit")
			}
			written, err := w.Write(buf[:n])
			done += int64(written)
			if err != nil {
				return done, err
			}
			if written != n {
				return done, io.ErrShortWrite
			}
			if progress != nil && time.Since(last) >= 200*time.Millisecond {
				progress(done, total)
				last = time.Now()
			}
		}
		if readErr == io.EOF {
			if progress != nil {
				progress(done, total)
			}
			return done, nil
		}
		if readErr != nil {
			return done, readErr
		}
	}
}
