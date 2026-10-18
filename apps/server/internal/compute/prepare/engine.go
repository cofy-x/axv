package prepare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/artifacts"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type Engine struct {
	options        Options
	dir            string
	owner          string
	store          *taskStore
	lock           *os.File
	ctx            context.Context
	stop           context.CancelFunc
	mu             sync.Mutex
	wg             sync.WaitGroup
	closed         bool
	runners        map[string]*runnerControl
	platform, arch string
	client         *http.Client
	// Tests supply small pinned artifacts and deterministic process boundaries.
	cache             *artifactcache.Cache
	catalog           artifactCatalog
	beforeCommit      func() error
	dependencyMu      sync.Mutex
	dependencyChecks  []Requirement
	dependencyChecked time.Time
	dependencyRunning bool
}

type runnerControl struct{ cancel context.CancelFunc }

func New(options Options) (*Engine, error) {
	if !filepath.IsAbs(options.Home) {
		return nil, fmt.Errorf("preparation profile home must be absolute")
	}
	options.Home = filepath.Clean(options.Home)
	cacheHome := options.CacheHome
	var err error
	if cacheHome == "" {
		cacheHome, err = artifactcache.Home()
	}
	if err != nil {
		return nil, err
	}
	cache, err := artifactcache.New(cacheHome)
	if err != nil {
		return nil, err
	}
	resolvedCache, err := absoluteDirectory(cache.Home)
	if err != nil {
		return nil, err
	}
	resolvedHome, err := absoluteDirectory(options.Home)
	if err != nil {
		return nil, err
	}
	if within(resolvedCache, resolvedHome) {
		return nil, fmt.Errorf("profile runtime cannot be inside AXV_CACHE_HOME")
	}
	for _, managed := range []string{"preparation", "images", "compute", "lima", "tools", "data", "workspace", "run", "bin", "logs", "ssh", "host"} {
		runtimePath, resolveErr := absoluteDirectory(filepath.Join(options.Home, managed))
		if resolveErr != nil {
			return nil, resolveErr
		}
		if within(runtimePath, resolvedCache) || within(resolvedCache, runtimePath) {
			return nil, fmt.Errorf("AXV_CACHE_HOME must be separate from profile runtime directory %s", runtimePath)
		}
	}
	// Identity resolves symlinked ancestors (including /tmp on macOS). Create the
	// profile directory first so its identity remains identical on the next run.
	if err := os.MkdirAll(options.Home, 0700); err != nil {
		return nil, err
	}
	if options.ImageBundle == "" {
		options.ImageBundle = os.Getenv("AXV_IMAGE_BUNDLE")
	}
	e := &Engine{cache: cache, options: options, dir: filepath.Join(options.Home, "preparation"), owner: profile.Identity(options.Home), platform: runtime.GOOS, arch: runtime.GOARCH, runners: map[string]*runnerControl{}, catalog: productionCatalog()}
	e.catalog, err = artifacts.WithMirrors(e.catalog, os.Getenv("AXV_ARTIFACT_MIRRORS"))
	if err != nil {
		return nil, err
	}
	if err := owneddir.Ensure(e.dir, e.owner+":preparation"); err != nil {
		return nil, err
	}
	lock, err := filelock.TryLock(filepath.Join(e.dir, "runner.lock"))
	if err != nil {
		return nil, fmt.Errorf("another preparation engine owns this profile: %w", err)
	}
	e.lock = lock
	e.store, err = openStore(e.dir)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err = e.store.interrupt(context.Background()); err != nil {
		_ = e.store.db.Close()
		_ = lock.Close()
		return nil, err
	}
	options.Logger.Info("preparation.recovered")
	e.ctx, e.stop = context.WithCancel(diagnostics.Context(context.Background(), options.Logger))
	e.client = downloadClient()
	return e, nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.stop()
	e.mu.Unlock()
	e.wg.Wait()
	e.client.CloseIdleConnections()
	err := e.store.db.Close()
	if lockErr := e.lock.Close(); err == nil {
		err = lockErr
	}
	return err
}

// Snapshot uses the same inexpensive reads as Check and does not probe hosts or
// hash downloaded images. It is suitable for desktop environment responses.
func (e *Engine) Snapshot() Check { result, _ := e.Check(context.Background()); return result }

func (e *Engine) Check(ctx context.Context) (Check, error) {
	c := Check{Platform: e.platform, Arch: e.arch, ConfigurationSource: "profile", CacheHome: e.cache.Home, RequiredLimaVersion: compute.SupportedLimaVersion, AutomaticSetupSupported: e.platform == "darwin" && (e.arch == "arm64" || e.arch == "amd64"), Defaults: e.defaults(), Checks: []Requirement{}}
	release, published := e.catalog.AXV["lima/"+e.arch]
	c.OfficialImageAvailable = published && release.Validate("lima", e.arch) == nil
	profile, err := profile.Read(e.options.Home)
	if err != nil {
		c.LockedReason = err.Error()
		c.Checks = append(c.Checks, blocked("configuration", "Profile configuration", err.Error()))
	} else {
		c.Configured = profile.Compute.Mode != "disabled"
		if c.Configured {
			c.Defaults, err = e.existingDefaults(profile.Compute)
			if err != nil {
				c.Checks = append(c.Checks, blocked("configuration", "VM configuration", err.Error()))
			}
		}
	}
	if e.options.ConfigurationLockedReason != "" {
		c.ConfigurationSource, c.LockedReason = "environment", e.options.ConfigurationLockedReason
		c.Checks = append(c.Checks, blocked("configuration_locked", "Configuration source", c.LockedReason))
	}
	if !c.Configured && e.platform == "darwin" {
		c.Checks = append(c.Checks, e.dependencies()...)
	}
	if e.platform != "darwin" && e.platform != "linux" {
		c.Checks = append(c.Checks, blocked("platform", "VM platform", "VM preparation supports macOS and a local Linux Firecracker service."))
	}
	c.ActiveJob, c.LatestJob, err = e.store.recent(ctx)
	return c, err
}

func (e *Engine) defaults() Request {
	limaHome := e.options.DefaultLimaHome
	if limaHome == "" {
		limaHome = filepath.Join(e.options.Home, "lima")
	}
	imageSource := "ubuntu-base"
	if e.options.ImageBundle != "" {
		imageSource = "bundle"
	}
	return Request{ImageSource: imageSource, ImageBundle: e.options.ImageBundle, StorageRoot: e.options.Home, LimaHome: limaHome, CPUs: max(2, runtime.NumCPU()/2), MemoryMiB: 4096, DiskGiB: 128, Machines: 4}
}

func (e *Engine) existingDefaults(c profile.Compute) (Request, error) {
	// Reconnecting an existing environment does not select or replace its image.
	r := e.defaults()
	r.ImageSource, r.ImageBundle = "", ""
	if c.Mode == "external" {
		r.Socket = c.Socket
		return r, nil
	}
	h, err := compute.LoadConfig(c.HostConfig)
	if err != nil {
		return r, err
	}
	r.StorageRoot, r.CPUs, r.MemoryMiB, r.DiskGiB, r.Machines = filepath.Dir(h.DataDir), h.Capacity.CPUs, h.Capacity.MemoryMiB, h.Capacity.DiskGiB, h.Capacity.Machines
	if h.Lima != nil {
		r.LimaHome = h.Lima.Home
	}
	return r, nil
}

func (e *Engine) Job(ctx context.Context, id string) (Job, error) {
	r, err := e.store.record(ctx, id)
	return r.Job, err
}

func (e *Engine) Start(ctx context.Context, request StartRequest) (Job, error) {
	if err := validRequestID(request.RequestID); err != nil {
		return Job{}, err
	}
	if !request.Confirmed {
		return Job{}, problem(KindInvalidInput, "confirmation_required", "Confirm the reviewed preparation plan before starting.")
	}
	fingerprint := digestJSON(request)
	if r, err := e.store.request(ctx, request.RequestID, "start", fingerprint); err != nil {
		return Job{}, err
	} else if r != nil {
		return r.Job, nil
	}
	spec, err := e.store.plan(ctx, request.PlanID)
	if err != nil {
		return Job{}, err
	}
	if !spec.Plan.CanStart {
		return Job{}, problem(KindConflict, "plan_blocked", "Resolve the preparation checks and review a new plan.")
	}
	expires, err := time.Parse(time.RFC3339Nano, spec.Plan.ExpiresAt)
	if err != nil || time.Now().After(expires) {
		return Job{}, problem(KindConflict, "plan_expired", "This plan has expired. Review a new plan.")
	}
	if e.options.ConfigurationLockedReason != "" {
		return Job{}, problem(KindConflict, "configuration_locked", "%s", e.options.ConfigurationLockedReason)
	}
	_, current, err := readCompute(e.options.Home)
	if err != nil {
		return Job{}, err
	}
	if current != spec.ComputeFingerprint {
		return Job{}, problem(KindConflict, "configuration_changed", "VM configuration changed after review. Review a new plan.")
	}
	timestamp := now()
	r := record{Spec: spec, Job: Job{ID: compute.NewID("prepare"), RequestID: request.RequestID, PlanID: spec.Plan.ID, Revision: 1, Status: "queued", Attempt: 1, Phase: "queued", Steps: stepsFor(spec), CreatedAt: timestamp, UpdatedAt: timestamp, CanCancel: !spec.Existing, Committed: spec.Existing, Plan: spec.Plan}}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return Job{}, problem(KindUnavailable, "preparation_stopping", "The preparation service is stopping.")
	}
	r, created, err := e.store.create(ctx, r, fingerprint)
	if err == nil && created {
		e.jobLogger(r.Job).Info("preparation.queued")
		e.launchLocked(r.Job.ID)
	}
	return r.Job, err
}

func (e *Engine) Retry(ctx context.Context, id string, request ActionRequest) (Job, error) {
	if err := validRequestID(request.RequestID); err != nil {
		return Job{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return Job{}, problem(KindUnavailable, "preparation_stopping", "The preparation service is stopping.")
	}
	r, changed, err := e.store.action(ctx, id, "retry", request, func(r *record) error {
		if !r.Job.CanRetry || (r.Job.Status != "failed" && r.Job.Status != "interrupted") {
			return problem(KindConflict, "retry_unavailable", "This task cannot be retried in its current state.")
		}
		if e.options.ConfigurationLockedReason != "" {
			return problem(KindConflict, "configuration_locked", "%s", e.options.ConfigurationLockedReason)
		}
		r.Job.Status, r.Job.Error, r.Job.CanRetry = "queued", nil, false
		r.Job.CanCancel = !r.Job.Committed && r.Intent == nil
		r.Job.Attempt++
		return nil
	})
	if err == nil && changed {
		e.jobLogger(r.Job).Info("preparation.retry", "actionRequestId", request.RequestID)
		e.launchLocked(id)
	}
	return r.Job, err
}

func (e *Engine) Cancel(ctx context.Context, id string, request ActionRequest) (Job, error) {
	if err := validRequestID(request.RequestID); err != nil {
		return Job{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, changed, err := e.store.action(ctx, id, "cancel", request, func(r *record) error {
		if !r.Job.CanCancel || r.Job.Committed || r.Intent != nil {
			return problem(KindConflict, "cancel_unavailable", "Configuration commit has started. Preparation cannot be cancelled now.")
		}
		r.Job.Status, r.Job.CanCancel, r.Job.CanRetry, r.Job.Error = "cancelled", false, false, nil
		return nil
	})
	if err == nil && changed {
		e.jobLogger(r.Job).Info("preparation.cancelled", "actionRequestId", request.RequestID)
		if runner := e.runners[id]; runner != nil {
			runner.cancel()
		}
	}
	return r.Job, err
}

func (e *Engine) launchLocked(id string) {
	ctx, cancel := context.WithCancel(e.ctx)
	control := &runnerControl{cancel: cancel}
	e.runners[id] = control
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer cancel()
		e.run(ctx, id)
		e.mu.Lock()
		if e.runners[id] == control {
			delete(e.runners, id)
		}
		e.mu.Unlock()
	}()
}

func shortLimaHome(path string) bool {
	return len(filepath.Join(path, "vm-"+strings.Repeat("0", 32), "ssh.sock."+strings.Repeat("0", 16))) < 104
}

func (e *Engine) dependencies() []Requirement {
	e.dependencyMu.Lock()
	checks := append([]Requirement{}, e.dependencyChecks...)
	launch := !e.dependencyRunning && time.Since(e.dependencyChecked) > 30*time.Second
	if launch {
		e.dependencyRunning = true
	}
	e.dependencyMu.Unlock()
	if len(checks) == 0 {
		checks = []Requirement{{ID: "dependency_check", Label: "VM dependencies", Status: "needed", Detail: "Checking this computer's virtualization tools."}}
	}
	if launch {
		e.mu.Lock()
		if !e.closed {
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				result := []Requirement{}
				if err := macPrerequisites(e.ctx, e.arch); err != nil {
					result = append(result, blocked("platform_dependencies", "macOS virtualization", err.Error()))
				} else {
					result = append(result, ready("platform_dependencies", "macOS virtualization", "Native VZ and system SSH tools are available."))
				}
				if binary := e.lightweightLima(); binary != "" {
					result = append(result, ready("lima_dependency", "Lima 2.2.1", "An installation is available at "+binary+"; preparation verifies it before use."))
				} else {
					result = append(result, Requirement{ID: "lima_dependency", Label: "Lima 2.2.1", Status: "needed", Detail: "Preparation will install verified Lima privately for this AXV profile."})
				}
				e.dependencyMu.Lock()
				e.dependencyChecks = result
				e.dependencyChecked = time.Now()
				e.dependencyRunning = false
				e.dependencyMu.Unlock()
			}()
		}
		e.mu.Unlock()
	}
	return checks
}

func digestJSON(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validRequestID(id string) error {
	if len(id) < 8 || len(id) > 128 || strings.TrimSpace(id) != id || strings.ContainsAny(id, "\x00\r\n") {
		return problem(KindInvalidInput, "invalid_request", "requestId must contain 8–128 bytes without surrounding whitespace or control characters.")
	}
	return nil
}
func ready(id, label, detail string) Requirement {
	return Requirement{ID: id, Label: label, Status: "ready", Detail: detail}
}
func blocked(id, label, detail string) Requirement {
	return Requirement{ID: id, Label: label, Status: "blocked", Detail: detail}
}

func readCompute(home string) (json.RawMessage, string, error) {
	b, err := os.ReadFile(filepath.Join(home, "config.json"))
	if os.IsNotExist(err) {
		return nil, digestJSON(nil), nil
	}
	if err != nil {
		return nil, "", err
	}
	if len(b) > 1<<20 {
		return nil, "", fmt.Errorf("profile configuration exceeds 1 MiB")
	}
	var values map[string]json.RawMessage
	if err = json.Unmarshal(b, &values); err != nil || values == nil {
		return nil, "", fmt.Errorf("profile configuration must contain a JSON object")
	}
	raw := values["compute"]
	var value any
	if len(raw) != 0 {
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, "", err
		}
	}
	return raw, digestJSON(value), nil
}

func (e *Engine) jobLogger(j Job) *diagnostics.Logger {
	return e.options.Logger.With("jobId", j.ID, "planId", j.PlanID, "requestId", j.RequestID, "attempt", j.Attempt)
}
