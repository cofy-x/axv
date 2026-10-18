package prepare

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

	"github.com/cofy-x/axv/apps/server/internal/compute/artifacts"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestUbuntuTransitionDefaultAndExplicitOfficialPublicationBlocker(t *testing.T) {
	e, _ := testEngine(t, nil)
	e.platform = "darwin"
	plan, err := e.Plan(context.Background(), Request{ImageSource: "official"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Request.ImageSource != "official" || plan.CanStart {
		t.Fatalf("default source: %+v", plan)
	}
	pending := false
	for _, check := range plan.Checks {
		if check.ID == "release_pending" {
			pending = true
		}
	}
	if !pending {
		t.Fatal("missing publication blocker")
	}
	for _, download := range plan.Downloads {
		if download.ID == "ubuntu" {
			t.Fatal("silently substituted Ubuntu")
		}
	}
	body := []byte("reviewed public image fixture")
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write(body) }))
	defer server.Close()
	digest := sha256.Sum256(body)
	base := artifact{ID: "ubuntu", Name: "Ubuntu fixture", URL: server.URL, SHA256: hex.EncodeToString(digest[:]), MaxBytes: 1 << 20, Bytes: int64(len(body))}
	e.catalog.Ubuntu[e.arch] = base
	if _, err = e.cache.CopyTo(context.Background(), server.Client(), base, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	plan, err = e.Plan(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, download := range plan.Downloads {
		if download.ID == "ubuntu" {
			hit = download.Cached && download.CacheState == "ready"
		}
	}
	if plan.Request.ImageSource != "ubuntu-base" || !hit || plan.CacheHome != e.cache.Home || requests.Load() != 1 {
		t.Fatalf("verified cache not reflected: %+v, requests=%d", plan, requests.Load())
	}
	saved, err := e.store.plan(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.ImageShared || saved.ImageBundle.Guest != nil || saved.ImageArtifact.SHA256 != base.SHA256 {
		t.Fatalf("basic contract: %+v", saved)
	}
	e.catalog.Ubuntu[e.arch] = artifact{SHA256: strings.Repeat("a", 64)}
	saved, err = e.store.plan(context.Background(), plan.ID)
	if err != nil || saved.ImageArtifact.SHA256 != base.SHA256 || saved.ImageArtifact.URL != server.URL {
		t.Fatal("reviewed pin changed with catalog", err)
	}
}

func TestPublishedCatalogUsesTheSameFrozenBundleContract(t *testing.T) {
	e, _ := testEngine(t, nil)
	e.platform = "darwin"
	a := artifact{ID: "axv", Name: "AXV fixture release", URL: "https://release.invalid/controlled-test.img", SHA256: strings.Repeat("a", 64), MaxBytes: 1 << 20}
	release := artifacts.Release{Version: "test-v1", Artifact: a, Bundle: guestimage.Bundle{Version: guestimage.Version, ID: "axv-test-v1", Name: a.Name, Provider: "lima", Arch: e.arch, Guest: &guestimage.Contract{ImageVersion: 1, ProtocolVersion: guestproto.Version, Capabilities: []string{"exec"}}, Rootfs: guestimage.Artifact{SHA256: a.SHA256}}}
	e.catalog.AXV["lima/"+e.arch] = release
	if !e.Snapshot().OfficialImageAvailable {
		t.Fatal("published reviewed release missing from diagnostics")
	}
	plan, err := e.Plan(context.Background(), Request{ImageSource: "official"})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range plan.Checks {
		if check.ID == "release_pending" || check.ID == "release_invalid" {
			t.Fatalf("fixture release rejected: %+v", check)
		}
	}
	saved, err := e.store.plan(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	versionRecorded := false
	for _, check := range saved.Plan.Checks {
		if check.ID == "official_image" && strings.Contains(check.Detail, release.Version) {
			versionRecorded = true
		}
	}
	if !versionRecorded {
		t.Fatal("reviewed release version was not persisted")
	}
	if !saved.ImageShared || saved.ImageBundle.Guest == nil || saved.ImageArtifact.URL != a.URL {
		t.Fatalf("official path lost bundle contract: %+v", saved)
	}
}

func TestPreparationNeverWritesProfileRuntimeInsideCache(t *testing.T) {
	cacheHome := filepath.Join(t.TempDir(), "shared-cache")
	profileHome := filepath.Join(cacheHome, "profile")
	if engine, err := New(Options{Home: profileHome, CacheHome: cacheHome}); err == nil {
		_ = engine.Close()
		t.Fatal("profile inside cache accepted")
	}
	if _, err := os.Stat(cacheHome); !os.IsNotExist(err) {
		t.Fatal("invalid profile selection wrote to the cache", err)
	}
	e, _ := testEngine(t, nil)
	request := e.defaults()
	request.StorageRoot = e.cache.Home
	if _, err := e.validateStorage(request); err == nil {
		t.Fatal("runtime storage inside cache accepted")
	}
}

func TestNewPreparationNeverSharesASystemLimaInstallation(t *testing.T) {
	e, _ := testEngine(t, nil)
	systemRoot := t.TempDir()
	binary := filepath.Join(systemRoot, "bin", "limactl")
	agentArch := "x86_64"
	if e.arch == "arm64" {
		agentArch = "aarch64"
	}
	for _, directory := range []string{filepath.Dir(binary), filepath.Join(systemRoot, "share", "lima")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'limactl version 2.2.1\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemRoot, "share", "lima", "lima-guestagent.Linux-"+agentArch), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(binary))
	if e.matchingLima(context.Background()) != "" || e.lightweightLima() != "" {
		t.Fatal("automatic preparation adopted a global Lima installation")
	}
}

func TestPlanFreezesMirrorSourceWithoutChangingArtifactIdentity(t *testing.T) {
	t.Setenv("AXV_ARTIFACT_MIRRORS", "")
	e, _ := testEngine(t, nil)
	e.platform = "darwin"
	canonical := e.catalog.Ubuntu[e.arch]
	config, _ := json.Marshal(map[string]string{canonical.SHA256: "https://operator.invalid/reviewed-exact.img"})
	var err error
	e.catalog, err = artifacts.WithMirrors(e.catalog, string(config))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := e.Plan(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := e.store.plan(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ImageArtifact.URL == canonical.URL || saved.ImageArtifact.OriginURL != canonical.URL || saved.ImageArtifact.SHA256 != canonical.SHA256 {
		t.Fatal("mirror changed identity", saved.ImageArtifact)
	}
	found := false
	for _, download := range plan.Downloads {
		if download.SHA256 == canonical.SHA256 {
			found = download.URL == saved.ImageArtifact.URL && download.OriginURL == canonical.URL
		}
	}
	if !found {
		t.Fatal("plan omitted mirror diagnostics")
	}
	// Catalogs are fixed for an engine's lifetime. A later launch can select a
	// different source, after the previous engine's dependency checks have stopped.
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(e.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	current := reopened.catalog.Ubuntu[reopened.arch]
	if current.URL != canonical.URL || current.URL == saved.ImageArtifact.URL || current.SHA256 != canonical.SHA256 {
		t.Fatalf("next launch did not select the changed catalog source: %+v", current)
	}
	savedAgain, err := reopened.store.plan(context.Background(), plan.ID)
	if err != nil || savedAgain.ImageArtifact.URL != saved.ImageArtifact.URL || savedAgain.ImageArtifact.OriginURL != canonical.URL || savedAgain.ImageArtifact.SHA256 != canonical.SHA256 {
		t.Fatal("catalog change redirected frozen plan", err)
	}
}

func TestExistingPreparationDoesNotInheritImageSelection(t *testing.T) {
	e, home := testEngine(t, nil)
	e.options.ImageBundle = "/explicit/build/manifest.json"
	if err := profile.SaveCompute(home, profile.Compute{Mode: "external", Socket: "/test/owned-host.sock"}); err != nil {
		t.Fatal(err)
	}
	check, err := e.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if check.Defaults.ImageSource != "" || check.Defaults.ImageBundle != "" {
		t.Fatal("existing service inherited an image input", check.Defaults)
	}
	if !sameExistingRequest(check.Defaults, check.Defaults) {
		t.Fatal("reconnecting unchanged configuration was rejected")
	}
	for _, source := range []string{"official", "ubuntu-base", "bundle"} {
		if sameExistingRequest(check.Defaults, Request{ImageSource: source}) {
			t.Fatalf("image replacement %q was silently ignored", source)
		}
	}
}
