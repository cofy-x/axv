package cloudflarepublish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflareapi"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/projectpreview"
)

func TestDedicatedSiteIsPermanentlyConsumedAndCannotBecomeDesktop(t *testing.T) {
	f := publishingFixtureWithDomain(t, "example.net")
	if supported, reason := f.manager.Availability(); !supported || reason != "" {
		t.Fatalf("fresh independent domain unavailable: %v %q", supported, reason)
	}
	first := publish(t, f, "public", "first")
	waitState(t, f.manager, first.ID, "published")
	if site, _ := profile.CloudflareCookieSite(first.Hostname); site != "example.net" {
		t.Fatalf("publication lost its dedicated cookie site: %q", site)
	}
	for _, origin := range []string{"https://example.net", "https://desktop.example.net", "https://" + first.Hostname} {
		if err := f.manager.ValidateDesktopOrigin(origin); err == nil {
			t.Fatalf("desktop could claim a publication site: %s", origin)
		}
	}
	if _, err := f.manager.Unpublish(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, first.ID, "removed")
	if supported, reason := f.manager.Availability(); supported || !strings.Contains(reason, "permanently") {
		t.Fatalf("retired dedicated site appeared reusable: %v %q", supported, reason)
	}
	_, err := f.manager.Publish(context.Background(), Registration{InstanceID: f.manager.InstanceID(), RequestID: compute.NewID("new-target"), Name: "second", Domain: "example.net", Binding: binding(), Port: 5173, Visibility: "public"})
	var failure *Problem
	if !errors.As(err, &failure) || failure.Code != "publication_site_retired" {
		t.Fatalf("retired site accepted another record: %v", err)
	}
	if err = f.manager.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), f.options)
	if err != nil {
		t.Fatal(err)
	}
	if supported, _ := restarted.Availability(); supported || len(restarted.List()) != 1 || restarted.List()[0].Hostname != first.Hostname {
		t.Fatal("restart discarded its permanent site tombstone")
	}
	if err = restarted.Close(); err != nil {
		t.Fatal(err)
	}
	options := f.options
	settings := f.settings
	settings.DesktopOrigin = "https://new-desktop.example.net"
	options.Settings = func() (Settings, error) { return settings, nil }
	before, _ := os.ReadFile(f.manager.path)
	if opened, err := New(context.Background(), options); err == nil {
		_ = opened.Close()
		t.Fatal("editing config bypassed a retired publication site")
	}
	after, _ := os.ReadFile(f.manager.path)
	if string(before) != string(after) {
		t.Fatal("a conflicting desktop configuration changed the retained journal")
	}
}

func TestPublicSuffixAllocatesIndependentGenerationsAndReplaysExactReceipt(t *testing.T) {
	f := publishingFixture(t)
	input := Registration{InstanceID: f.manager.InstanceID(), RequestID: compute.NewID("original"), Name: "app", Domain: f.settings.Domain, Binding: binding(), Port: 5173, Visibility: "public"}
	first, err := f.manager.Publish(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, first.ID, "published")
	if first.Name != input.Name || first.Domain != input.Domain || len(first.Generation) != 32 || first.Hostname != first.Name+"-"+first.Generation+"."+first.Domain {
		t.Fatalf("hostname was not generated from the original allocation: %+v", first)
	}
	if supported, reason := f.manager.Availability(); !supported || reason != "" {
		t.Fatalf("independent PSL sites prevented another publication: %v %q", supported, reason)
	}
	if _, err = f.manager.Unpublish(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, first.ID, "removed")
	replayed, err := f.manager.Publish(context.Background(), input)
	if err != nil || replayed.ID != first.ID || replayed.Hostname != first.Hostname || replayed.Generation != first.Generation || replayed.State != "removed" {
		t.Fatalf("retired original request acquired a fresh generation: %+v %v", replayed, err)
	}
	second := publish(t, f, "public", "app")
	waitState(t, f.manager, second.ID, "published")
	firstSite, _ := profile.CloudflareCookieSite(first.Hostname)
	secondSite, _ := profile.CloudflareCookieSite(second.Hostname)
	if firstSite == secondSite || firstSite != first.Hostname || secondSite != second.Hostname || first.Generation == second.Generation {
		t.Fatalf("PSL publications share a cookie site or generation: %q %q", firstSite, secondSite)
	}
	if err = f.manager.ValidateDesktopOrigin("https://" + first.Hostname); err == nil {
		t.Fatal("a retired PSL publication became the desktop")
	}
	if err = f.manager.ValidateDesktopOrigin("https://another-desktop.github.io"); err != nil {
		t.Fatalf("an independent PSL desktop was rejected: %v", err)
	}
	input.Domain = "example.net"
	if _, err = f.manager.Publish(context.Background(), input); err == nil {
		t.Fatal("changing the reviewed domain replayed an original receipt")
	}
}

func TestReviewedDomainChangeAndInvalidNamesCannotCreateCloudResources(t *testing.T) {
	f := publishingFixture(t)
	for _, name := range []string{"", "App", "with.dot", "-leading", "trailing-", strings.Repeat("a", 25), "äpp", "xn--editor"} {
		_, err := f.manager.Publish(context.Background(), Registration{InstanceID: f.manager.InstanceID(), RequestID: compute.NewID("invalid-name"), Name: name, Domain: f.settings.Domain, Binding: binding(), Port: 5173, Visibility: "public"})
		if err == nil {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
	for _, domain := range []string{"", "apps.example.net", "example.net"} {
		_, err := f.manager.Publish(context.Background(), Registration{InstanceID: f.manager.InstanceID(), RequestID: compute.NewID("reviewed-domain"), Name: "app", Domain: domain, Binding: binding(), Port: 5173, Visibility: "public"})
		if err == nil {
			t.Fatalf("unreviewed allocation domain accepted: %q", domain)
		}
	}
	if len(f.manager.List()) != 0 {
		t.Fatal("invalid input created a durable publication")
	}
	if _, err := os.Stat(f.manager.path); !os.IsNotExist(err) {
		t.Fatal("invalid input initialized the publication journal")
	}
	f.cloud.mu.Lock()
	defer f.cloud.mu.Unlock()
	if f.cloud.dnsCreates != 0 || f.cloud.ingressWrites != 0 {
		t.Fatal("unreviewed input mutated Cloudflare resources")
	}
}

func TestPublicationJournalRejectsChangedAllocationAndDuplicateRetiredSites(t *testing.T) {
	f := publishingFixtureWithDomain(t, "example.net")
	first := publish(t, f, "public", "app")
	waitState(t, f.manager, first.ID, "published")
	if _, err := f.manager.Unpublish(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, first.ID, "removed")
	if err := f.manager.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.manager.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"generation", "hostname", "name", "domain", "site", "duplicate-retired-site"} {
		t.Run(kind, func(t *testing.T) {
			var state journal
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			item := state.Records[first.ID]
			switch kind {
			case "generation":
				item.Publication.Generation = strings.Repeat("A", 32)
			case "hostname":
				item.Publication.Hostname = "selected.example.net"
			case "name":
				item.Publication.Name = "changed"
			case "domain":
				item.Publication.Domain = "apps.example.net"
			case "site":
				item.CookieSite = "unrelated.example.org"
			case "duplicate-retired-site":
				duplicate := item
				duplicate.Publication.ID = "another-original-record"
				duplicate.Input.RequestID = compute.NewID("another-request")
				duplicate.Publication.RequestID = duplicate.Input.RequestID
				duplicate.Publication.Generation = strings.Repeat("0", 32)
				duplicate.Publication.Hostname, _ = allocatedHostname(duplicate.Input.Name, duplicate.Publication.Generation, duplicate.Publication.Domain)
				state.Records[duplicate.Publication.ID] = duplicate
			}
			state.Records[first.ID] = item
			corrupt, _ := json.Marshal(state)
			if err = os.WriteFile(f.manager.path, corrupt, 0600); err != nil {
				t.Fatal(err)
			}
			if opened, err := New(context.Background(), f.options); err == nil {
				_ = opened.Close()
				t.Fatal("changed allocation identity or retired site was silently accepted")
			}
			actual, _ := os.ReadFile(f.manager.path)
			if string(actual) != string(corrupt) {
				t.Fatal("invalid publication journal was reset")
			}
		})
	}
}

func TestMissingJournalPreservesUnknownOwnershipAndCannotReallocateSites(t *testing.T) {
	f := publishingFixture(t)
	first := publish(t, f, "public", "app")
	waitState(t, f.manager, first.ID, "published")
	if err := f.manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.manager.path); err != nil {
		t.Fatal(err)
	}
	if replacement, err := New(context.Background(), f.options); err == nil {
		_ = replacement.Close()
		t.Fatal("missing ownership history was silently initialized as a fresh journal")
	}
	if _, err := os.Stat(f.manager.path); !os.IsNotExist(err) {
		t.Fatal("missing journal was recreated without its original receipt tombstones")
	}
	f.cloud.mu.Lock()
	defer f.cloud.mu.Unlock()
	if f.cloud.dnsCreates != 1 || f.cloud.ingressWrites != 1 || len(f.cloud.dns) != 1 {
		t.Fatal("missing journal caused cloud ownership to be recreated or removed")
	}
}

func TestInterruptedProvisioningWaitsForResumeAndKeepsOriginalGeneration(t *testing.T) {
	f := publishingFixture(t)
	if err := f.manager.Close(); err != nil {
		t.Fatal(err)
	}
	resolving := make(chan struct{})
	options := f.options
	options.Resolve = func(ctx context.Context, _ CapturedRoute, _ compute.ProjectBinding, _ int) (projectpreview.Target, error) {
		close(resolving)
		<-ctx.Done()
		return projectpreview.Target{}, ctx.Err()
	}
	m, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	input := Registration{InstanceID: m.InstanceID(), RequestID: compute.NewID("interrupted"), Name: "app", Domain: f.settings.Domain, Binding: binding(), Port: 5173, Visibility: "public"}
	first, err := m.Publish(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-resolving:
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not begin checking its original target")
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), f.options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	view := waitState(t, restarted, first.ID, "unavailable")
	if view.Phase != "restart-required" || view.Generation != first.Generation || view.Hostname != first.Hostname {
		t.Fatalf("interrupted publication did not preserve its suspended identity: %+v", view)
	}
	f.cloud.mu.Lock()
	writes := f.cloud.dnsCreates + f.cloud.ingressWrites
	f.cloud.mu.Unlock()
	if writes != 0 || f.captures.Load() != 1 {
		t.Fatal("restart provisioned a suspended service without owner review")
	}
	f.valid.Store(false)
	if _, err = restarted.Retry(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	view = waitState(t, restarted, first.ID, "unavailable")
	if view.Phase != "verify-target" || view.Hostname != first.Hostname || view.Generation != first.Generation || f.captures.Load() != 1 {
		t.Fatalf("resume substituted a current environment for the original boot: %+v", view)
	}
}

func TestRestartAutomaticallyContinuesOnlyDurableRemoval(t *testing.T) {
	f := publishingFixture(t)
	first := publish(t, f, "public", "app")
	waitState(t, f.manager, first.ID, "published")
	f.cloud.mu.Lock()
	f.cloud.failDeleteDNS = true
	f.cloud.mu.Unlock()
	if _, err := f.manager.Unpublish(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, f.manager, first.ID, "error")
	if err := f.manager.Close(); err != nil {
		t.Fatal(err)
	}
	f.cloud.mu.Lock()
	f.cloud.failDeleteDNS = false
	f.cloud.mu.Unlock()
	options := f.options
	options.Capture = func(context.Context, compute.ProjectBinding) (CapturedRoute, error) {
		t.Error("removal selected a replacement target")
		return CapturedRoute{}, errors.New("must not capture")
	}
	restarted, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	removed := waitState(t, restarted, first.ID, "removed")
	if removed.Hostname != first.Hostname || removed.Generation != first.Generation || removed.CleanupPending {
		t.Fatalf("restarted cleanup discarded the original identity: %+v", removed)
	}
	f.cloud.mu.Lock()
	defer f.cloud.mu.Unlock()
	if f.cloud.dnsCreates != 1 || len(f.cloud.dns) != 0 || len(f.cloud.configuration["ingress"].([]any)) != 2 {
		t.Fatal("restart recreated owned resources or altered foreign routes")
	}
}

func waitWorkerStopped(t *testing.T, m *Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		running := m.workers[id]
		m.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("failed publication kept rescheduling a background worker")
}

func TestFailedWithdrawalWhileResumingStopsQueuedProbesAndAutomaticRetries(t *testing.T) {
	f := publishingFixture(t)
	f.valid.Store(false)
	first := publish(t, f, "public", "app")
	waitState(t, f.manager, first.ID, "unavailable")
	waitWorkerStopped(t, f.manager, first.ID)
	f.valid.Store(true)
	resolving, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var resolves, probes atomic.Int32
	original := f.manager.options.Resolve
	f.manager.options.Resolve = func(ctx context.Context, route CapturedRoute, b compute.ProjectBinding, port int) (projectpreview.Target, error) {
		resolves.Add(1)
		once.Do(func() { close(resolving) })
		select {
		case <-release:
		case <-ctx.Done():
			return projectpreview.Target{}, ctx.Err()
		}
		target, err := original(ctx, route, b, port)
		handler := target.Handler
		target.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			probes.Add(1)
			handler.ServeHTTP(w, r)
		})
		return target, err
	}
	if _, err := f.manager.Retry(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-resolving:
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not begin resolving the original target")
	}
	saved := f.manager.path + ".saved"
	if err := os.Rename(f.manager.path, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.manager.path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Unpublish(context.Background(), first.ID); err == nil {
		t.Fatal("failed removal save was acknowledged")
	}
	close(release)
	waitWorkerStopped(t, f.manager, first.ID)
	f.manager.schedule(first.ID)
	f.manager.mu.Lock()
	running := f.manager.workers[first.ID]
	f.manager.mu.Unlock()
	if running || resolves.Load() != 1 || probes.Load() != 0 {
		t.Fatalf("failed withdrawal scheduled more target work: running=%v resolves=%d probes=%d", running, resolves.Load(), probes.Load())
	}
	f.cloud.mu.Lock()
	writes := f.cloud.dnsCreates + f.cloud.ingressWrites
	f.cloud.mu.Unlock()
	if writes != 0 {
		t.Fatal("a withdrawn queued resume created cloud resources")
	}
	if err := os.Remove(f.manager.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, f.manager.path); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupPersistenceFailureStopsUntilRepairAndRestart(t *testing.T) {
	f := publishingFixture(t)
	first := publish(t, f, "public", "app")
	waitState(t, f.manager, first.ID, "published")
	waitWorkerStopped(t, f.manager, first.ID)
	var clients atomic.Int32
	original := f.manager.options.Client
	f.manager.options.Client = func(s Settings) (*cloudflareapi.Client, error) {
		clients.Add(1)
		return original(s)
	}
	f.manager.cloudOp.Lock()
	locked := true
	defer func() {
		if locked {
			f.manager.cloudOp.Unlock()
		}
	}()
	if _, err := f.manager.Unpublish(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	saved := f.manager.path + ".saved"
	if err := os.Rename(f.manager.path, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.manager.path, 0700); err != nil {
		t.Fatal(err)
	}
	f.manager.cloudOp.Unlock()
	locked = false
	waitState(t, f.manager, first.ID, "error")
	waitWorkerStopped(t, f.manager, first.ID)
	f.manager.schedule(first.ID)
	f.manager.mu.Lock()
	running := f.manager.workers[first.ID]
	f.manager.mu.Unlock()
	if running || clients.Load() != 1 {
		t.Fatalf("storage failure retried cloud cleanup automatically: running=%v clients=%d", running, clients.Load())
	}
	f.cloud.mu.Lock()
	unchanged := f.cloud.dnsCreates == 1 && f.cloud.ingressWrites == 1 && len(f.cloud.dns) == 1
	f.cloud.mu.Unlock()
	if !unchanged {
		t.Fatal("cleanup continued mutating cloud resources after its journal failed")
	}
	if _, err := f.manager.Retry(context.Background(), first.ID); err == nil {
		t.Fatal("cleanup ignored its unrepaired storage failure")
	}
	if err := os.Remove(f.manager.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, f.manager.path); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), f.options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	waitState(t, restarted, first.ID, "removed")
}
