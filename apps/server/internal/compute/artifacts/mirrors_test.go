package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
)

func TestMirrorSelectionPreservesPinAndRejectsUnsafeConfiguration(t *testing.T) {
	original := Official().Ubuntu["arm64"]
	config, _ := json.Marshal(map[string]string{original.SHA256: "https://operator.invalid/existing-exact-image"})
	selected, err := MirrorArtifact(original, string(config))
	if err != nil || selected.OriginURL != original.URL || selected.SHA256 != original.SHA256 || selected.Bytes != original.Bytes || selected.MaxBytes != original.MaxBytes {
		t.Fatal(selected, err)
	}
	if selected.URL == original.URL {
		t.Fatal("mirror not selected")
	}
	for _, bad := range []string{"null", "[]", "{}{}", `{"short":"https://example.com"}`, `{"` + original.SHA256 + `":"https://user:secret@example.com"}`, `{"` + original.SHA256 + `":"http://example.com"}`, `{"` + original.SHA256 + `":"https://example.com?token=secret"}`} {
		if _, err := MirrorArtifact(original, bad); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe configuration %s: %v", bad, err)
		}
	}
}
func TestMirrorIntegrityAndWarmReuseAcrossSourceChoice(t *testing.T) {
	body := []byte("pinned fixture")
	sum := sha256.Sum256(body)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/bad" {
			w.Write([]byte("wrong"))
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	original := artifactcache.Artifact{ID: "fixture", URL: "https://origin.invalid/pinned", SHA256: hex.EncodeToString(sum[:]), MaxBytes: 1024}
	selectURL := func(location string) artifactcache.Artifact {
		config, _ := json.Marshal(map[string]string{original.SHA256: location})
		a, err := MirrorArtifact(original, string(config))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	cache, _ := artifactcache.New(filepath.Join(t.TempDir(), "cache"))
	if _, err := cache.CopyTo(context.Background(), server.Client(), selectURL(server.URL+"/good"), "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.CopyTo(context.Background(), server.Client(), original, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("warm cache consulted source")
	}
	badCache, _ := artifactcache.New(filepath.Join(t.TempDir(), "cache"))
	if _, err := badCache.CopyTo(context.Background(), server.Client(), selectURL(server.URL+"/bad"), "", "", "", nil); err == nil {
		t.Fatal("accepted wrong mirror bytes")
	}
	if err := badCache.Remove(context.Background(), original.SHA256); err != nil {
		t.Fatal(err)
	}
}

func TestChangingMirrorDoesNotJoinPartialResponses(t *testing.T) {
	body := []byte(strings.Repeat("source-bound-data ", 4096))
	sum := sha256.Sum256(body)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.ServeContent(w, r, "fixture", time.Time{}, strings.NewReader(string(body)))
	}))
	defer server.Close()
	original := artifactcache.Artifact{ID: "fixture", URL: server.URL + "/original", SHA256: hex.EncodeToString(sum[:]), MaxBytes: 1 << 20}
	cache, _ := artifactcache.New(filepath.Join(t.TempDir(), "cache"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := cache.CopyTo(ctx, server.Client(), original, "", "", "", func(done, total int64) {
		if done > 0 {
			cancel()
		}
	}); err == nil {
		t.Fatal("cancellation ignored")
	}
	config, _ := json.Marshal(map[string]string{original.SHA256: server.URL + "/mirror"})
	mirrored, err := MirrorArtifact(original, string(config))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cache.CopyTo(context.Background(), server.Client(), mirrored, "", "", "", nil); err == nil || !strings.Contains(err.Error(), "another source") {
		t.Fatal("partial lost origin binding", err)
	}
	if !strings.Contains(err.Error(), "axv cache remove "+original.SHA256) {
		t.Fatal("source conflict omitted the exact repair command")
	}
	if requests.Load() != 1 {
		t.Fatal("changed source was requested with an old partial")
	}
	status, err := cache.Status(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(status.Path), "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	var retained artifactcache.Artifact
	if err = json.Unmarshal(raw, &retained); err != nil || retained.URL != original.URL {
		t.Fatalf("failed source switch rewrote retained source metadata: %+v, %v", retained, err)
	}
	if _, err = cache.CopyTo(context.Background(), server.Client(), original, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatal("original source did not resume exactly once")
	}
}

func TestMirrorCatalogRetainsItsOriginalSource(t *testing.T) {
	original := Official()
	a := original.Ubuntu["arm64"]
	config, _ := json.Marshal(map[string]string{a.SHA256: "https://operator.invalid/exact-image"})
	mirrored, err := WithMirrors(original, string(config))
	if err != nil {
		t.Fatal(err)
	}
	if original.Ubuntu["arm64"] != a {
		t.Fatal("mirror selection mutated the original catalog")
	}
	reapplied, err := WithMirrors(mirrored, string(config))
	if err != nil || reapplied.Ubuntu["arm64"].OriginURL != a.URL {
		t.Fatal("reapplying a mirror lost its reviewed original source", err)
	}
}
