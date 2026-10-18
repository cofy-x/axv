package artifactcache

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, body []byte, handler http.HandlerFunc) (*httptest.Server, Artifact) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	digest := sha256.Sum256(body)
	return server, Artifact{ID: "fixture", Name: "Fixture", URL: server.URL, SHA256: hex.EncodeToString(digest[:]), MaxBytes: 1 << 20, Bytes: int64(len(body))}
}
func testCache(t *testing.T) *Cache {
	t.Helper()
	c, err := New(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestColdWarmIndependentProfilesAndMaintenance(t *testing.T) {
	body := []byte(strings.Repeat("verified bytes ", 8192))
	var requests atomic.Int32
	server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write(body) })
	c := testCache(t)
	paths := []string{filepath.Join(t.TempDir(), "image"), filepath.Join(t.TempDir(), "image")}
	for _, dest := range paths {
		status, err := c.CopyTo(context.Background(), server.Client(), a, dest, dest+".part", "", nil)
		if err != nil || status.State != "ready" {
			t.Fatalf("status=%+v err=%v", status, err)
		}
		cached, _ := os.Stat(status.Path)
		copied, _ := os.Stat(dest)
		if os.SameFile(cached, copied) {
			t.Fatal("profile aliases shared payload")
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("downloads=%d", requests.Load())
	}
	if err := os.RemoveAll(filepath.Dir(paths[0])); err != nil {
		t.Fatal(err)
	}
	if status, err := c.Status(context.Background(), a); err != nil || status.State != "ready" {
		t.Fatalf("profile cleanup damaged cache: %v", err)
	}
	if err := c.Remove(context.Background(), a.SHA256); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(context.Background(), paths[1], a.SHA256, a.MaxBytes); err != nil {
		t.Fatalf("cache cleanup damaged profile: %v", err)
	}
}
func TestCorruptUnsafeAndWrongDigestRejected(t *testing.T) {
	for _, mode := range []string{"content", "permission", "symlink", "digest", "directory"} {
		t.Run(mode, func(t *testing.T) {
			body := []byte("correct bytes")
			server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
			c := testCache(t)
			if mode == "digest" {
				a.SHA256 = strings.Repeat("a", 64)
				if _, err := c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err == nil {
					t.Fatal("accepted wrong digest")
				}
				return
			}
			status, err := c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "content":
				_ = os.Chmod(status.Path, 0600)
				_ = os.WriteFile(status.Path, []byte("changed"), 0444)
				_ = os.Chmod(status.Path, 0444)
			case "permission":
				_ = os.Chmod(status.Path, 0666)
			case "directory":
				_ = os.Chmod(filepath.Dir(status.Path), 0755)
			case "symlink":
				_ = os.Remove(status.Path)
				target := filepath.Join(t.TempDir(), "private")
				_ = os.WriteFile(target, body, 0600)
				_ = os.Symlink(target, status.Path)
			}
			if _, err = c.Status(context.Background(), a); err == nil {
				t.Fatal("accepted unsafe entry")
			}
			if _, err = c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err == nil {
				t.Fatal("silently repaired unsafe entry")
			}
		})
	}
}
func TestCanceledDownloadRetainsPartialAndResumes(t *testing.T) {
	body := []byte(strings.Repeat("resume ", 8192))
	var ranges atomic.Int32
	server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"fixed"`)
		if r.Header.Get("Range") != "" {
			ranges.Add(1)
		}
		http.ServeContent(w, r, "image", time.Time{}, strings.NewReader(string(body)))
	})
	c := testCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.CopyTo(ctx, server.Client(), a, "", "", "", func(done, total int64) {
		if done > 0 {
			cancel()
		}
	})
	if err == nil {
		t.Fatal("cancellation was ignored")
	}
	status, err := c.Status(context.Background(), a)
	if err != nil || status.State == "ready" {
		t.Fatalf("partial published: %+v %v", status, err)
	}
	if _, err = c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if ranges.Load() == 0 {
		t.Fatal("retained download was not resumed")
	}
}

// Separate OS processes exercise the file lock, including the post-lock check.
func TestCacheProcessHelper(t *testing.T) {
	if os.Getenv("AXV_CACHE_HELPER") != "1" {
		return
	}
	var a Artifact
	if err := json.Unmarshal([]byte(os.Getenv("AXV_CACHE_ARTIFACT")), &a); err != nil {
		t.Fatal(err)
	}
	c, err := New(os.Getenv("AXV_CACHE_HOME"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(os.Getenv("AXV_CACHE_TEST_CA"))) {
		t.Fatal("missing local fixture CA")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	if _, err = c.CopyTo(context.Background(), client, a, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
}
func TestMultipleProcessesDownloadOnce(t *testing.T) {
	body := []byte(strings.Repeat("process bytes", 4096))
	var requests atomic.Int32
	server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write(body) })
	c := testCache(t)
	raw, _ := json.Marshal(a)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var commands []*exec.Cmd
	for i := 0; i < 3; i++ {
		cmd := exec.Command(executable, "-test.run=^TestCacheProcessHelper$")
		cmd.Env = append(os.Environ(), "AXV_CACHE_HELPER=1", "AXV_CACHE_HOME="+c.Home, "AXV_CACHE_ARTIFACT="+string(raw), "AXV_CACHE_TEST_CA="+string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})))
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err = cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("same digest downloaded %d times", requests.Load())
	}
}

func TestCleanupSkipsAnEntryDuringProfileCopy(t *testing.T) {
	body := []byte(strings.Repeat("locked copy ", 8192))
	server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	c := testCache(t)
	if _, err := c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "profile-image")
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		first := true
		_, err := c.CopyTo(context.Background(), server.Client(), a, destination, destination+".part", "", func(int64, int64) {
			if first {
				first = false
				close(entered)
				<-release
			}
		})
		done <- err
	}()
	<-entered
	if err := c.Remove(context.Background(), a.SHA256); err == nil {
		t.Error("cleanup removed an actively copied entry")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(context.Background(), destination, a.SHA256, a.MaxBytes); err != nil {
		t.Fatal(err)
	}
}

func TestDifferentDigestsProceedWhileAnotherDownloadIsCanceled(t *testing.T) {
	slowBody := []byte("slow image")
	fastBody := []byte("another image")
	entered := make(chan struct{})
	slow, slowArtifact := fixture(t, slowBody, func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() })
	fast, fastArtifact := fixture(t, fastBody, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(fastBody) })
	c := testCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 1)
	go func() { _, err := c.CopyTo(ctx, slow.Client(), slowArtifact, "", "", "", nil); completed <- err }()
	<-entered
	status, err := c.CopyTo(context.Background(), fast.Client(), fastArtifact, "", "", "", nil)
	if err != nil || status.State != "ready" {
		t.Fatalf("other digest blocked: %+v %v", status, err)
	}
	busy, err := c.Status(context.Background(), slowArtifact)
	if err != nil || busy.State != "in_use" {
		t.Fatalf("busy entry: %+v %v", busy, err)
	}
	cancel()
	if err = <-completed; err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err = c.Status(context.Background(), fastArtifact); err != nil {
		t.Fatal("one cancellation damaged another entry", err)
	}
}

func TestCacheRootAndLocksNeverFollowSymlinks(t *testing.T) {
	body := []byte("public bytes")
	server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	for _, kind := range []string{"root", "lock"} {
		t.Run(kind, func(t *testing.T) {
			outside := t.TempDir()
			foreign := filepath.Join(outside, "unrelated")
			if err := os.WriteFile(foreign, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			c := testCache(t)
			if kind == "root" {
				if err := os.Symlink(outside, c.Home); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := c.Status(context.Background(), a); err != nil {
					t.Fatal(err)
				}
				lock := filepath.Join(c.Home, namespace, "locks", a.SHA256+".lock")
				if err := os.Remove(lock); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(foreign, lock); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err == nil {
				t.Fatal("unsafe root/lock accepted")
			}
			bytes, err := os.ReadFile(foreign)
			if err != nil || string(bytes) != "keep" {
				t.Fatal("foreign path changed", err)
			}
		})
	}
}

func TestProfileOutputCannotAliasCacheThroughASymlink(t *testing.T) {
	body := []byte("verified public image")
	server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	c := testCache(t)
	if _, err := c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(c.Home, alias); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(alias, "profile-file")
	if _, err := c.CopyTo(context.Background(), server.Client(), a, destination, destination+".part", "", nil); err == nil {
		t.Fatal("profile copy into cache alias accepted")
	}
	if _, err := os.Stat(filepath.Join(c.Home, "profile-file")); !os.IsNotExist(err) {
		t.Fatal("private profile file written into cache", err)
	}
}

func TestFailedHTTPResponseIsNeverACompleteEntry(t *testing.T) {
	body := []byte("successful explicit retry")
	var requests atomic.Int32
	server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(body)
	})
	c := testCache(t)
	if _, err := c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err == nil {
		t.Fatal("failed response accepted")
	}
	status, err := c.Status(context.Background(), a)
	if err != nil || status.State != "missing" {
		t.Fatalf("failed transfer became usable: %+v %v", status, err)
	}
	if requests.Load() != 1 {
		t.Fatal("download was automatically retried")
	}
	if _, err = c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
}

type failureTransport struct{}

func (failureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("proxy connection failed for user:secret-proxy-password")
}
func TestDownloadDiagnosticsDoNotExposeProxyCredentials(t *testing.T) {
	c := testCache(t)
	a := Artifact{ID: "fixture", URL: "https://source.invalid/public-image", SHA256: strings.Repeat("a", 64), MaxBytes: 1024}
	_, err := c.CopyTo(context.Background(), &http.Client{Transport: failureTransport{}}, a, "", "", "", nil)
	if err == nil || strings.Contains(err.Error(), "secret-proxy-password") || strings.Contains(err.Error(), "user:") {
		t.Fatalf("unsafe diagnostic: %v", err)
	}
}

func TestProfileCopyStaysBoundToItsOpenedDirectory(t *testing.T) {
	body := []byte(strings.Repeat("directory identity ", 8192))
	server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	c := testCache(t)
	if _, err := c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "profile")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "image")
	original := directory + "-original"
	swapped := false
	_, err := c.CopyTo(context.Background(), server.Client(), a, destination, destination+".part", "", func(int64, int64) {
		if swapped {
			return
		}
		swapped = true
		if err := os.Rename(directory, original); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(c.Home, directory); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyFile(context.Background(), filepath.Join(original, "image"), a.SHA256, a.MaxBytes); err != nil {
		t.Fatalf("copy lost its original directory: %v", err)
	}
	if _, err = os.Stat(filepath.Join(c.Home, "image")); !os.IsNotExist(err) {
		t.Fatal("directory replacement redirected a profile write into the cache", err)
	}
}
