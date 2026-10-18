package artifactcache

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProfileStagingCannotTruncateAnUnrelatedHardLink(t *testing.T) {
	body := []byte("verified public artifact")
	server, a := fixture(t, body, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	c := testCache(t)
	destination := filepath.Join(t.TempDir(), "image")
	partial := destination + ".part"
	foreign := filepath.Join(t.TempDir(), "user-document")
	if err := os.WriteFile(foreign, []byte("preserve this document"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(foreign, partial); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CopyTo(context.Background(), server.Client(), a, destination, partial, "", nil); err == nil {
		t.Fatal("accepted a staging file linked to unrelated user data")
	}
	got, err := os.ReadFile(foreign)
	if err != nil || string(got) != "preserve this document" {
		t.Fatalf("user document changed: %q, %v", got, err)
	}
}

func TestImportSourceCannotBeItsOwnStagingFile(t *testing.T) {
	body := []byte("original image bytes")
	_, a := fixture(t, body, func(http.ResponseWriter, *http.Request) {})
	destination := filepath.Join(t.TempDir(), "image")
	partial := destination + ".part"
	if err := os.WriteFile(partial, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := CopyPinned(context.Background(), partial, destination, partial, a, nil); err == nil {
		t.Fatal("accepted an import source aliasing staging")
	}
	got, err := os.ReadFile(partial)
	if err != nil || string(got) != string(body) {
		t.Fatalf("import source changed: %q, %v", got, err)
	}
}

func TestDownloadChecksRedirectsWithACallerSuppliedClient(t *testing.T) {
	body := []byte("public artifact")
	var insecureRequests atomic.Int32
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		insecureRequests.Add(1)
		_, _ = w.Write(body)
	}))
	defer insecure.Close()
	server, a := fixture(t, body, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, insecure.URL, http.StatusFound)
	})
	c := testCache(t)
	if _, err := c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err == nil {
		t.Fatal("accepted an HTTPS downgrade through a caller-supplied client")
	}
	if insecureRequests.Load() != 0 {
		t.Fatal("contacted the insecure redirect target")
	}
}

type lockWaitContext struct {
	context.Context
	waited chan struct{}
}

func (c lockWaitContext) Done() <-chan struct{} {
	select {
	case c.waited <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func TestWaitingForAReplacedLockDoesNotAcquireTheOldInode(t *testing.T) {
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	owner, _, err := acquire(context.Background(), r, "entry.lock", true)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waited := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		f, _, err := acquire(lockWaitContext{ctx, waited}, r, "entry.lock", true)
		if f != nil {
			f.Close()
		}
		done <- err
	}()
	<-waited
	if err = r.Remove("entry.lock"); err != nil {
		t.Fatal(err)
	}
	replacement, _, err := acquire(context.Background(), r, "entry.lock", true)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	owner.Close()
	if err = <-done; err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("waiter accepted a stale lock while the new owner held the path: %v", err)
	}
}

func TestCanceledVerificationDoesNotRecommendDeletingAHealthyEntry(t *testing.T) {
	body := []byte("healthy cache content")
	server, a := fixture(t, body, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	c := testCache(t)
	if _, err := c.CopyTo(context.Background(), server.Client(), a, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	r, err := c.prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.statusIn(ctx, r, a); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "cache remove") {
		t.Fatalf("cancellation was reported as damage: %v", err)
	}
	if status, err := c.Status(context.Background(), a); err != nil || status.State != "ready" {
		t.Fatalf("cancellation damaged the entry: %+v, %v", status, err)
	}
}
