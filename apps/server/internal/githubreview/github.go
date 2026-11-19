package githubreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	githubHost                = "github.com"
	githubAPIHost             = "api.github.com"
	githubPageSize            = 100
	githubMaxPages            = 10
	maxAuthorTitleBytes       = 1 << 10
	maxAuthorDescriptionBytes = 8 << 10
)

var (
	githubAccountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	githubStatusPattern  = regexp.MustCompile(`\bHTTP ([45][0-9]{2})\b`)

	ErrGitHubLoginRequired = errors.New("Connect a GitHub CLI account to continue.")
	ErrGitHubAccountChange = errors.New("The active GitHub account changed. Reconnect before reviewing or publishing.")
	ErrGitHubRateLimited   = errors.New("GitHub rate limited the request. Try again after the rate limit resets.")
	ErrGitHubForbidden     = errors.New("GitHub did not grant permission for this operation.")
	ErrTargetChanged       = errors.New("The pull request changed or is no longer ready for review.")
)

// GitHubCLI uses the deployment user's official GitHub CLI credentials.
type GitHubCLI struct {
	Directory  string
	Executable string
}

func (g GitHubCLI) executable() (string, error) {
	name := g.Executable
	if name == "" {
		name = "gh"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", errors.New("GitHub CLI (gh) is unavailable on the AXV machine.")
	}
	return path, nil
}

func (g GitHubCLI) command(ctx context.Context, token string, args ...string) (*exec.Cmd, error) {
	path, err := g.executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = g.Directory
	cmd.Env = cliEnvironment("gh", "")
	if token != "" {
		cmd.Env = append(cmd.Env, "GH_TOKEN="+token)
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd, nil
}

type cliOutput struct {
	// Keep Write as the sole input path even when os/exec uses io.Copy.
	data      []byte
	limit     int
	truncated bool
}

func (out *cliOutput) Write(p []byte) (int, error) {
	count := len(p)
	if remaining := out.limit - len(out.data); count > remaining {
		p = p[:remaining]
		out.truncated = true
	}
	out.data = append(out.data, p...)
	return count, nil
}

func (out *cliOutput) String() string { return string(out.data) }

func (g GitHubCLI) output(ctx context.Context, token string, input io.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, err := g.command(ctx, token, args...)
	if err != nil {
		return nil, err
	}
	stdout := cliOutput{limit: 512 << 10}
	stderr := cliOutput{limit: 8 << 10}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, githubCLIError(stderr.String())
	}
	if stdout.truncated {
		return nil, errors.New("The GitHub response exceeded the supported size.")
	}
	return stdout.data, nil
}

func githubCLIError(stderr string) error {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "http 429") {
		return ErrGitHubRateLimited
	}
	if strings.Contains(lower, "gh auth login") || strings.Contains(lower, "no oauth token found") || strings.Contains(lower, "http 401") {
		return ErrGitHubLoginRequired
	}
	if matches := githubStatusPattern.FindStringSubmatch(stderr); len(matches) == 2 {
		if matches[1] == "403" {
			return ErrGitHubForbidden
		}
		return fmt.Errorf("GitHub returned HTTP %s.", matches[1])
	}
	return errors.New("The GitHub CLI request failed. Check the connection to GitHub.")
}

func (g GitHubCLI) api(ctx context.Context, token string, result any, input io.Reader, args ...string) error {
	args = append([]string{"api", "--hostname", githubHost}, args...)
	data, err := g.output(ctx, token, input, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("GitHub returned an unexpected response.")
	}
	return nil
}

func (g GitHubCLI) storedToken(ctx context.Context) (string, error) {
	data, err := g.output(ctx, "", nil, "auth", "token", "--hostname", githubHost)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n\t ") {
		return "", ErrGitHubLoginRequired
	}
	return token, nil
}

func (g GitHubCLI) tokenAccount(ctx context.Context, token string) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := g.api(ctx, token, &user, nil, "user", "--jq", "{login}"); err != nil {
		return "", err
	}
	if !githubAccountPattern.MatchString(user.Login) {
		return "", errors.New("GitHub did not return a valid account.")
	}
	return user.Login, nil
}

func (g GitHubCLI) Account(ctx context.Context) (string, error) {
	token, err := g.storedToken(ctx)
	if err != nil {
		return "", err
	}
	return g.tokenAccount(ctx, token)
}

// Token returns the active stored credential only after validating that exact
// credential against the captured account. It never enters browser state.
func (g GitHubCLI) Token(ctx context.Context, account string) (string, error) {
	if !githubAccountPattern.MatchString(account) {
		return "", ErrGitHubLoginRequired
	}
	token, err := g.storedToken(ctx)
	if err != nil {
		return "", err
	}
	current, err := g.tokenAccount(ctx, token)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(current, account) {
		return "", ErrGitHubAccountChange
	}
	return token, nil
}

type githubPull struct {
	Description *string  `json:"description"`
	Repository  string   `json:"repository"`
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	BaseSHA     string   `json:"baseSha"`
	HeadSHA     string   `json:"headSha"`
	State       string   `json:"state"`
	Draft       bool     `json:"draft"`
	Reviewers   []string `json:"reviewers"`
}

// Discovery reads compact PR identities. Fetch author text with the single-PR
// preflight so queued work uses metadata from the same head verification.
const githubPullFields = `{repository:.base.repo.full_name,number,title,baseSha:.base.sha,headSha:.head.sha,state,draft,reviewers:[.requested_reviewers[]?.login]}`

// jq slices by Unicode code point. Read one extra to preserve the truncation
// signal, then enforce the UTF-8 byte budget before storing or sending text.
var githubReviewPullFields = githubPullFields + ` + {description:((.body // "")[:` + strconv.Itoa(maxAuthorDescriptionBytes+1) + `])}`

func prefixUTF8(text string, maxBytes int) (string, bool) {
	if len(text) <= maxBytes {
		return text, false
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}

func (pr githubPull) target(repository, account, source string) (Target, error) {
	title, titleTruncated := prefixUTF8(pr.Title, maxAuthorTitleBytes)
	target := Target{Repository: pr.Repository, Number: pr.Number, Title: title, Source: source,
		BaseSHA: pr.BaseSHA, HeadSHA: pr.HeadSHA, Draft: pr.Draft, Closed: pr.State == "closed"}
	if !target.valid() || !strings.EqualFold(pr.Repository, repository) || (pr.State != "open" && pr.State != "closed") {
		return Target{}, errors.New("GitHub returned a different or invalid pull request target.")
	}
	if pr.Description != nil {
		text, truncated := prefixUTF8(*pr.Description, maxAuthorDescriptionBytes)
		target.authorContext = &AuthorContext{Title: title, Description: text, TitleTruncated: titleTruncated, DescriptionTruncated: truncated}
	}
	for _, reviewer := range pr.Reviewers {
		if strings.EqualFold(reviewer, account) {
			target.Requested = true
			break
		}
	}
	return target, nil
}

func githubPullEndpoint(target Target) string {
	return fmt.Sprintf("repos/%s/pulls/%d", target.Repository, target.Number)
}

func (g GitHubCLI) current(ctx context.Context, token, account string, target Target, fields string) (Target, error) {
	if !validRepository(target.Repository) || target.Number <= 0 {
		return Target{}, errors.New("Invalid GitHub pull request target.")
	}
	var raw githubPull
	if err := g.api(ctx, token, &raw, nil, githubPullEndpoint(target), "--jq", fields); err != nil {
		return Target{}, err
	}
	if raw.Number != target.Number {
		return Target{}, errors.New("GitHub returned a different pull request number.")
	}
	return raw.target(target.Repository, account, target.Source)
}

func (g GitHubCLI) Current(ctx context.Context, target Target, account string) (Target, error) {
	token, err := g.Token(ctx, account)
	if err != nil {
		return Target{}, err
	}
	return g.current(ctx, token, account, target, githubReviewPullFields)
}

type githubSearchResult struct {
	Items []struct {
		RepositoryURL string `json:"repository_url"`
		Number        int    `json:"number"`
	} `json:"items"`
	Incomplete bool `json:"incomplete_results"`
	Total      int  `json:"total_count"`
}

func repositoryFromSearchURL(raw string) string {
	prefix := "https://" + githubAPIHost + "/repos/"
	repository, ok := strings.CutPrefix(raw, prefix)
	if !ok || !validRepository(repository) {
		return ""
	}
	return repository
}

func (g GitHubCLI) requested(ctx context.Context, token, account string) ([]Target, error) {
	var targets []Target
	var failures []error
	for page := 1; page <= githubMaxPages; page++ {
		var result githubSearchResult
		err := g.api(ctx, token, &result, nil, "search/issues", "--method", "GET",
			"--raw-field", "q=is:pr is:open user-review-requested:@me draft:false",
			"--raw-field", "sort=updated", "--raw-field", "order=desc",
			"--raw-field", "per_page="+strconv.Itoa(githubPageSize), "--raw-field", "page="+strconv.Itoa(page),
			"--jq", `{incomplete_results,total_count,items:[.items[]|{repository_url,number}]}`)
		if err != nil {
			return targets, errors.Join(append(failures, err)...)
		}
		if result.Incomplete {
			failures = append(failures, errors.New("GitHub reported incomplete review-request search results."))
		}
		for _, item := range result.Items {
			repository := repositoryFromSearchURL(item.RepositoryURL)
			if repository == "" || item.Number <= 0 {
				failures = append(failures, errors.New("GitHub returned an invalid review request target."))
				continue
			}
			target, err := g.current(ctx, token, account, Target{Repository: repository, Number: item.Number, Source: sourceRequested}, githubPullFields)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s#%d: %w", repository, item.Number, err))
				if ctx.Err() != nil || errors.Is(err, ErrGitHubRateLimited) {
					return targets, errors.Join(failures...)
				}
				continue
			}
			if !target.Closed && !target.Draft && target.Requested {
				targets = append(targets, target)
			}
		}
		if len(result.Items) < githubPageSize || page*githubPageSize >= result.Total {
			return targets, errors.Join(failures...)
		}
	}
	return targets, errors.Join(append(failures, errors.New("GitHub review-request search reached its 1,000-result limit."))...)
}

func (g GitHubCLI) repositoryPulls(ctx context.Context, token, account, repository string) ([]Target, error) {
	if !validRepository(repository) {
		return nil, errors.New("Invalid GitHub repository. Enter owner/repository.")
	}
	var targets []Target
	var failures []error
	for page := 1; page <= githubMaxPages; page++ {
		var pulls []githubPull
		endpoint := fmt.Sprintf("repos/%s/pulls?state=open&sort=updated&direction=desc&per_page=%d&page=%d", repository, githubPageSize, page)
		if err := g.api(ctx, token, &pulls, nil, endpoint, "--jq", "[.[]|"+githubPullFields+"]"); err != nil {
			return targets, errors.Join(append(failures, err)...)
		}
		for _, pr := range pulls {
			target, err := pr.target(repository, account, sourceRepository)
			if err != nil {
				failures = append(failures, err)
			} else if !target.Closed {
				targets = append(targets, target)
			}
		}
		if len(pulls) < githubPageSize {
			return targets, errors.Join(failures...)
		}
	}
	return targets, errors.Join(append(failures, errors.New("The repository exceeded the supported pull request scan size."))...)
}

func (g GitHubCLI) Discover(ctx context.Context, repositories []string, account string) ([]Target, error) {
	token, err := g.Token(ctx, account)
	if err != nil {
		return nil, err
	}
	targets, directErr := g.requested(ctx, token, account)
	var failures []error
	if directErr != nil {
		failures = append(failures, fmt.Errorf("Review requests: %w", directErr))
		if ctx.Err() != nil || errors.Is(directErr, ErrGitHubRateLimited) {
			return targets, errors.Join(failures...)
		}
	}
	seen := make(map[string]bool)
	for _, target := range targets {
		seen[githubTargetKey(target)] = true
	}
	for _, repository := range repositories {
		pulls, err := g.repositoryPulls(ctx, token, account, repository)
		for _, target := range pulls {
			key := githubTargetKey(target)
			if !seen[key] {
				seen[key] = true
				targets = append(targets, target)
			}
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", repository, err))
			if ctx.Err() != nil || errors.Is(err, ErrGitHubRateLimited) {
				break
			}
		}
	}
	return targets, errors.Join(failures...)
}

func githubTargetKey(target Target) string {
	return strings.ToLower(target.Repository) + "#" + strconv.Itoa(target.Number)
}

type githubComment struct {
	URL string `json:"html_url"`
}

func checkCommentURL(target Target, raw string) (string, error) {
	u, err := url.Parse(raw)
	expected := "/" + target.Repository + "/pull/" + strconv.Itoa(target.Number)
	if err != nil || u.Scheme != "https" || u.Host != githubHost || u.User != nil ||
		!strings.EqualFold(u.Path, expected) || u.RawQuery != "" {
		return "", errors.New("GitHub did not return a valid comment link.")
	}
	id, ok := strings.CutPrefix(u.Fragment, "issuecomment-")
	number, parseErr := strconv.ParseUint(id, 10, 64)
	if !ok || parseErr != nil || number == 0 {
		return "", errors.New("GitHub did not return a valid comment link.")
	}
	return u.String(), nil
}

func (g GitHubCLI) findComment(ctx context.Context, token, account, endpoint, marker string) (githubComment, error) {
	// Only a marker on its own line and authored by the connected account may
	// reconcile an uncertain write.
	loginJSON, _ := json.Marshal(strings.ToLower(account))
	markerJSON, _ := json.Marshal(marker)
	query := `{count:length,match:([.[] | select((.user.login // "" | ascii_downcase) == ` +
		string(loginJSON) + `) | select((.body // "" | split("\n") | index(` + string(markerJSON) + `)) != null) | {html_url}] | first)}`
	for page := 1; page <= githubMaxPages; page++ {
		var result struct {
			Count int           `json:"count"`
			Match githubComment `json:"match"`
		}
		path := fmt.Sprintf("%s?per_page=%d&page=%d", endpoint, githubPageSize, page)
		if err := g.api(ctx, token, &result, nil, path, "--jq", query); err != nil {
			return githubComment{}, err
		}
		if result.Match.URL != "" || result.Count < githubPageSize {
			return result.Match, nil
		}
	}
	return githubComment{}, errors.New("GitHub comments exceeded the supported reconciliation scan size.")
}

// Publish adds one conversation comment without satisfying a review request.
func (g GitHubCLI) Publish(ctx context.Context, target Target, account, marker, body string) (string, error) {
	if !target.valid() ||
		marker == "" || strings.ContainsAny(marker, "\r\n") ||
		!strings.HasPrefix(marker, "<!-- ") || !strings.HasSuffix(marker, " -->") ||
		strings.TrimSpace(body) == "" || len(body)+len(marker) > 60_000 {
		return "", errors.New("Invalid GitHub review comment.")
	}
	token, err := g.Token(ctx, account)
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("repos/%s/issues/%d/comments", target.Repository, target.Number)
	comment, err := g.findComment(ctx, token, account, endpoint, marker)
	if err != nil {
		return "", err
	}
	if comment.URL != "" {
		return checkCommentURL(target, comment.URL)
	}
	current, err := g.current(ctx, token, account, target, githubPullFields)
	if err != nil {
		return "", err
	}
	if current.Closed || current.Draft || current.HeadSHA != target.HeadSHA || current.BaseSHA != target.BaseSHA ||
		(target.Source == sourceRequested && !current.Requested) {
		return "", ErrTargetChanged
	}
	payload, err := json.Marshal(map[string]string{"body": marker + "\n\n" + body})
	if err != nil {
		return "", err
	}
	err = g.api(ctx, token, &comment, bytes.NewReader(payload), endpoint, "--method", "POST", "--input", "-", "--jq", "{html_url}")
	if err != nil {
		return "", err
	}
	return checkCommentURL(target, comment.URL)
}

// Restrict subprocess inputs to the device login and provider client settings.
// In particular, ambient API keys and GitHub tokens cannot select an account.
func cliEnvironment(provider, proxy string) []string {
	env := []string{"NO_COLOR=1", "TERM=dumb"}
	for _, key := range []string{
		"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "TMPDIR", "TMP", "TEMP",
		"SystemRoot", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR",
		"DBUS_SESSION_BUS_ADDRESS", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy",
	} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	if provider == "gh" {
		env = append(env, "GH_PROMPT_DISABLED=1")
		if value := os.Getenv("GH_CONFIG_DIR"); value != "" {
			env = append(env, "GH_CONFIG_DIR="+value)
		}
	}
	if provider == "codex" {
		for _, key := range []string{"CODEX_HOME", "CODEX_CA_CERTIFICATE"} {
			if value := os.Getenv(key); value != "" {
				env = append(env, key+"="+value)
			}
		}
		if proxy != "" && !hasCLIProxy() {
			if strings.HasPrefix(proxy, "socks5") {
				env = append(env, "ALL_PROXY="+proxy, "all_proxy="+proxy)
			} else {
				env = append(env, "HTTP_PROXY="+proxy, "http_proxy="+proxy, "HTTPS_PROXY="+proxy, "https_proxy="+proxy)
			}
		}
	}
	return env
}

func hasCLIProxy() bool {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}
