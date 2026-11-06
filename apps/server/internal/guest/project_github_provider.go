package guest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
)

const (
	projectGitHubTimeout      = 20 * time.Second
	projectGitHubOutputLimit  = 2 << 20
	projectGitHubPRLimit      = 10
	projectGitHubContextLimit = 50
)

// The query is application-owned. All caller-derived values are JSON variables;
// neither the browser nor the repository supplies GraphQL text or CLI arguments.
// See https://docs.github.com/en/graphql/reference/commits and /checks, /pulls,
// and /repos for the selected fields and their pagination contracts.
const projectGitHubQuery = `query AXVProjectGitHub($owner: String!, $name: String!, $repositoryID: ID!, $byID: Boolean!, $sha: String!, $branch: String!, $hasCommit: Boolean!, $hasBranch: Boolean!) {
  viewer { id login }
  review: repository(owner: $owner, name: $name) @skip(if: $byID) { ...AXVRepository }
  selected: node(id: $repositoryID) @include(if: $byID) { __typename ... on Repository { ...AXVRepository } }
}
fragment AXVRepository on Repository {
  __typename id nameWithOwner url
  localCommit: object(expression: $sha) @include(if: $hasCommit) {
    __typename
    ... on Commit {
      ...AXVCommit
      associatedPullRequests(first: 10) {
        totalCount pageInfo { hasNextPage }
        nodes { ...AXVPullRequest }
      }
    }
  }
  branchPullRequests: pullRequests(first: 10, states: [OPEN], headRefName: $branch) @include(if: $hasBranch) {
    totalCount pageInfo { hasNextPage }
    nodes { ...AXVPullRequest }
  }
}
fragment AXVRepositoryIdentity on Repository { id nameWithOwner url }
fragment AXVCommit on Commit {
  __typename oid repository { ...AXVRepositoryIdentity }
  statusCheckRollup {
    state
    contexts(first: 50) {
      totalCount pageInfo { hasNextPage }
      nodes {
        __typename
        ... on CheckRun {
          id name status conclusion url: permalink
          repository { ...AXVRepositoryIdentity }
          checkSuite { commit { oid } }
        }
        ... on StatusContext {
          id context state targetUrl
          commit { oid repository { ...AXVRepositoryIdentity } }
        }
      }
    }
  }
}
fragment AXVPullRequest on PullRequest {
  id number title url state headRefName headRefOid
  repository { ...AXVRepositoryIdentity }
  headRepository { ...AXVRepositoryIdentity }
  headRef { target { __typename ... on Commit { ...AXVCommit } } }
  potentialMergeCommit { ...AXVCommit }
}`

var (
	projectGitHubSlug      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9_.-]{1,100}$`)
	projectGitHubID        = regexp.MustCompile(`^[A-Za-z0-9_+/=-]{1,256}$`)
	projectGitHubSHA       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	projectGitHubSHA256    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	projectGitHubLogin     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,99}$`)
	projectGitHubEnum      = regexp.MustCompile(`^[A-Z][A-Z_]{0,63}$`)
	projectGitHubCheckPath = regexp.MustCompile(`^/(actions/runs/[1-9][0-9]*(/(job|attempts)/[1-9][0-9]*)?|runs/[1-9][0-9]*|pull/[1-9][0-9]*/checks)$`)
)

type projectGitHubRepositoryNode struct {
	Type string `json:"__typename"`
	protocol.GitHubRepository
	LocalCommit        *projectGitHubCommit       `json:"localCommit"`
	BranchPullRequests *projectGitHubPRConnection `json:"branchPullRequests"`
}

type projectGitHubCommit struct {
	Type                   string                     `json:"__typename"`
	OID                    string                     `json:"oid"`
	Repository             *protocol.GitHubRepository `json:"repository"`
	Rollup                 json.RawMessage            `json:"statusCheckRollup"`
	AssociatedPullRequests *projectGitHubPRConnection `json:"associatedPullRequests"`
}

type projectGitHubPage struct {
	HasNextPage *bool `json:"hasNextPage"`
}

type projectGitHubPRConnection struct {
	TotalCount *int               `json:"totalCount"`
	PageInfo   *projectGitHubPage `json:"pageInfo"`
	Nodes      []*projectGitHubPR `json:"nodes"`
}

type projectGitHubPR struct {
	ID             string                     `json:"id"`
	Number         int                        `json:"number"`
	Title          string                     `json:"title"`
	URL            string                     `json:"url"`
	State          string                     `json:"state"`
	HeadBranch     string                     `json:"headRefName"`
	HeadSHA        string                     `json:"headRefOid"`
	Repository     *protocol.GitHubRepository `json:"repository"`
	HeadRepository *protocol.GitHubRepository `json:"headRepository"`
	HeadRef        *struct {
		Target *projectGitHubCommit `json:"target"`
	} `json:"headRef"`
	MergeCommit *projectGitHubCommit `json:"potentialMergeCommit"`
}

type projectGitHubContext struct {
	Type       string                     `json:"__typename"`
	ID         string                     `json:"id"`
	Name       string                     `json:"name"`
	Status     string                     `json:"status"`
	Conclusion string                     `json:"conclusion"`
	URL        string                     `json:"url"`
	Context    string                     `json:"context"`
	State      string                     `json:"state"`
	TargetURL  string                     `json:"targetUrl"`
	Repository *protocol.GitHubRepository `json:"repository"`
	CheckSuite *struct {
		Commit *struct {
			OID string `json:"oid"`
		} `json:"commit"`
	} `json:"checkSuite"`
	Commit *struct {
		OID        string                     `json:"oid"`
		Repository *protocol.GitHubRepository `json:"repository"`
	} `json:"commit"`
}

type projectGitHubResponse struct {
	Data *struct {
		Viewer   *protocol.GitHubAccount      `json:"viewer"`
		Review   *projectGitHubRepositoryNode `json:"review"`
		Selected *projectGitHubRepositoryNode `json:"selected"`
	} `json:"data"`
	Errors []struct {
		Type string `json:"type"`
	} `json:"errors"`
}

func (s *service) readProjectGitHub(ctx context.Context, req protocol.ProjectGitHubRequest, git protocol.GitHubGitState) (protocol.ProjectGitHubObservation, error) {
	var empty protocol.ProjectGitHubObservation
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return empty, projectProblem(503, "github_profile_invalid", "The guest account home is unavailable.")
	}
	cli, err := os.Stat(filepath.Join(home, ".local/bin/gh"))
	if err != nil || !cli.Mode().IsRegular() || cli.Mode().Perm()&0111 == 0 {
		return empty, projectProblem(503, "github_cli_missing", "Install a reviewed GitHub CLI at ~/.local/bin/gh in this guest account.")
	}
	if err := projectGitHubProfile(home); err != nil {
		return empty, err
	}
	ctx, cancel := context.WithTimeout(ctx, projectGitHubTimeout)
	defer cancel()
	if s.ctx != nil {
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
	}
	select {
	case s.executions <- struct{}{}:
		defer func() { <-s.executions }()
	default:
		return empty, projectProblem(503, "github_busy", "Guest read capacity is full. Retry when another read finishes.")
	}
	cmd, err := projectGitHubCommand(ctx, home, req, git)
	if err != nil {
		return empty, err
	}
	configureExecution(cmd)
	cmd.WaitDelay = time.Second
	out := &projectGitHubOutput{buffer: limitedbuffer.Buffer{Limit: projectGitHubOutputLimit}, cancel: cancel}
	diagnostic := &projectGitHubOutput{buffer: limitedbuffer.Buffer{Limit: 16 << 10}, cancel: cancel}
	cmd.Stdout, cmd.Stderr = out, diagnostic
	runErr := cmd.Run()
	if out.buffer.Truncated || diagnostic.buffer.Truncated {
		return empty, projectProblem(502, "github_response_invalid", "GitHub output exceeded the read limit. No partial result was accepted.")
	}
	if ctx.Err() != nil {
		return empty, projectProblem(504, "github_timeout", "The GitHub read was interrupted or exceeded its deadline.")
	}
	if errors.Is(runErr, os.ErrNotExist) {
		return empty, projectProblem(503, "github_cli_missing", "Install a reviewed GitHub CLI at ~/.local/bin/gh in this guest account.")
	}
	body, status, err := projectGitHubHTTP(out.buffer.Data)
	if err != nil {
		if runErr != nil {
			return empty, projectGitHubRunError(diagnostic.buffer.Data)
		}
		return empty, projectGitHubInvalid()
	}
	if status != http.StatusOK {
		return empty, projectGitHubHTTPError(status)
	}
	observation, parseErr := parseProjectGitHub(body, req, git)
	if parseErr != nil {
		return empty, parseErr
	}
	if runErr != nil {
		return empty, projectGitHubRunError(diagnostic.buffer.Data)
	}
	return observation, nil
}

type projectGitHubOutput struct {
	buffer limitedbuffer.Buffer
	cancel context.CancelFunc
}

func (w *projectGitHubOutput) Write(data []byte) (int, error) {
	n, err := w.buffer.Write(data)
	if w.buffer.Truncated {
		w.cancel()
	}
	return n, err
}

func projectGitHubProfile(home string) error {
	for _, relative := range []string{".config", ".config/axv", ".config/axv/github"} {
		info, err := os.Lstat(filepath.Join(home, relative))
		if errors.Is(err, os.ErrNotExist) {
			return projectProblem(401, "github_auth_required", "Configure and explicitly select this guest account's dedicated GitHub profile at ~/.config/axv/github.")
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || relative == ".config/axv/github" && info.Mode().Perm() != 0700 {
			return projectProblem(503, "github_profile_invalid", "The dedicated guest GitHub profile must be a private directory without symbolic links.")
		}
	}
	info, err := os.Lstat(filepath.Join(home, ".config/axv/github/hosts.yml"))
	if errors.Is(err, os.ErrNotExist) {
		return projectProblem(401, "github_auth_required", "Authenticate GitHub CLI using the dedicated guest profile before connecting this repository.")
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return projectProblem(503, "github_profile_invalid", "The dedicated GitHub profile metadata must be a private regular file.")
	}
	return nil
}

func projectGitHubCommand(ctx context.Context, home string, req protocol.ProjectGitHubRequest, git protocol.GitHubGitState) (*exec.Cmd, error) {
	byID := req.RepositoryID != ""
	owner, name := "", ""
	if byID {
		if req.Repository != "" || !projectGitHubID.MatchString(req.RepositoryID) || !projectGitHubID.MatchString(req.AccountID) {
			return nil, projectProblem(400, "invalid_request", "Supply the reviewed repository and account identities.")
		}
	} else {
		if !projectGitHubSlug.MatchString(req.Repository) || req.AccountID != "" {
			return nil, projectProblem(400, "invalid_request", "Supply one GitHub repository as owner/name for review.")
		}
		owner, name, _ = strings.Cut(req.Repository, "/")
	}
	if byID && projectGitHubSHA256.MatchString(git.HeadSHA) {
		return nil, projectProblem(409, "github_git_unsupported", "GitHub checks require a SHA-1 Git repository. Local Git metadata remains available.")
	}
	if byID && git.HeadSHA != "" && !projectGitHubSHA.MatchString(git.HeadSHA) || !projectGitHubText(git.Branch, 1024) {
		return nil, projectProblem(409, "git_metadata_invalid", "The captured Git identity cannot be used for a GitHub read.")
	}
	variables := map[string]any{
		"owner": owner, "name": name, "repositoryID": req.RepositoryID, "byID": byID,
		"sha": git.HeadSHA, "branch": git.Branch, "hasCommit": byID && git.HeadSHA != "",
		"hasBranch": byID && git.Branch != "" && !git.Detached,
	}
	payload, err := json.Marshal(map[string]any{"query": projectGitHubQuery, "variables": variables})
	if err != nil {
		return nil, projectGitHubInvalid()
	}
	cmd := exec.CommandContext(ctx, filepath.Join(home, ".local/bin/gh"),
		"api", "--hostname", "github.com", "--method", "POST", "--header", "Accept: application/vnd.github+json",
		"--include", "--input", "-", "graphql")
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Dir = home
	cmd.Env = []string{
		"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC", "TERM=dumb",
		"GH_CONFIG_DIR=" + filepath.Join(home, ".config/axv/github"), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1",
		"GH_NO_EXTENSION_UPDATE_NOTIFIER=1", "GH_PAGER=cat", "PAGER=cat", "NO_COLOR=1",
	}
	return cmd, nil
}

func projectGitHubHTTP(data []byte) ([]byte, int, error) {
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(data)), nil)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if response.StatusCode == http.StatusForbidden && (response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("Retry-After") != "") {
		return body, http.StatusTooManyRequests, err
	}
	return body, response.StatusCode, err
}

func projectGitHubHTTPError(status int) error {
	switch status {
	case 401:
		return projectProblem(401, "github_auth_required", "The selected guest GitHub profile requires authentication.")
	case 403:
		return projectProblem(403, "github_forbidden", "The selected GitHub account cannot access this read, or GitHub has limited requests.")
	case 404:
		return projectProblem(404, "github_repository_unavailable", "The reviewed repository is unavailable or inaccessible to this GitHub account.")
	case 429:
		return projectProblem(429, "github_rate_limited", "GitHub has limited requests. Wait before refreshing.")
	default:
		return projectProblem(502, "github_unavailable", "GitHub did not complete the read. Retry later.")
	}
}

func projectGitHubRunError(diagnostic []byte) error {
	text := strings.ToLower(string(diagnostic))
	switch {
	case strings.Contains(text, "gh auth login"), strings.Contains(text, "authentication token"), strings.Contains(text, "http 401"):
		return projectGitHubHTTPError(401)
	case strings.Contains(text, "rate limit"), strings.Contains(text, "http 429"):
		return projectGitHubHTTPError(429)
	case strings.Contains(text, "http 403"):
		return projectGitHubHTTPError(403)
	default:
		return projectProblem(502, "github_unavailable", "The guest GitHub read failed. Check the selected profile and network connection.")
	}
}

func projectGitHubInvalid() error {
	return projectProblem(502, "github_response_invalid", "GitHub returned an incomplete or invalid response. No result was accepted.")
}

func parseProjectGitHub(body []byte, req protocol.ProjectGitHubRequest, git protocol.GitHubGitState) (protocol.ProjectGitHubObservation, error) {
	var empty protocol.ProjectGitHubObservation
	if len(body) > projectGitHubOutputLimit || !jsonunicode.Valid(body) {
		return empty, projectGitHubInvalid()
	}
	var response projectGitHubResponse
	d := json.NewDecoder(bytes.NewReader(body))
	if d.Decode(&response) != nil || d.Decode(new(any)) != io.EOF {
		return empty, projectGitHubInvalid()
	}
	if len(response.Errors) != 0 {
		for _, problem := range response.Errors {
			if problem.Type == "RATE_LIMITED" {
				return empty, projectGitHubHTTPError(429)
			}
			if problem.Type == "FORBIDDEN" || problem.Type == "INSUFFICIENT_SCOPES" {
				return empty, projectGitHubHTTPError(403)
			}
			if problem.Type == "NOT_FOUND" {
				return empty, projectGitHubHTTPError(404)
			}
		}
		return empty, projectGitHubInvalid()
	}
	if response.Data == nil || response.Data.Viewer == nil || !projectGitHubID.MatchString(response.Data.Viewer.ID) || !projectGitHubLogin.MatchString(response.Data.Viewer.Login) {
		return empty, projectGitHubInvalid()
	}
	if req.AccountID != "" && req.AccountID != response.Data.Viewer.ID {
		return empty, projectProblem(409, "github_account_changed", "The guest GitHub account changed. Review the connection again.")
	}
	repository := response.Data.Review
	if req.RepositoryID != "" {
		repository = response.Data.Selected
	}
	if repository == nil {
		return empty, projectGitHubHTTPError(404)
	}
	if repository.Type != "Repository" || !projectGitHubRepositoryValid(&repository.GitHubRepository) {
		return empty, projectGitHubInvalid()
	}
	if req.RepositoryID != "" && repository.ID != req.RepositoryID {
		return empty, projectProblem(409, "github_repository_changed", "The GitHub repository identity changed. Review the connection again.")
	}
	if req.Repository != "" && !strings.EqualFold(req.Repository, repository.NameWithOwner) {
		return empty, projectGitHubInvalid()
	}
	result := protocol.ProjectGitHubObservation{
		Git: git, Account: *response.Data.Viewer, Repository: repository.GitHubRepository,
		PullRequests: []protocol.GitHubPullRequest{}, PullRequestsComplete: true,
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if req.RepositoryID == "" {
		result.PullRequestsComplete = false
		return result, nil
	}
	if git.HeadSHA != "" {
		if repository.LocalCommit == nil {
			result.LocalChecks = &protocol.GitHubCheckSet{Repository: result.Repository, SHA: git.HeadSHA, State: "unavailable", Checks: []protocol.GitHubCheck{}}
			result.PullRequestsComplete = false
		} else {
			var err error
			result.LocalChecks, err = projectGitHubChecks(repository.LocalCommit, result.Repository, git.HeadSHA)
			if err != nil {
				return empty, err
			}
		}
	}
	seen := make(map[string]protocol.GitHubPullRequest)
	connections := []struct {
		value  *projectGitHubPRConnection
		branch bool
	}{}
	if git.Branch != "" && !git.Detached {
		connections = append(connections, struct {
			value  *projectGitHubPRConnection
			branch bool
		}{repository.BranchPullRequests, true})
	}
	if git.HeadSHA != "" && repository.LocalCommit != nil {
		connections = append(connections, struct {
			value  *projectGitHubPRConnection
			branch bool
		}{repository.LocalCommit.AssociatedPullRequests, false})
	}
	for _, connection := range connections {
		complete, err := projectGitHubConnection(connection.value, projectGitHubPRLimit)
		if err != nil {
			return empty, err
		}
		result.PullRequestsComplete = result.PullRequestsComplete && complete
		for _, raw := range connection.value.Nodes {
			if raw == nil {
				result.PullRequestsComplete = false
				continue
			}
			pr, err := projectGitHubPullRequest(raw)
			if err != nil {
				return empty, err
			}
			if connection.branch && pr.BaseRepository.ID != result.Repository.ID {
				return empty, projectGitHubInvalid()
			}
			if pr.BaseRepository.ID == result.Repository.ID && pr.BaseRepository.NameWithOwner != result.Repository.NameWithOwner || pr.HeadRepository.ID == result.Repository.ID && pr.HeadRepository.NameWithOwner != result.Repository.NameWithOwner {
				return empty, projectGitHubInvalid()
			}
			if connection.branch && (pr.State != "open" || pr.HeadRepository.ID != result.Repository.ID || pr.HeadBranch != git.Branch) {
				continue
			}
			if !connection.branch && pr.BaseRepository.ID != result.Repository.ID && pr.HeadRepository.ID != result.Repository.ID {
				continue
			}
			if previous, exists := seen[pr.ID]; exists {
				if !reflect.DeepEqual(previous, pr) {
					return empty, projectGitHubInvalid()
				}
				continue
			}
			seen[pr.ID] = pr
			result.PullRequests = append(result.PullRequests, pr)
		}
	}
	return result, nil
}

func projectGitHubConnection(connection *projectGitHubPRConnection, limit int) (bool, error) {
	if connection == nil || connection.TotalCount == nil || connection.PageInfo == nil || connection.PageInfo.HasNextPage == nil || connection.Nodes == nil || *connection.TotalCount < len(connection.Nodes) || len(connection.Nodes) > limit {
		return false, projectGitHubInvalid()
	}
	return !*connection.PageInfo.HasNextPage && *connection.TotalCount == len(connection.Nodes), nil
}

func projectGitHubPullRequest(raw *projectGitHubPR) (protocol.GitHubPullRequest, error) {
	var empty protocol.GitHubPullRequest
	if !projectGitHubID.MatchString(raw.ID) || raw.Number < 1 || raw.Title == "" || !projectGitHubText(raw.Title, 1024) || raw.HeadBranch == "" || !projectGitHubText(raw.HeadBranch, 1024) || !projectGitHubSHA.MatchString(raw.HeadSHA) || !projectGitHubRepositoryValid(raw.Repository) || raw.HeadRepository != nil && !projectGitHubRepositoryValid(raw.HeadRepository) {
		return empty, projectGitHubInvalid()
	}
	if raw.State != "OPEN" && raw.State != "CLOSED" && raw.State != "MERGED" || raw.URL != raw.Repository.URL+"/pull/"+strconv.Itoa(raw.Number) {
		return empty, projectGitHubInvalid()
	}
	pr := protocol.GitHubPullRequest{ID: raw.ID, Number: raw.Number, Title: raw.Title, URL: raw.URL, State: strings.ToLower(raw.State), BaseRepository: *raw.Repository, HeadBranch: raw.HeadBranch, HeadSHA: raw.HeadSHA}
	if raw.HeadRepository != nil {
		pr.HeadRepository = *raw.HeadRepository
	}
	if raw.HeadRef != nil && raw.HeadRef.Target != nil && raw.HeadRepository != nil {
		var err error
		pr.HeadChecks, err = projectGitHubChecks(raw.HeadRef.Target, pr.HeadRepository, pr.HeadSHA)
		if err != nil {
			return empty, err
		}
	}
	if raw.MergeCommit != nil {
		var err error
		pr.MergeChecks, err = projectGitHubChecks(raw.MergeCommit, pr.BaseRepository, raw.MergeCommit.OID)
		if err != nil {
			return empty, err
		}
	}
	return pr, nil
}

func projectGitHubChecks(commit *projectGitHubCommit, repository protocol.GitHubRepository, sha string) (*protocol.GitHubCheckSet, error) {
	if commit.Type != "Commit" || !projectGitHubSHA.MatchString(sha) || commit.OID != sha || !projectGitHubRepositoryValid(commit.Repository) || commit.Repository.ID != repository.ID || commit.Repository.NameWithOwner != repository.NameWithOwner || len(commit.Rollup) == 0 {
		return nil, projectGitHubInvalid()
	}
	result := &protocol.GitHubCheckSet{Repository: repository, SHA: sha, State: "none", Complete: true, Checks: []protocol.GitHubCheck{}}
	if bytes.Equal(bytes.TrimSpace(commit.Rollup), []byte("null")) {
		return result, nil
	}
	var rollup struct {
		State    string `json:"state"`
		Contexts *struct {
			TotalCount *int                    `json:"totalCount"`
			PageInfo   *projectGitHubPage      `json:"pageInfo"`
			Nodes      []*projectGitHubContext `json:"nodes"`
		} `json:"contexts"`
	}
	if json.Unmarshal(commit.Rollup, &rollup) != nil || rollup.Contexts == nil || rollup.Contexts.TotalCount == nil || rollup.Contexts.PageInfo == nil || rollup.Contexts.PageInfo.HasNextPage == nil || rollup.Contexts.Nodes == nil || len(rollup.Contexts.Nodes) > projectGitHubContextLimit || *rollup.Contexts.TotalCount < len(rollup.Contexts.Nodes) || !projectGitHubEnum.MatchString(rollup.State) {
		return nil, projectGitHubInvalid()
	}
	result.Complete = !*rollup.Contexts.PageInfo.HasNextPage && *rollup.Contexts.TotalCount == len(rollup.Contexts.Nodes)
	result.State = strings.ToLower(rollup.State)
	if !projectGitHubStatusState(rollup.State) {
		result.Complete = false
	}
	seen := make(map[string]bool)
	failed, pending := false, false
	for _, raw := range rollup.Contexts.Nodes {
		if raw == nil || !projectGitHubID.MatchString(raw.ID) || seen[raw.ID] {
			result.Complete = false
			continue
		}
		seen[raw.ID] = true
		check := protocol.GitHubCheck{}
		switch raw.Type {
		case "CheckRun":
			if !projectGitHubRepositoryValid(raw.Repository) || raw.Repository.ID != repository.ID || raw.CheckSuite == nil || raw.CheckSuite.Commit == nil || raw.CheckSuite.Commit.OID != sha || raw.Name == "" || !projectGitHubText(raw.Name, 512) || !projectGitHubEnum.MatchString(raw.Status) || raw.Conclusion != "" && !projectGitHubEnum.MatchString(raw.Conclusion) {
				return nil, projectGitHubInvalid()
			}
			check = protocol.GitHubCheck{Name: raw.Name, Source: "check-run", Status: strings.ToLower(raw.Status), Conclusion: strings.ToLower(raw.Conclusion), URL: projectGitHubSafeCheckURL(raw.URL, repository, sha)}
			switch raw.Status {
			case "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED", "PENDING":
				pending = true
			case "COMPLETED":
				switch raw.Conclusion {
				case "SUCCESS", "SKIPPED", "NEUTRAL":
				case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STALE", "STARTUP_FAILURE":
					failed = true
				default:
					result.Complete = false
				}
			default:
				result.Complete = false
			}
		case "StatusContext":
			if raw.Commit == nil || !projectGitHubRepositoryValid(raw.Commit.Repository) || raw.Commit.Repository.ID != repository.ID || raw.Commit.OID != sha || raw.Context == "" || !projectGitHubText(raw.Context, 512) || !projectGitHubEnum.MatchString(raw.State) {
				return nil, projectGitHubInvalid()
			}
			check = protocol.GitHubCheck{Name: raw.Context, Source: "commit-status", Status: "completed", Conclusion: strings.ToLower(raw.State), URL: projectGitHubSafeCheckURL(raw.TargetURL, repository, sha)}
			switch raw.State {
			case "SUCCESS":
			case "ERROR", "FAILURE":
				failed = true
			case "PENDING", "EXPECTED":
				check.Status, check.Conclusion = strings.ToLower(raw.State), ""
				pending = true
			default:
				result.Complete = false
			}
		default:
			result.Complete = false
			continue
		}
		result.Checks = append(result.Checks, check)
	}
	switch {
	case !result.Complete:
		result.State = "unknown"
	case len(result.Checks) == 0:
		result.State = "none"
	case failed:
		result.State = "failure"
	case pending && result.State == "success":
		result.State = "pending"
	}
	return result, nil
}

func projectGitHubStatusState(state string) bool {
	return state == "SUCCESS" || state == "FAILURE" || state == "ERROR" || state == "EXPECTED" || state == "PENDING"
}

func projectGitHubRepositoryValid(repository *protocol.GitHubRepository) bool {
	return repository != nil && projectGitHubID.MatchString(repository.ID) && projectGitHubSlug.MatchString(repository.NameWithOwner) && repository.URL == "https://github.com/"+repository.NameWithOwner
}

func projectGitHubText(text string, limit int) bool {
	return len(text) <= limit && utf8.ValidString(text) && strings.IndexFunc(text, unicode.IsControl) < 0
}

func projectGitHubSafeCheckURL(raw string, repository protocol.GitHubRepository, sha string) string {
	if raw == "" || len(raw) > 2048 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" {
		return ""
	}
	path, ok := strings.CutPrefix(u.Path, "/"+repository.NameWithOwner)
	if !ok || !projectGitHubCheckPath.MatchString(path) && path != "/commit/"+sha+"/checks" {
		return ""
	}
	return raw
}
