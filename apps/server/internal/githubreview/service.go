package githubreview

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

type githubProvider interface {
	Discover(context.Context, []string, string) ([]Target, error)
	Checks(context.Context, Target, string, Checks) (Checks, error)
	Current(context.Context, Target, string) (Target, error)
	Token(context.Context, string) (string, error)
	Publish(context.Context, Target, string, string, string) (string, error)
}

type authProvider interface {
	Inspect(context.Context, string) Connection
	Connect(context.Context, string) error
	Stop(string)
}

type reviewer interface {
	Review(context.Context, Target, string, string, func(string) error) ([]Finding, error)
}

type Options struct {
	VMTests   VMTestRunner
	Directory string
	ProxyURL  string
}

type observedConnection struct {
	value Connection
	at    time.Time
}

// Provider adapters report only sanitized connection failures.
type connectionError struct{ error }

type Service struct {
	vm        VMTestRunner
	store     *store
	github    githubProvider
	auth      authProvider
	runner    reviewer
	ctx       context.Context
	cancel    context.CancelFunc
	wake      chan struct{}
	retryWake chan struct{}
	done      chan struct{}

	authMu    sync.Mutex
	enrolling map[string]bool
	observed  map[string]observedConnection

	policyMu          sync.Mutex
	cancelPublication context.CancelFunc
	deferredOutcome   *Job // owned by the worker loop

	discoveryMu sync.Mutex
	pollMu      sync.Mutex
	pollAt      time.Time
	pollError   string
}

func Open(parent context.Context, options Options) (*Service, error) {
	if err := owneddir.Ensure(options.Directory, "axv-github-review"); err != nil {
		return nil, err
	}
	storage, err := openStore(parent, filepath.Join(options.Directory, "reviews.sqlite"))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	github := GitHubCLI{Directory: options.Directory}
	auth := &Auth{Context: ctx, GitHub: github, ProxyURL: options.ProxyURL, WorkDir: options.Directory}
	runner := CodexRunner{WorkDir: options.Directory, ProxyURL: options.ProxyURL}
	s := newService(ctx, cancel, storage, github, auth, runner)
	s.vm = options.VMTests
	go s.run()
	return s, nil
}

func newService(ctx context.Context, cancel context.CancelFunc, store *store, github githubProvider, auth authProvider, runner reviewer) *Service {
	return &Service{
		store: store, github: github, auth: auth, runner: runner, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), retryWake: make(chan struct{}, 1), done: make(chan struct{}),
		enrolling: make(map[string]bool), observed: make(map[string]observedConnection),
	}
}

func (s *Service) Close() error {
	s.cancel()
	<-s.done
	s.auth.Stop("github")
	s.auth.Stop("codex")
	return s.store.db.Close()
}

func (s *Service) schedule() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) requestRun(ctx context.Context) error {
	current, err := s.store.settings(ctx)
	if err != nil {
		return err
	}
	if !current.active() {
		return ErrInactive
	}
	s.schedule()
	return nil
}

func (s *Service) requestRetry(ctx context.Context, request RetryRequest) error {
	if !request.valid() {
		return ErrInvalid
	}
	saved, err := s.store.settings(ctx)
	if err != nil {
		return errStore
	}
	if saved.Revision != request.Revision {
		return ErrConflict
	}
	if !saved.active() {
		return ErrInactive
	}
	job, err := s.store.job(ctx, saved.account, request.Repository, request.Number, request.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRetryStale
	}
	if err != nil {
		return errStore
	}
	if job.BaseSHA != request.BaseSHA || !job.canRetryReview() {
		return ErrRetryStale
	}
	check, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	current, err := s.github.Current(check, job.Target, saved.account)
	if err != nil {
		return err
	}
	if err := s.store.retry(ctx, saved.account, request, current, time.Now()); err != nil {
		return err
	}
	select {
	case s.retryWake <- struct{}{}:
	default:
	}
	return nil
}

func (s *Service) connect(ctx context.Context, provider string) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if err := s.auth.Connect(ctx, provider); err != nil {
		return &connectionError{err}
	}
	s.enrolling[provider] = true
	delete(s.observed, provider)
	_, err := s.observeLocked(ctx, provider)
	return err
}

func (s *Service) disconnect(ctx context.Context, provider string) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if err := s.attach(ctx, provider, ""); err != nil {
		return err
	}
	delete(s.enrolling, provider)
	delete(s.observed, provider)
	s.auth.Stop(provider)
	return nil
}

func (s *Service) attach(ctx context.Context, provider, identity string) error {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if err := s.store.attach(ctx, provider, identity); err != nil {
		return err
	}
	if s.cancelPublication != nil {
		s.cancelPublication()
	}
	return nil
}

func (s *Service) configure(ctx context.Context, config Config) error {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if err := s.store.configure(ctx, config); err != nil {
		return err
	}
	if s.cancelPublication != nil {
		s.cancelPublication()
	}
	s.schedule()
	return nil
}

func (s *Service) observeLocked(ctx context.Context, provider string) (Connection, error) {
	cached := s.observed[provider]
	lifetime := 15 * time.Second
	if s.enrolling[provider] {
		lifetime = time.Second
	}
	if time.Since(cached.at) < lifetime {
		return cached.value, nil
	}
	check, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	view := s.auth.Inspect(check, provider)
	if err := ctx.Err(); err != nil {
		return Connection{}, err
	}
	if s.enrolling[provider] && (view.Attempt == nil || view.Attempt.Status != "pending") {
		if view.Connected && (view.Attempt == nil || view.Attempt.Status == "complete") {
			identity := view.Account
			if provider == "codex" {
				identity = "chatgpt"
			}
			if err := s.attach(ctx, provider, identity); err != nil {
				return Connection{}, err
			}
			delete(s.enrolling, provider)
			s.schedule()
		} else if view.Attempt != nil && view.Attempt.Status == "failed" {
			delete(s.enrolling, provider)
		}
	}
	s.observed[provider] = observedConnection{value: view, at: time.Now()}
	return view, nil
}

func (s *Service) snapshot(ctx context.Context) (Snapshot, error) {
	s.authMu.Lock()
	github, err := s.observeLocked(ctx, "github")
	if err != nil {
		s.authMu.Unlock()
		return Snapshot{}, err
	}
	codex, err := s.observeLocked(ctx, "codex")
	s.authMu.Unlock()
	if err != nil {
		return Snapshot{}, err
	}
	saved, err := s.store.settings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if saved.account == "" {
		github.Connected = false
	} else if !strings.EqualFold(saved.account, github.Account) {
		github.Connected = false
		if github.Error == "" {
			github.Error = "The local GitHub login changed. Reconnect the intended account to continue."
		}
	}
	if !saved.codex {
		codex.Connected = false
	}
	jobs, err := s.store.history(ctx, saved.account)
	if err != nil {
		return Snapshot{}, err
	}
	budget, err := s.store.budget(ctx, time.Now())
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{ComparisonReady: s.comparisonReady(), Config: saved.Config, Budget: budget, GitHub: github, Codex: codex, Jobs: jobs}
	s.pollMu.Lock()
	if !s.pollAt.IsZero() {
		at := s.pollAt
		result.LastPollAt = &at
	}
	result.Error = s.pollError
	s.pollMu.Unlock()
	return result, nil
}

func (s *Service) checkEnrollments(ctx context.Context) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	for provider := range s.enrolling {
		if _, err := s.observeLocked(ctx, provider); err != nil {
			s.recordError(err)
		}
	}
}

func (s *Service) run() {
	defer close(s.done)
	var workers sync.WaitGroup
	workers.Go(s.runDiscovery)
	workers.Go(s.runReviews)
	workers.Go(s.runEvidence)
	workers.Go(s.runComparisons)
	workers.Wait()
}

func (s *Service) runDiscovery() {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	var lastScan time.Time
	for s.ctx.Err() == nil {
		s.checkEnrollments(s.ctx)
		now := time.Now()
		settings, err := s.store.settings(s.ctx)
		if err != nil {
			s.recordError(errStore)
		} else if settings.active() && now.Sub(lastScan) >= time.Duration(settings.IntervalMinutes)*time.Minute {
			lastScan = now
			s.scan(s.ctx, settings, now)
			s.wakeReviews()
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
			lastScan = time.Time{}
		case <-ticker.C:
		}
	}
}

func (s *Service) wakeReviews() {
	select {
	case s.retryWake <- struct{}{}:
	default:
	}
}

func (s *Service) runReviews() {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	var pauseUntil time.Time
	for s.ctx.Err() == nil {
		now := time.Now()
		saved, err := s.store.settings(s.ctx)
		if err != nil {
			s.recordError(errStore)
		} else if saved.connected() && !now.Before(pauseUntil) {
			err = s.processNext(s.ctx, saved, now)
			if err != nil && !errors.Is(err, sql.ErrNoRows) && s.ctx.Err() == nil {
				s.recordError(err)
				pauseUntil = retryTime(err, time.Now())
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.retryWake:
		case <-ticker.C:
		}
	}
}

func retryTime(err error, at time.Time) time.Time {
	switch {
	case errors.Is(err, ErrCodexUsageLimited):
		return at.Add(10 * time.Minute)
	case githubConnectionError(err):
		return at.Add(time.Minute)
	default:
		return at
	}
}

func githubConnectionError(err error) bool {
	return errors.Is(err, ErrGitHubRateLimited) || errors.Is(err, ErrGitHubAccountChange) || errors.Is(err, ErrGitHubLoginRequired)
}

func (s *Service) scan(ctx context.Context, settings savedSettings, now time.Time) {
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	check, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	targets, err := s.github.Discover(check, settings.Repositories, settings.account)
	for _, t := range targets {
		if t.valid() {
			err = errors.Join(err, s.observeComparison(check, settings.account, t, now))
		}
	}
	selected := chooseTargets(settings.Config, targets)
	if saveErr := s.store.enqueue(ctx, settings.account, targets, selected, now); saveErr != nil {
		err = errStore
	}
	s.pollMu.Lock()
	s.pollAt = now
	s.pollError = safeError(err)
	s.pollMu.Unlock()
}

func chooseTargets(config Config, targets []Target) []Target {
	selected := make([]Target, 0, len(targets))
	keys := make(map[string]int, len(targets))
	for _, target := range targets {
		if !target.valid() || target.Closed || target.Draft {
			continue
		}
		if target.Requested || target.Source == sourceRequested {
			target.Source = sourceRequested
		} else if config.watches(target.Repository) {
			target.Source = sourceRepository
		} else {
			continue
		}
		key := targetKey(target)
		if index, ok := keys[key]; ok {
			if target.Source == sourceRequested {
				selected[index] = target
			}
			continue
		}
		keys[key] = len(selected)
		selected = append(selected, target)
	}
	return selected
}

var errStore = errors.New("GitHub Review could not save its state.")
var errOutdated = errors.New("The pull request changed or is no longer selected for review.")

func (s *Service) recordError(err error) {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	s.pollError = safeError(err)
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Service) processNext(ctx context.Context, saved savedSettings, now time.Time) error {
	if s.deferredOutcome != nil {
		if err := s.store.update(ctx, *s.deferredOutcome); err != nil {
			return errStore
		}
		s.deferredOutcome = nil
	}
	budget, err := s.store.budget(ctx, now)
	if err != nil {
		return errStore
	}
	job, err := s.store.nextJob(ctx, saved.account, now.Add(-time.Minute), budget.Used < saved.DailyLimit, saved.Enabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return errStore
	}
	if job.Status == "publishing" && !saved.autoPublishes(job.Repository) {
		return s.setOutcome(ctx, job, "analyzed", nil)
	}
	// Connectivity is checked immediately before processing, rather than
	// relying on an earlier browser visit to the Connections pane.
	if job.Status == "queued" {
		check, cancel := context.WithTimeout(ctx, 6*time.Second)
		login := s.auth.Inspect(check, "codex")
		cancel()
		if !login.Connected {
			return errors.New("Reconnect the local Codex CLI to continue reviewing.")
		}
	}
	remote, cancel := context.WithTimeout(ctx, 40*time.Second)
	current, err := s.github.Current(remote, job.Target, saved.account)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return s.deferJob(ctx, job, err)
	}
	saved, err = s.store.settings(ctx)
	if err != nil {
		return errStore
	}
	if !saved.connected() || (!saved.Enabled && !job.manualReview) || !strings.EqualFold(saved.account, job.account) {
		return nil
	}
	if !matchesJob(saved.Config, job, current) {
		return s.setOutcome(ctx, job, "stale", errOutdated)
	}
	if job.Status == "publishing" {
		return s.publish(ctx, job)
	}
	job.Requested = current.Requested
	job.authorContext = current.authorContext
	return s.review(ctx, saved, job)
}

func (s *Service) deferJob(ctx context.Context, job Job, cause error) error {
	if githubConnectionError(cause) {
		return cause
	}
	job.attempts++
	status := job.Status
	if job.attempts >= 3 || errors.Is(cause, ErrGitHubForbidden) {
		status = "failed"
	}
	return s.setOutcome(ctx, job, status, cause)
}

func matchesJob(config Config, job Job, current Target) bool {
	return !current.Closed && !current.Draft &&
		strings.EqualFold(job.Repository, current.Repository) && job.Number == current.Number &&
		(job.BaseSHA == current.BaseSHA || job.equivalentBaseSHA == current.BaseSHA) && job.HeadSHA == current.HeadSHA &&
		(config.watches(current.Repository) || current.Requested)
}

func (s *Service) review(ctx context.Context, saved savedSettings, job Job) error {
	remote, cancel := context.WithTimeout(ctx, 30*time.Second)
	token, err := s.github.Token(remote, saved.account)
	cancel()
	if err != nil {
		return s.deferJob(ctx, job, err)
	}
	if err := s.store.startReview(ctx, job, time.Now()); err != nil {
		if errors.Is(err, ErrDailyLimit) || errors.Is(err, ErrInactive) || errors.Is(err, ErrConflict) {
			return sql.ErrNoRows
		}
		return errStore
	}
	started, err := s.store.job(ctx, job.account, job.Repository, job.Number, job.HeadSHA)
	if err != nil || started.Evidence == nil {
		return errStore
	}
	if started.Status != "reviewing" {
		return nil
	}
	job.evidenceKey = started.evidenceKey
	run, cancel := context.WithTimeout(ctx, 12*time.Minute)
	runner := s.runner
	if local, ok := runner.(CodexRunner); ok {
		local.recordMergeBase = func(sha string) error {
			_, err := s.store.db.ExecContext(run, `UPDATE review_jobs SET merge_base_sha=? WHERE account=? AND repository=? AND number=? AND head_sha=? AND base_sha=? AND evidence_key=?`, sha, job.account, job.Repository, job.Number, job.HeadSHA, job.BaseSHA, job.evidenceKey)
			return err
		}
		runner = local
	}
	findings, err := runner.Review(run, job.Target, token, started.Evidence.Policy.Rules, func(message string) error {
		at := time.Now().UTC().Format(time.RFC3339Nano)
		_, err := s.store.db.ExecContext(run, `UPDATE review_jobs SET review_stage=json_set(review_stage,
  '$.messages[#]',json_object('at',?,'text',?),'$.updatedAt',?) WHERE account=? AND repository=? AND number=? AND head_sha=? AND evidence_key=?`,
			at, message, at, job.account, job.Repository, job.Number, job.HeadSHA, started.evidenceKey)
		if err != nil {
			return errStore
		}
		return nil
	})
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if errors.Is(err, ErrCodexUsageLimited) {
			if saveErr := s.setOutcome(ctx, job, "queued", err); saveErr != nil {
				return saveErr
			}
			return err
		}
		if errors.Is(err, ErrCodexTransient) || errors.Is(err, context.DeadlineExceeded) {
			return s.deferJob(ctx, job, err)
		}
		return s.setOutcome(ctx, job, "failed", err)
	}
	job.attempts = 0
	job.Findings = findings
	if len(findings) == 0 {
		return s.setOutcome(ctx, job, "clean", nil)
	}
	before, err := s.store.settings(ctx)
	if err != nil {
		return s.setOutcome(ctx, job, "analyzed", errStore)
	}
	if !job.manualReview && saved.autoPublishes(job.Repository) && before.active() && before.autoPublishes(job.Repository) && strings.EqualFold(before.account, job.account) {
		return s.setOutcome(ctx, job, "publishing", nil)
	}
	return s.setOutcome(ctx, job, "analyzed", nil)
}

func (s *Service) publish(ctx context.Context, job Job) error {
	s.policyMu.Lock()
	settings, err := s.store.settings(ctx)
	if err != nil {
		s.policyMu.Unlock()
		return errStore
	}
	if !settings.active() || !strings.EqualFold(settings.account, job.account) {
		s.policyMu.Unlock()
		return nil
	}
	if !settings.autoPublishes(job.Repository) {
		s.policyMu.Unlock()
		return s.setOutcome(ctx, job, "analyzed", nil)
	}
	remote, cancel := context.WithTimeout(ctx, time.Minute)
	s.cancelPublication = cancel
	s.policyMu.Unlock()
	defer func() {
		cancel()
		s.policyMu.Lock()
		s.cancelPublication = nil
		s.policyMu.Unlock()
	}()
	target := job.Target
	target.Source = sourceRepository
	job.attempts++
	if err := s.setOutcome(ctx, job, "publishing", nil); err != nil {
		return err
	}
	id := sha256.Sum256(fmt.Appendf(nil, "%s\n%s\n%d\n%s", strings.ToLower(job.account),
		strings.ToLower(job.Repository), job.Number, job.HeadSHA))
	marker := fmt.Sprintf("<!-- axv-review:%x -->", id)
	link, err := s.github.Publish(remote, target, job.account, marker, reviewComment(job))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if errors.Is(remote.Err(), context.Canceled) {
			job.attempts--
			return s.setOutcome(ctx, job, "publishing", nil)
		}
		if errors.Is(err, ErrTargetChanged) {
			return s.setOutcome(ctx, job, "stale", errOutdated)
		}
		if githubConnectionError(err) {
			job.attempts--
			if saveErr := s.setOutcome(ctx, job, "publishing", err); saveErr != nil {
				return saveErr
			}
			return err
		}
		status := "publishing"
		if job.attempts >= 3 || errors.Is(err, ErrGitHubForbidden) {
			status = "failed"
		}
		return s.setOutcome(ctx, job, status, err)
	}
	job.CommentURL = link
	return s.setOutcome(ctx, job, "published", nil)
}

func (s *Service) setOutcome(ctx context.Context, job Job, status string, cause error) error {
	job.Status, job.UpdatedAt, job.Error = status, time.Now().UTC(), safeError(cause)
	if err := s.store.update(ctx, job); err != nil {
		if status != "reviewing" {
			s.deferredOutcome = &job
		}
		return errStore
	}
	return nil
}

var markdown = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", "@", "@\u200b",
	"\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]",
	"|", "\\|", "\n", " ", "\r", " ",
)

func reviewComment(job Job) string {
	var body strings.Builder
	fmt.Fprintf(&body, "Automated Codex review of commit `%s`.\n", job.HeadSHA)
	for _, finding := range job.Findings {
		location := fmt.Sprintf("%s:%d", markdown.Replace(finding.Path), finding.Line)
		if finding.Side == "base" {
			location += " (base)"
		}
		fmt.Fprintf(&body, "\n- **%s** · %s\n  %s\n", markdown.Replace(finding.Title), location, markdown.Replace(finding.Body))
	}
	return body.String()
}
