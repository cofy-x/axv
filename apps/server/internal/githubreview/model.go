// Package githubreview schedules local Codex reviews of GitHub pull requests.
package githubreview

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	sourceRepository = "repository"
	sourceRequested  = "requested"
)

var (
	ErrInvalid    = errors.New("review the repository list, polling interval, and daily limit")
	ErrConflict   = errors.New("review settings changed; reload them before saving")
	ErrInactive   = errors.New("connect GitHub and Codex and enable polling first")
	ErrDailyLimit = errors.New("the daily review limit has been reached")
	ErrRetryStale = errors.New("this pull request version is no longer available to retry")
	repoName      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)
	commitSHA     = regexp.MustCompile(`^[a-f0-9]{40}$`)
)

type RepositoryPolicy struct {
	ComparisonRuntimes []ComparisonRuntime `json:"comparisonRuntimes,omitempty"`
	TestProfiles       []TestProfile       `json:"testProfiles,omitempty"`
	Rules              string              `json:"rules"`
	TestCommand        []string            `json:"testCommand"`
}

type Config struct {
	RepositoryPolicies  map[string]RepositoryPolicy `json:"repositoryPolicies"`
	Repositories        []string                    `json:"repositories"`
	PublishRepositories []string                    `json:"publishRepositories"`
	IntervalMinutes     int                         `json:"intervalMinutes"`
	DailyLimit          int                         `json:"dailyLimit"`
	Enabled             bool                        `json:"enabled"`
	Revision            uint64                      `json:"revision"`
}

type Budget struct {
	Used     int       `json:"used"`
	ResetsAt time.Time `json:"resetsAt"`
}

type RetryRequest struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	BaseSHA    string `json:"baseSha"`
	HeadSHA    string `json:"headSha"`
	Revision   uint64 `json:"revision"`
}

type Target struct {
	authorContext *AuthorContext
	Repository    string `json:"repository"`
	Number        int    `json:"number"`
	Title         string `json:"title,omitempty"`
	BaseSHA       string `json:"baseSha"`
	HeadSHA       string `json:"headSha"`
	Source        string `json:"source"`
	Draft         bool   `json:"-"`
	Closed        bool   `json:"-"`
	Requested     bool   `json:"-"`
}

type Finding struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Side     string `json:"side,omitempty"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

type CheckRun struct {
	ExecutionSHA  string `json:"executionSha,omitempty"`
	ExecutionRef  string `json:"executionRef,omitempty"`
	Scope         string `json:"scope,omitempty"`
	ProvenanceURL string `json:"provenanceUrl,omitempty"`

	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	URL        string `json:"url,omitempty"`
}
type Checks struct {
	RefreshErrorAt *time.Time `json:"refreshErrorAt,omitempty"`
	Status         string     `json:"status"`
	Runs           []CheckRun `json:"runs,omitempty"`
	Error          string     `json:"error,omitempty"`
	UpdatedAt      *time.Time `json:"updatedAt,omitempty"`
}
type ReviewMessage struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// AuthorContext captures GitHub metadata returned with the verified PR head.
type AuthorContext struct {
	Title                string `json:"title"`
	Description          string `json:"description"`
	TitleTruncated       bool   `json:"titleTruncated,omitempty"`
	DescriptionTruncated bool   `json:"descriptionTruncated,omitempty"`
}

type ReviewStage struct {
	AuthorContext *AuthorContext  `json:"authorContext,omitempty"`
	Messages      []ReviewMessage `json:"messages"`
	Status        string          `json:"status"`
	StartedAt     time.Time       `json:"startedAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	Error         string          `json:"error,omitempty"`
}
type Evidence struct {
	Review ReviewStage      `json:"review"`
	Policy RepositoryPolicy `json:"policy"`
	Checks Checks           `json:"checks"`
	Tests  []VMTestAttempt  `json:"tests"`
}
type Job struct {
	Followups          []Candidate           `json:"followups,omitempty"`
	Candidates         []Candidate           `json:"candidates,omitempty"`
	PreviousCandidates []CandidateOccurrence `json:"previousCandidates,omitempty"`
	EvidenceOptions    []EvidenceItem        `json:"evidenceOptions,omitempty"`
	MergeBaseSHA       string                `json:"mergeBaseSha,omitempty"`
	ObservedBaseSHA    string                `json:"observedBaseSha,omitempty"`
	equivalentBaseSHA  string
	manualReview       bool
	Evidence           *Evidence `json:"evidence,omitempty"`
	Target
	Status      string    `json:"status"`
	Findings    []Finding `json:"findings,omitempty"`
	CommentURL  string    `json:"commentUrl,omitempty"`
	Error       string    `json:"error,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
	account     string
	evidenceKey string
	attempts    int
}

type LoginAttempt struct {
	Status string `json:"status"`
	URL    string `json:"url,omitempty"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Connection struct {
	Available bool          `json:"available"`
	Connected bool          `json:"connected"`
	Account   string        `json:"account,omitempty"`
	Attempt   *LoginAttempt `json:"attempt,omitempty"`
	Error     string        `json:"error,omitempty"`
}

type Snapshot struct {
	ComparisonReady bool       `json:"comparisonReady"`
	Config          Config     `json:"config"`
	Budget          Budget     `json:"budget"`
	GitHub          Connection `json:"github"`
	Codex           Connection `json:"codex"`
	Jobs            []Job      `json:"jobs"`
	LastPollAt      *time.Time `json:"lastPollAt,omitempty"`
	Error           string     `json:"error,omitempty"`
}

func validRepository(repo string) bool {
	_, name, _ := strings.Cut(repo, "/")
	return repoName.MatchString(repo) && name != "." && name != ".."
}

func (t Target) valid() bool {
	return validRepository(t.Repository) && t.Number > 0 && commitSHA.MatchString(t.BaseSHA) && commitSHA.MatchString(t.HeadSHA)
}

func (r RetryRequest) valid() bool {
	target := Target{Repository: r.Repository, Number: r.Number, BaseSHA: r.BaseSHA, HeadSHA: r.HeadSHA}
	return r.Revision > 0 && target.valid()
}

func (c Config) normalized() (Config, error) {
	if c.Repositories == nil || c.PublishRepositories == nil || len(c.Repositories) > 100 || len(c.PublishRepositories) > 100 ||
		c.IntervalMinutes < 1 || c.IntervalMinutes > 1440 || c.DailyLimit < 1 || c.DailyLimit > 1000 {
		return Config{}, ErrInvalid
	}
	repositories := make([]string, 0, len(c.Repositories))
	watched := make(map[string]string, len(c.Repositories))
	for _, value := range c.Repositories {
		repo := strings.TrimSpace(value)
		if !validRepository(repo) {
			return Config{}, ErrInvalid
		}
		key := strings.ToLower(repo)
		if _, exists := watched[key]; !exists {
			repositories = append(repositories, repo)
			watched[key] = repo
		}
	}
	publishRepositories := make([]string, 0, len(c.PublishRepositories))
	seen := make(map[string]bool, len(c.PublishRepositories))
	for _, value := range c.PublishRepositories {
		requested := strings.TrimSpace(value)
		if !validRepository(requested) {
			return Config{}, ErrInvalid
		}
		key := strings.ToLower(requested)
		repo, ok := watched[key]
		if !ok {
			return Config{}, ErrInvalid
		}
		if !seen[key] {
			publishRepositories = append(publishRepositories, repo)
			seen[key] = true
		}
	}
	policies := make(map[string]RepositoryPolicy, len(c.RepositoryPolicies))
	for key, policy := range c.RepositoryPolicies {
		name, ok := watched[strings.ToLower(key)]
		if !ok || !policy.valid() {
			return Config{}, ErrInvalid
		}
		if _, duplicate := policies[name]; duplicate {
			return Config{}, ErrInvalid
		}
		policy.Rules = strings.TrimSpace(policy.Rules)
		policy.TestProfiles = normalizeTestProfiles(policy.TestProfiles)
		if policy.TestCommand == nil {
			policy.TestCommand = []string{}
		}
		policies[name] = policy
	}
	c.RepositoryPolicies = policies
	c.Repositories = repositories
	c.PublishRepositories = publishRepositories
	return c, nil
}

func (c Config) watches(repository string) bool {
	return hasRepository(c.Repositories, repository)
}

func (c Config) autoPublishes(repository string) bool {
	return c.watches(repository) && hasRepository(c.PublishRepositories, repository)
}

func hasRepository(repositories []string, repository string) bool {
	for _, watched := range repositories {
		if strings.EqualFold(watched, repository) {
			return true
		}
	}
	return false
}

func (p RepositoryPolicy) valid() bool {
	if len(p.Rules) > 4096 || strings.IndexFunc(p.Rules, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' }) >= 0 || !validTestCommand(p.TestCommand) {
		return false
	}
	return validTestProfiles(p.TestProfiles) && validComparisonRuntimes(p.ComparisonRuntimes)
}

func (c Config) policy(repository string) RepositoryPolicy {
	for name, policy := range c.RepositoryPolicies {
		if strings.EqualFold(name, repository) {
			return policy
		}
	}
	return RepositoryPolicy{TestCommand: []string{}}
}

func (j Job) canRetryReview() bool {
	return j.Status == "failed" || j.Status == "stale" || (j.Evidence == nil && (j.Status == "analyzed" || j.Status == "clean"))
}
