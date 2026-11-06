//go:build linux || darwin

package guest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

const (
	githubProviderLocalSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	githubProviderHeadSHA  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	githubProviderMergeSHA = "cccccccccccccccccccccccccccccccccccccccc"
)

func githubProviderRepository(id, name string) map[string]any {
	return map[string]any{"id": id, "nameWithOwner": name, "url": "https://github.com/" + name}
}

func githubProviderConnection(nodes ...any) map[string]any {
	if nodes == nil {
		nodes = []any{}
	}
	return map[string]any{"totalCount": len(nodes), "pageInfo": map[string]any{"hasNextPage": false}, "nodes": nodes}
}

func githubProviderCommit(sha string, checks ...any) map[string]any {
	return map[string]any{
		"__typename": "Commit", "oid": sha, "repository": githubProviderRepository("R_selected", "octo/work"),
		"statusCheckRollup":      map[string]any{"state": "SUCCESS", "contexts": githubProviderConnection(checks...)},
		"associatedPullRequests": githubProviderConnection(),
	}
}

func githubProviderCheck(id, sha, status, conclusion string) map[string]any {
	return map[string]any{
		"__typename": "CheckRun", "id": id, "name": "build", "status": status, "conclusion": conclusion,
		"url": "https://github.com/octo/work/actions/runs/123/job/456", "repository": githubProviderRepository("R_selected", "octo/work"),
		"checkSuite": map[string]any{"commit": map[string]any{"oid": sha}},
	}
}

func githubProviderStatus(id, sha, state string) map[string]any {
	return map[string]any{
		"__typename": "StatusContext", "id": id, "context": "build", "state": state,
		"targetUrl": "https://ci.example.test/private?token=secret",
		"commit":    map[string]any{"oid": sha, "repository": githubProviderRepository("R_selected", "octo/work")},
	}
}

func githubProviderPR(id string, number int) map[string]any {
	return map[string]any{
		"id": id, "number": number, "title": "A reviewed change", "url": "https://github.com/octo/work/pull/" + strconv.Itoa(number),
		"state": "OPEN", "headRefName": "work", "headRefOid": githubProviderHeadSHA,
		"repository":           githubProviderRepository("R_selected", "octo/work"),
		"headRepository":       githubProviderRepository("R_selected", "octo/work"),
		"headRef":              map[string]any{"target": githubProviderCommit(githubProviderHeadSHA, githubProviderCheck("C_head", githubProviderHeadSHA, "IN_PROGRESS", ""))},
		"potentialMergeCommit": githubProviderCommit(githubProviderMergeSHA, githubProviderCheck("C_merge", githubProviderMergeSHA, "COMPLETED", "FAILURE")),
	}
}

func githubProviderResponse() (map[string]any, map[string]any) {
	repository := githubProviderRepository("R_selected", "octo/work")
	repository["__typename"] = "Repository"
	repository["localCommit"] = githubProviderCommit(githubProviderLocalSHA, githubProviderCheck("C_local", githubProviderLocalSHA, "COMPLETED", "SUCCESS"))
	repository["branchPullRequests"] = githubProviderConnection()
	return map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "U_selected", "login": "octocat"}, "review": repository, "selected": repository}}, repository
}

func githubProviderJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The executable and all generated profiles stay in the test wrapper's private
// test root. The fixture has no network access or real credentials.
func githubProviderFixture(t *testing.T, payload []byte, extra string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, relative := range []string{".local/bin", ".config/axv/github"} {
		if err := os.MkdirAll(filepath.Join(home, relative), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for relative, data := range map[string][]byte{
		".config/axv/github/hosts.yml": []byte("fixture: no credential\n"),
		"github-response.json":         payload,
		".local/bin/gh":                []byte("#!/bin/sh\nset -eu\ncat > \"$HOME/github-input.json\"\nprintf '%s\\n' \"$@\" > \"$HOME/github-args\"\nenv > \"$HOME/github-env\"\n" + extra + "\nprintf 'HTTP/1.1 200 OK\\r\\nContent-Type: application/json\\r\\n\\r\\n'\ncat \"$HOME/github-response.json\"\n"),
	} {
		mode := os.FileMode(0600)
		if relative == ".local/bin/gh" {
			mode = 0700
		}
		if err := os.WriteFile(filepath.Join(home, relative), data, mode); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func githubProviderService() *service {
	return &service{ctx: context.Background(), executions: make(chan struct{}, 8)}
}

func githubProviderGit() protocol.GitHubGitState {
	return protocol.GitHubGitState{Repository: true, HeadSHA: githubProviderLocalSHA, Branch: "work", Remotes: []protocol.GitHubRemote{}}
}

func githubProviderErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var problem *projectFileError
	if !errors.As(err, &problem) || problem.code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestProjectGitHubProviderFixedExecution(t *testing.T) {
	payload, _ := githubProviderResponse()
	home := githubProviderFixture(t, githubProviderJSON(t, payload), "")
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST", "GH_REPO", "GH_CONFIG_DIR", "GH_DEBUG", "DEBUG", "HTTPS_PROXY", "SSL_CERT_FILE", "BASH_ENV", "GIT_CONFIG_GLOBAL", "GIT_SSH_COMMAND"} {
		t.Setenv(key, "ambient-secret-must-not-be-used")
	}
	result, err := githubProviderService().readProjectGitHub(t.Context(), protocol.ProjectGitHubRequest{Repository: "octo/work"}, githubProviderGit())
	if err != nil {
		t.Fatal(err)
	}
	if result.Repository.ID != "R_selected" || result.Account.ID != "U_selected" || result.LocalChecks != nil || result.PullRequestsComplete || result.ObservedAt == "" {
		t.Fatalf("unexpected observation: %+v", result)
	}
	args, err := os.ReadFile(filepath.Join(home, "github-args"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api", "--hostname", "github.com", "--method", "POST", "--header", "Accept: application/vnd.github+json", "--include", "--input", "-", "graphql"}
	if !reflect.DeepEqual(strings.Split(strings.TrimSpace(string(args)), "\n"), want) {
		t.Fatalf("argv = %q", args)
	}
	env, err := os.ReadFile(filepath.Join(home, "github-env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), "ambient-secret") || !strings.Contains(string(env), "GH_CONFIG_DIR="+filepath.Join(home, ".config/axv/github")+"\n") || !strings.Contains(string(env), "PWD="+home+"\n") || !strings.Contains(string(env), "GH_PROMPT_DISABLED=1\n") {
		t.Fatalf("unexpected child environment: %s", env)
	}
	input, err := os.ReadFile(filepath.Join(home, "github-input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(input, &request); err != nil {
		t.Fatal(err)
	}
	if request.Query != projectGitHubQuery || !strings.HasPrefix(request.Query, "query ") || strings.Contains(request.Query, "mutation ") || request.Variables["owner"] != "octo" || request.Variables["name"] != "work" || request.Variables["sha"] != githubProviderLocalSHA || request.Variables["byID"] != false || request.Variables["hasCommit"] != false || request.Variables["hasBranch"] != false {
		t.Fatalf("unexpected fixed query payload: %s", input)
	}
}

func TestProjectGitHubProviderReviewNeedsOnlyIdentityForSHA256(t *testing.T) {
	payload, repository := githubProviderResponse()
	delete(repository, "localCommit")
	delete(repository, "branchPullRequests")
	home := githubProviderFixture(t, githubProviderJSON(t, payload), "")
	git := githubProviderGit()
	git.HeadSHA = strings.Repeat("a", 64)
	result, err := githubProviderService().readProjectGitHub(t.Context(), protocol.ProjectGitHubRequest{Repository: "octo/work"}, git)
	if err != nil {
		t.Fatal(err)
	}
	if result.LocalChecks != nil || len(result.PullRequests) != 0 || result.PullRequestsComplete || result.Git.HeadSHA != git.HeadSHA || result.Repository.ID != "R_selected" || result.Account.ID != "U_selected" {
		t.Fatalf("review must contain only identities and captured Git: %+v", result)
	}
	input, err := os.ReadFile(filepath.Join(home, "github-input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Variables map[string]any `json:"variables"`
	}
	if json.Unmarshal(input, &request) != nil || request.Variables["hasCommit"] != false || request.Variables["hasBranch"] != false {
		t.Fatal("review requested check or pull-request fields")
	}
	_, err = projectGitHubCommand(t.Context(), home, protocol.ProjectGitHubRequest{RepositoryID: "R_selected", AccountID: "U_selected"}, git)
	githubProviderErrorCode(t, err, "github_git_unsupported")
}

func TestProjectGitHubProviderKeepsCommitAndCheckSourcesDistinct(t *testing.T) {
	payload, repository := githubProviderResponse()
	repository["localCommit"] = githubProviderCommit(githubProviderLocalSHA,
		githubProviderCheck("C_local", githubProviderLocalSHA, "COMPLETED", "SUCCESS"),
		githubProviderStatus("S_local", githubProviderLocalSHA, "FAILURE"))
	first, associated := githubProviderPR("PR_one", 1), githubProviderPR("PR_three", 3)
	wrongFork := githubProviderPR("PR_wrongfork", 2)
	wrongFork["headRepository"] = githubProviderRepository("R_fork", "other/work")
	wrongFork["headRef"] = nil
	repository["branchPullRequests"] = githubProviderConnection(first, wrongFork)
	repository["localCommit"].(map[string]any)["associatedPullRequests"] = githubProviderConnection(first, associated)
	result, err := parseProjectGitHub(githubProviderJSON(t, payload), protocol.ProjectGitHubRequest{RepositoryID: "R_selected", AccountID: "U_selected"}, githubProviderGit())
	if err != nil {
		t.Fatal(err)
	}
	if result.LocalChecks.State != "failure" || len(result.LocalChecks.Checks) != 2 || result.LocalChecks.Checks[0].Source != "check-run" || result.LocalChecks.Checks[1].Source != "commit-status" || result.LocalChecks.Checks[1].URL != "" {
		t.Fatalf("mixed checks = %+v", result.LocalChecks)
	}
	if len(result.PullRequests) != 2 || result.PullRequests[0].ID != "PR_one" || result.PullRequests[1].ID != "PR_three" {
		t.Fatalf("PR candidates = %+v", result.PullRequests)
	}
	pr := result.PullRequests[0]
	if pr.HeadSHA != githubProviderHeadSHA || pr.HeadChecks.SHA != githubProviderHeadSHA || pr.HeadChecks.State != "pending" || pr.MergeChecks.SHA != githubProviderMergeSHA || pr.MergeChecks.State != "failure" || pr.BaseRepository.ID != "R_selected" || !result.PullRequestsComplete {
		t.Fatalf("commit scopes = %+v", pr)
	}
}

func TestProjectGitHubProviderDetachedCommitAndForkPR(t *testing.T) {
	payload, repository := githubProviderResponse()
	pr := githubProviderPR("PR_one", 1)
	pr["repository"] = githubProviderRepository("R_parent", "parent/work")
	pr["url"] = "https://github.com/parent/work/pull/1"
	pr["potentialMergeCommit"] = nil
	repository["localCommit"].(map[string]any)["associatedPullRequests"] = githubProviderConnection(pr)
	delete(repository, "branchPullRequests")
	git := githubProviderGit()
	git.Branch, git.Detached = "", true
	result, err := parseProjectGitHub(githubProviderJSON(t, payload), protocol.ProjectGitHubRequest{RepositoryID: "R_selected", AccountID: "U_selected"}, git)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PullRequests) != 1 || result.PullRequests[0].BaseRepository.ID != "R_parent" || result.PullRequests[0].HeadRepository.ID != "R_selected" || result.PullRequests[0].HeadSHA == git.HeadSHA || result.PullRequests[0].MergeChecks != nil {
		t.Fatalf("detached association = %+v", result)
	}
}

func TestProjectGitHubProviderRejectsPartialAndChangedIdentities(t *testing.T) {
	for _, test := range []struct {
		name, code string
		edit       func(map[string]any, map[string]any)
	}{
		{"account switched", "github_account_changed", func(p, _ map[string]any) {
			p["data"].(map[string]any)["viewer"] = map[string]any{"id": "U_other", "login": "someone"}
		}},
		{"repository changed", "github_repository_changed", func(_, r map[string]any) { r["id"] = "R_other" }},
		{"GraphQL partial data", "github_forbidden", func(p, _ map[string]any) {
			p["errors"] = []any{map[string]any{"type": "FORBIDDEN", "message": "secret provider diagnostic"}}
		}},
		{"GraphQL insufficient scopes", "github_forbidden", func(p, _ map[string]any) {
			p["errors"] = []any{map[string]any{"type": "INSUFFICIENT_SCOPES", "message": "secret provider scope diagnostic"}}
		}},
		{"GraphQL rate limit", "github_rate_limited", func(p, _ map[string]any) { p["errors"] = []any{map[string]any{"type": "RATE_LIMITED"}} }},
		{"GraphQL unknown error", "github_response_invalid", func(p, _ map[string]any) {
			p["errors"] = []any{map[string]any{"message": "secret provider diagnostic"}}
		}},
		{"missing viewer", "github_response_invalid", func(p, _ map[string]any) { delete(p["data"].(map[string]any), "viewer") }},
		{"wrong local SHA", "github_response_invalid", func(_, r map[string]any) { r["localCommit"].(map[string]any)["oid"] = githubProviderHeadSHA }},
		{"wrong check SHA", "github_response_invalid", func(_, r map[string]any) {
			r["localCommit"] = githubProviderCommit(githubProviderLocalSHA, githubProviderCheck("C_wrong", githubProviderHeadSHA, "COMPLETED", "SUCCESS"))
		}},
		{"wrong status source", "github_response_invalid", func(_, r map[string]any) {
			c := githubProviderStatus("S_wrong", githubProviderLocalSHA, "SUCCESS")
			c["commit"].(map[string]any)["repository"] = githubProviderRepository("R_fork", "other/work")
			r["localCommit"] = githubProviderCommit(githubProviderLocalSHA, c)
		}},
		{"credential URL", "github_response_invalid", func(_, r map[string]any) { r["url"] = "https://secret@github.com/octo/work" }},
		{"missing pagination", "github_response_invalid", func(_, r map[string]any) { delete(r["branchPullRequests"].(map[string]any), "pageInfo") }},
		{"duplicate PR advanced during read", "github_response_invalid", func(_, r map[string]any) {
			r["branchPullRequests"] = githubProviderConnection(githubProviderPR("PR_one", 1))
			newer := githubProviderPR("PR_one", 1)
			newer["title"] = "Changed during the read"
			r["localCommit"].(map[string]any)["associatedPullRequests"] = githubProviderConnection(newer)
		}},
		{"forged branch base repository", "github_response_invalid", func(_, r map[string]any) {
			pr := githubProviderPR("PR_one", 1)
			pr["repository"] = githubProviderRepository("R_other", "other/work")
			pr["url"] = "https://github.com/other/work/pull/1"
			pr["potentialMergeCommit"] = nil
			r["branchPullRequests"] = githubProviderConnection(pr)
		}},
		{"oversized PR page", "github_response_invalid", func(_, r map[string]any) {
			nodes := make([]any, projectGitHubPRLimit+1)
			for i := range nodes {
				nodes[i] = githubProviderPR("PR_one", 1)
			}
			r["branchPullRequests"] = githubProviderConnection(nodes...)
		}},
		{"oversized context page", "github_response_invalid", func(_, r map[string]any) {
			nodes := make([]any, projectGitHubContextLimit+1)
			for i := range nodes {
				nodes[i] = githubProviderCheck("C_local", githubProviderLocalSHA, "COMPLETED", "SUCCESS")
			}
			r["localCommit"] = githubProviderCommit(githubProviderLocalSHA, nodes...)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, repository := githubProviderResponse()
			test.edit(payload, repository)
			_, err := parseProjectGitHub(githubProviderJSON(t, payload), protocol.ProjectGitHubRequest{RepositoryID: "R_selected", AccountID: "U_selected"}, githubProviderGit())
			githubProviderErrorCode(t, err, test.code)
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("provider diagnostic escaped: %v", err)
			}
		})
	}
	for _, payload := range []string{`null`, `{"data":`, `{"data":null}`, `{"errors":[null]}`, `{} {}`, `{"data":{"viewer":{"login":"\ud800"}}}`} {
		_, err := parseProjectGitHub([]byte(payload), protocol.ProjectGitHubRequest{Repository: "octo/work"}, githubProviderGit())
		githubProviderErrorCode(t, err, "github_response_invalid")
	}
}

func TestProjectGitHubProviderRequestCannotSupplyCommands(t *testing.T) {
	for _, request := range []protocol.ProjectGitHubRequest{
		{Repository: "octo/work;touch bad"},
		{Repository: "https://github.com/octo/work"},
		{Repository: "octo/work", RepositoryID: "R_selected", AccountID: "U_selected"},
		{RepositoryID: "R_selected"},
		{RepositoryID: "R_selected", AccountID: "U_selected\nGH_TOKEN=bad"},
	} {
		_, err := projectGitHubCommand(t.Context(), t.TempDir(), request, githubProviderGit())
		githubProviderErrorCode(t, err, "invalid_request")
	}
	git := githubProviderGit()
	git.HeadSHA = "HEAD"
	_, err := projectGitHubCommand(t.Context(), t.TempDir(), protocol.ProjectGitHubRequest{RepositoryID: "R_selected", AccountID: "U_selected"}, git)
	githubProviderErrorCode(t, err, "git_metadata_invalid")
	git = githubProviderGit()
	git.Branch = `feature/$(touch-should-never-execute)`
	command, err := projectGitHubCommand(t.Context(), t.TempDir(), protocol.ProjectGitHubRequest{RepositoryID: "R_selected", AccountID: "U_selected"}, git)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(command.Args, " "), git.Branch) {
		t.Fatal("branch data became command arguments")
	}
	var payload struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(command.Stdin).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Query != projectGitHubQuery || payload.Variables["byID"] != true || payload.Variables["repositoryID"] != "R_selected" || payload.Variables["branch"] != git.Branch {
		t.Fatalf("snapshot variables were not fenced: %+v", payload.Variables)
	}
}

func TestProjectGitHubProviderTruncationAndAbsentResultsNeverPass(t *testing.T) {
	for _, test := range []struct {
		name, state           string
		complete, prsComplete bool
		edit                  func(map[string]any)
	}{
		{"truncated checks", "unknown", false, true, func(r map[string]any) {
			c := r["localCommit"].(map[string]any)["statusCheckRollup"].(map[string]any)["contexts"].(map[string]any)
			c["totalCount"] = 70
			c["pageInfo"] = map[string]any{"hasNextPage": true}
		}},
		{"unknown check conclusion", "unknown", false, true, func(r map[string]any) {
			r["localCommit"] = githubProviderCommit(githubProviderLocalSHA, githubProviderCheck("C_new", githubProviderLocalSHA, "COMPLETED", "FUTURE_STATE"))
		}},
		{"empty checks", "none", true, true, func(r map[string]any) { r["localCommit"] = githubProviderCommit(githubProviderLocalSHA) }},
		{"no rollup", "none", true, true, func(r map[string]any) { r["localCommit"].(map[string]any)["statusCheckRollup"] = nil }},
		{"unpublished commit", "unavailable", false, false, func(r map[string]any) { r["localCommit"] = nil }},
		{"truncated PRs", "success", true, false, func(r map[string]any) {
			c := r["branchPullRequests"].(map[string]any)
			c["totalCount"] = 30
			c["pageInfo"] = map[string]any{"hasNextPage": true}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, repository := githubProviderResponse()
			test.edit(repository)
			result, err := parseProjectGitHub(githubProviderJSON(t, payload), protocol.ProjectGitHubRequest{RepositoryID: "R_selected", AccountID: "U_selected"}, githubProviderGit())
			if err != nil {
				t.Fatal(err)
			}
			if result.LocalChecks.State != test.state || result.LocalChecks.Complete != test.complete || result.PullRequestsComplete != test.prsComplete {
				t.Fatalf("unexpected coverage: %+v / PRs=%v", result.LocalChecks, result.PullRequestsComplete)
			}
		})
	}
}

func TestProjectGitHubProviderAllGitHubCheckStates(t *testing.T) {
	for _, status := range []string{"QUEUED", "IN_PROGRESS", "REQUESTED", "WAITING", "PENDING"} {
		t.Run(status, func(t *testing.T) {
			payload, repository := githubProviderResponse()
			repository["localCommit"] = githubProviderCommit(githubProviderLocalSHA, githubProviderCheck("C_wait", githubProviderLocalSHA, status, ""))
			result, err := parseProjectGitHub(githubProviderJSON(t, payload), protocol.ProjectGitHubRequest{RepositoryID: "R_selected", AccountID: "U_selected"}, githubProviderGit())
			if err != nil {
				t.Fatal(err)
			}
			if result.LocalChecks.State != "pending" || result.LocalChecks.Checks[0].Status != strings.ToLower(status) {
				t.Fatalf("pending state = %+v", result.LocalChecks)
			}
		})
	}
	for _, conclusion := range []string{"FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STALE", "STARTUP_FAILURE", "SUCCESS", "SKIPPED", "NEUTRAL"} {
		t.Run(conclusion, func(t *testing.T) {
			payload, repository := githubProviderResponse()
			repository["localCommit"] = githubProviderCommit(githubProviderLocalSHA, githubProviderCheck("C_done", githubProviderLocalSHA, "COMPLETED", conclusion))
			result, err := parseProjectGitHub(githubProviderJSON(t, payload), protocol.ProjectGitHubRequest{RepositoryID: "R_selected", AccountID: "U_selected"}, githubProviderGit())
			if err != nil {
				t.Fatal(err)
			}
			state := "failure"
			if conclusion == "SUCCESS" || conclusion == "SKIPPED" || conclusion == "NEUTRAL" {
				state = "success"
			}
			if result.LocalChecks.State != state || result.LocalChecks.Checks[0].Conclusion != strings.ToLower(conclusion) {
				t.Fatalf("conclusion = %+v", result.LocalChecks)
			}
		})
	}
}

func TestProjectGitHubProviderPrivateProfileAndBoundedFailures(t *testing.T) {
	for _, test := range []struct {
		name, code, extra string
		setup             func(*testing.T, string)
	}{
		{"missing CLI", "github_cli_missing", "", func(t *testing.T, home string) {
			if err := os.Remove(filepath.Join(home, ".local/bin/gh")); err != nil {
				t.Fatal(err)
			}
		}},
		{"no dedicated profile", "github_auth_required", "", func(t *testing.T, home string) {
			if err := os.RemoveAll(filepath.Join(home, ".config/axv/github")); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(home, ".config/gh"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".config/gh/hosts.yml"), []byte("default must never be reused"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"public profile metadata", "github_profile_invalid", "", func(t *testing.T, home string) {
			if err := os.Chmod(filepath.Join(home, ".config/axv/github/hosts.yml"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing authentication", "github_auth_required", "printf 'secret-sentinel gh auth login\\n' >&2\nexit 1", nil},
		{"offline", "github_unavailable", "printf 'secret-sentinel error connecting to api.github.com\\n' >&2\nexit 1", nil},
		{"limited", "github_rate_limited", "printf 'HTTP/1.1 403 Forbidden\\r\\nX-RateLimit-Remaining: 0\\r\\n\\r\\n{}'\nexit 1", nil},
		{"bad output", "github_response_invalid", "printf 'secret-sentinel malformed output'\nexit 0", nil},
		{"large output", "github_response_invalid", "head -c 2097153 /dev/zero\nexit 0", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, _ := githubProviderResponse()
			home := githubProviderFixture(t, githubProviderJSON(t, payload), test.extra)
			if test.setup != nil {
				test.setup(t, home)
			}
			_, err := githubProviderService().readProjectGitHub(t.Context(), protocol.ProjectGitHubRequest{Repository: "octo/work"}, githubProviderGit())
			githubProviderErrorCode(t, err, test.code)
			if strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatalf("diagnostic escaped: %v", err)
			}
		})
	}
	t.Run("deadline", func(t *testing.T) {
		payload, _ := githubProviderResponse()
		githubProviderFixture(t, githubProviderJSON(t, payload), "sleep 30")
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := githubProviderService().readProjectGitHub(ctx, protocol.ProjectGitHubRequest{Repository: "octo/work"}, githubProviderGit())
		githubProviderErrorCode(t, err, "github_timeout")
	})
}

func TestProjectGitHubProviderAllowsOnlyScopedGitHubLinks(t *testing.T) {
	repository := protocol.GitHubRepository{ID: "R_selected", NameWithOwner: "octo/work", URL: "https://github.com/octo/work"}
	for _, path := range []string{"/actions/runs/123", "/actions/runs/123/job/456", "/runs/123", "/pull/1/checks", "/commit/" + githubProviderLocalSHA + "/checks"} {
		value := repository.URL + path
		if got := projectGitHubSafeCheckURL(value, repository, githubProviderLocalSHA); got != value {
			t.Fatalf("valid link rejected: %s", value)
		}
	}
	for _, value := range []string{"javascript:alert(1)", "https://secret@github.com/octo/work/runs/1", "http://github.com/octo/work/runs/1", "https://github.com.evil.test/octo/work/runs/1", "https://github.com:443/octo/work/runs/1", "https://github.com/other/work/runs/1", "https://github.com/octo/work/runs/1?token=secret", "https://github.com/octo/work/runs/1#secret", "https://github.com/octo/work/../other/runs/1", "https://github.com/octo/work/commit/" + githubProviderHeadSHA + "/checks", "https://github.com/octo/work/%72uns/1", "https://ci.example.test/run/1"} {
		if got := projectGitHubSafeCheckURL(value, repository, githubProviderLocalSHA); got != "" {
			t.Fatalf("unsafe link accepted: %s", value)
		}
	}
}
