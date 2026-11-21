//go:build linux && amd64

package hostinstall

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
)

type runtimeTestEntry struct {
	path string
	body string
	kind byte
}

func runtimeTestArchive(t *testing.T, entries []runtimeTestEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tarFile := tar.NewWriter(gz)
	for _, entry := range entries {
		header := tar.Header{Name: entry.path, Typeflag: entry.kind, Size: int64(len(entry.body)), Mode: 0777}
		if entry.kind == tar.TypeSymlink {
			header.Linkname = "../elsewhere"
		}
		if err := tarFile.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarFile.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestStageRuntimeRelease(t *testing.T) {
	fc := runtimeTestEntry{runtimePrefix + "firecracker" + runtimeSuffix, "firecracker executable", tar.TypeReg}
	jailer := runtimeTestEntry{runtimePrefix + "jailer" + runtimeSuffix, "jailer executable", tar.TypeReg}
	files := map[string]archiveFile{
		fc.path:     {"firecracker", fmt.Sprintf("%x", sha256.Sum256([]byte(fc.body))), 100, 0755},
		jailer.path: {"jailer", fmt.Sprintf("%x", sha256.Sum256([]byte(jailer.body))), 100, 0755},
	}
	for _, tc := range []struct {
		name             string
		entries          []runtimeTestEntry
		corruptDownload  bool
		preserveExisting bool
	}{
		{"correct", []runtimeTestEntry{fc, {"other/firecracker", "ignored", tar.TypeReg}, jailer}, false, false},
		{"corrupt download", []runtimeTestEntry{fc, jailer}, true, false},
		{"missing binary", []runtimeTestEntry{fc}, false, false},
		{"wrong binary digest", []runtimeTestEntry{fc, {jailer.path, "other executable", tar.TypeReg}}, false, false},
		{"duplicate binary", []runtimeTestEntry{fc, jailer, fc}, false, false},
		{"symlink", []runtimeTestEntry{fc, {jailer.path, "", tar.TypeSymlink}}, false, false},
		{"existing binary", []runtimeTestEntry{fc, jailer}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := runtimeTestArchive(t, tc.entries)
			digest := sha256.Sum256(data)
			if tc.corruptDownload {
				digest[0] ^= 255
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(data)
			}))
			defer server.Close()
			dir := t.TempDir()
			saved := filepath.Join(dir, "jailer")
			if tc.preserveExisting {
				if err := os.WriteFile(saved, []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			archive := artifactcache.Artifact{URL: server.URL, SHA256: fmt.Sprintf("%x", digest), MaxBytes: int64(len(data))}
			err := stageRuntimeRelease(t.Context(), dir, server.Client(), archive, files)
			if tc.name == "correct" {
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range files {
					p := filepath.Join(dir, file.name)
					if err := artifactcache.VerifyFile(t.Context(), p, file.digest, 100); err != nil {
						t.Fatal(err)
					}
					if info, err := os.Stat(p); err != nil || info.Mode().Perm() != 0755 {
						t.Fatalf("runtime mode: %v, %v", info, err)
					}
				}
			} else if err == nil {
				t.Fatal("expected installation to fail")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			var got, want []string
			for _, entry := range entries {
				got = append(got, entry.Name())
			}
			if tc.name == "correct" {
				want = []string{"firecracker", "jailer"}
			} else if tc.preserveExisting {
				want = []string{"jailer"}
				body, err := os.ReadFile(saved)
				if err != nil || string(body) != "existing" {
					t.Fatalf("existing executable changed: %q, %v", body, err)
				}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("staging contains %v, want %v", got, want)
			}
		})
	}
}
