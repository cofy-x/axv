package prepare

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

func pinned(data []byte) artifact {
	sum := sha256.Sum256(data)
	return artifact{ID: "test", Name: "test artifact", SHA256: hex.EncodeToString(sum[:]), MaxBytes: 1 << 20, Bytes: int64(len(data))}
}

func TestDownloadResumesValidatedRangeAndChecksWholeDigest(t *testing.T) {
	data := bytes.Repeat([]byte("pinned bytes\n"), 1024)
	cut := len(data) / 3
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"fixture-v1"`)
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data[:cut])
			return
		}
		if r.Header.Get("Range") != fmt.Sprintf("bytes=%d-", cut) || r.Header.Get("If-Range") != `"fixture-v1"` {
			t.Errorf("range headers: %v", r.Header)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", cut, len(data)-1, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)-cut))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[cut:])
	}))
	defer server.Close()
	a := pinned(data)
	a.URL = server.URL
	dir := t.TempDir()
	destination, partial := filepath.Join(dir, "image"), filepath.Join(dir, "partial")
	if err := fetchArtifact(context.Background(), server.Client(), a, destination, partial, "", nil); err == nil {
		t.Fatal("truncated response succeeded")
	}
	info, err := os.Stat(partial)
	if err != nil || info.Size() != int64(cut) {
		t.Fatalf("partial not retained: %v %v", info, err)
	}
	var done, total int64
	if err = fetchArtifact(context.Background(), server.Client(), a, destination, partial, "", func(d, n int64) { done, total = d, n }); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(destination)
	if !bytes.Equal(actual, data) || done != int64(len(data)) || total != int64(len(data)) {
		t.Fatal("range content or progress incorrect")
	}
	if err = fetchArtifact(context.Background(), server.Client(), a, destination, partial, "", nil); err != nil || requests.Load() != 2 {
		t.Fatal("verified cache downloaded again", err)
	}
}

func TestDownloadRestartsWhenRangeIgnored(t *testing.T) {
	data := []byte("complete pinned artifact")
	a := pinned(data)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			t.Error("missing resume range")
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	a.URL = server.URL
	dir := t.TempDir()
	partial := filepath.Join(dir, "partial")
	destination := filepath.Join(dir, "image")
	_ = os.WriteFile(partial, data[:5], 0600)
	_ = jsonfile.Write(partial+".json", downloadReceipt{URL: a.URL, SHA256: a.SHA256}, 0600)
	if err := fetchArtifact(context.Background(), server.Client(), a, destination, partial, "", nil); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(destination)
	if !bytes.Equal(actual, data) {
		t.Fatal("appended full response to partial")
	}
}

func TestDownloadRejectsInvalidRangeAndWrongHash(t *testing.T) {
	data := []byte("expected pinned artifact")
	a := pinned(data)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-9/20")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("wrong data"))
	}))
	defer server.Close()
	a.URL = server.URL
	dir := t.TempDir()
	partial := filepath.Join(dir, "partial")
	destination := filepath.Join(dir, "image")
	_ = os.WriteFile(partial, data[:3], 0600)
	_ = jsonfile.Write(partial+".json", downloadReceipt{URL: a.URL, SHA256: a.SHA256}, 0600)
	if err := fetchArtifact(context.Background(), server.Client(), a, destination, partial, "", nil); err == nil {
		t.Fatal("accepted invalid range")
	}
	wrong := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("wrong bytes")) }))
	defer wrong.Close()
	a.URL = wrong.URL
	_ = os.Remove(partial)
	_ = os.Remove(partial + ".json")
	if err := fetchArtifact(context.Background(), wrong.Client(), a, destination, partial, "", nil); err == nil {
		t.Fatal("accepted wrong digest")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("published unverified artifact")
	}
	if err := verifyFile(context.Background(), partial, a.SHA256, a.MaxBytes); err == nil {
		t.Fatal("corrupt partial passed integrity verification")
	}
}

func TestArtifactCacheCorruptionIsNeverReplaced(t *testing.T) {
	data := []byte("good pinned bytes")
	a := pinned(data)
	dir := t.TempDir()
	source, destination := filepath.Join(dir, "source"), filepath.Join(dir, "cache")
	_ = os.WriteFile(source, data, 0600)
	_ = os.WriteFile(destination, []byte("existing corrupt bytes"), 0600)
	if err := fetchArtifact(context.Background(), nil, a, destination, filepath.Join(dir, "partial"), source, nil); err == nil {
		t.Fatal("accepted corrupt cache")
	}
	b, _ := os.ReadFile(destination)
	if string(b) != "existing corrupt bytes" {
		t.Fatal("replaced existing cache")
	}
}

func TestPinnedCopyRefusesSymlinkPartialWithoutTouchingTarget(t *testing.T) {
	dir := t.TempDir()
	source, target, partial := filepath.Join(dir, "source"), filepath.Join(dir, "unrelated"), filepath.Join(dir, "partial")
	data := []byte("pinned artifact")
	_ = os.WriteFile(source, data, 0600)
	_ = os.WriteFile(target, []byte("preserve unrelated bytes"), 0600)
	if err := os.Symlink(target, partial); err != nil {
		t.Fatal(err)
	}
	if err := copyPinned(context.Background(), source, filepath.Join(dir, "destination"), partial, pinned(data), nil); err == nil {
		t.Fatal("accepted symlink partial")
	}
	actual, _ := os.ReadFile(target)
	if string(actual) != "preserve unrelated bytes" {
		t.Fatal("truncated symlink target")
	}
}

func TestDownloadRejectsHTTPRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/untrusted", http.StatusFound)
	}))
	defer server.Close()
	client := downloadClient()
	client.Transport = server.Client().Transport
	defer client.CloseIdleConnections()
	a := pinned([]byte("unused"))
	a.URL = server.URL
	dir := t.TempDir()
	if err := fetchArtifact(context.Background(), client, a, filepath.Join(dir, "cache"), filepath.Join(dir, "part"), "", nil); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("redirect accepted: %v", err)
	}
}

func TestPrivateInstallationUsesPersistedReviewedArtifact(t *testing.T) {
	archive := testArchive(t, []archiveEntry{
		{name: "bin/limactl", body: "#!/bin/sh\nprintf 'limactl version " + compute.SupportedLimaVersion + "\\n'\n"},
		{name: "share/lima/lima-guestagent.Linux-aarch64.gz", body: "native guest agent fixture"},
	})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/reviewed" {
			t.Errorf("downloaded unreviewed artifact: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	e, home := testEngine(t, nil)
	e.client = server.Client()
	reviewed := pinned(data)
	reviewed.ID, reviewed.URL = "lima", server.URL+"/reviewed"
	root := filepath.Join(home, "tools", "lima", "reviewed")
	spec := specification{Plan: Plan{ID: "plan-reviewed-artifact", Arch: "arm64"}, LimaArtifact: reviewed, LimaBinary: filepath.Join(root, "bin", "limactl"), LimaVersion: compute.SupportedLimaVersion}
	if err = e.store.savePlan(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	replacement := pinned([]byte("replacement catalog artifact"))
	replacement.URL = server.URL + "/replacement"
	e.catalog.Lima["arm64"] = replacement
	saved, err := e.store.plan(context.Background(), spec.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := e.installLima(context.Background(), "reviewed-artifact", saved.LimaArtifact, filepath.Dir(filepath.Dir(saved.LimaBinary)), saved.Plan.Arch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if binary != spec.LimaBinary || requests.Load() != 1 {
		t.Fatalf("reviewed installation changed: %s (%d downloads)", binary, requests.Load())
	}
	if err = verifyInstallation(context.Background(), root, reviewed.SHA256); err != nil {
		t.Fatal(err)
	}
}

type archiveEntry struct {
	name, body, link string
	kind             byte
}

func testArchive(t *testing.T, entries []archiveEntry) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "lima.tar.gz")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Typeflag: kind, Mode: 0755, Linkname: entry.link}
		if kind == tar.TypeReg {
			header.Size = int64(len(entry.body))
		}
		if err = writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err = writer.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestExtractionPreservesInternalLinksThroughSymlinkedAncestor(t *testing.T) {
	parent := t.TempDir()
	real := filepath.Join(parent, "real")
	alias := filepath.Join(parent, "alias")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(alias, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	archive := testArchive(t, []archiveEntry{{name: "bin/limactl", body: "binary"}, {name: "bin/lima", link: "limactl", kind: tar.TypeSymlink}, {name: "share/lima/templates/default.yaml", body: "template"}, {name: "share/doc/lima/LICENSE", body: "Apache-2.0"}, {name: "share/doc/lima/templates", link: "../../lima/templates", kind: tar.TypeSymlink}})
	receipt, err := extractLima(context.Background(), archive, stage, "archive-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = jsonfile.Write(filepath.Join(stage, "installation.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	if err = verifyInstallation(context.Background(), stage, "archive-hash"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "bin", "limactl"), []byte("tampered"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = verifyInstallation(context.Background(), stage, "archive-hash"); err == nil {
		t.Fatal("accepted changed private executable")
	}
}

func TestExtractionRejectsEscapesAndUnsupportedEntries(t *testing.T) {
	cases := map[string][]archiveEntry{
		"parent traversal": {{name: "../outside", body: "bad"}},
		"absolute":         {{name: "/tmp/axv-extract-escape", body: "bad"}},
		"escaping link":    {{name: "bin/lima", link: "../../outside", kind: tar.TypeSymlink}},
		"link as parent":   {{name: "actual/file", body: "safe"}, {name: "bin", link: "actual", kind: tar.TypeSymlink}, {name: "bin/overwrite", body: "bad"}},
		"hardlink":         {{name: "file", body: "safe"}, {name: "other", link: "file", kind: tar.TypeLink}},
		"device":           {{name: "device", kind: tar.TypeChar}},
		"duplicate":        {{name: "file", body: "first"}, {name: "file", body: "second"}},
		"owner marker":     {{name: ".axv-owner", body: "foreign"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			archive := testArchive(t, entries)
			if _, err := extractLima(context.Background(), archive, t.TempDir(), "digest"); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
