//go:build !windows

package githubreview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const fixtureGH = `#!/bin/sh
set -eu
if [ "$1" = auth ] && [ "$2" = token ]; then
  [ -z "${GH_TOKEN:-}" ] && [ -z "${GITHUB_TOKEN:-}" ]
  if [ -f signed-in ]; then printf 'fixture-token\n'; else printf 'no oauth token found for github.com\n' >&2; exit 1; fi
  exit
fi
if [ "$1" = auth ] && [ "$2" = login ]; then
  [ -z "${GH_TOKEN:-}" ] && [ -z "${GITHUB_TOKEN:-}" ] && [ "${GH_PROMPT_DISABLED:-}" = 1 ]
  printf '! First copy your one-time code: AB12-CD34\nOpen this URL to continue in your web browser: https://github.com/login/device\n' >&2
  i=0
  while [ ! -f signed-in ] && [ "$i" -lt 50 ]; do
    i=$((i + 1))
    sleep 0.02
  done
  [ -f signed-in ]
  exit
fi
[ "${GH_TOKEN:-}" = fixture-token ] && [ -z "${GITHUB_TOKEN:-}" ]
[ "$1" = api ] && [ "$2" = --hostname ] && [ "$3" = github.com ]
case "$4" in
  user)
    printf '{"login":"reviewer"}'
    ;;
  search/issues)
    case "$*" in *'q=is:pr is:open user-review-requested:@me draft:false'*) ;; *) exit 1 ;; esac
    printf '{"items":[{"repository_url":"https://api.github.com/repos/external/repo","number":21},{"repository_url":"https://api.github.com/repos/watched/repo","number":12},{"repository_url":"https://api.github.com/repos/external/repo","number":22}],"total_count":3}'
    ;;
  repos/external/repo/pulls/21|repos/external/repo/pulls/22|repos/watched/repo/pulls/12)
    repo=${4#repos/}
    repo=${repo%/pulls/*}
    number=${4##*/}
    reviewers='["reviewer"]'
    if [ "$number" = 22 ]; then reviewers='[]'; fi
    head=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    if [ -f new-head ]; then head=cccccccccccccccccccccccccccccccccccccccc; fi
    printf '{"repository":"%s","number":%s,"title":"Change","baseSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","headSha":"%s","state":"open","draft":false,"reviewers":%s}' "$repo" "$number" "$head" "$reviewers"
    ;;
  repos/watched/repo/pulls\?*)
    printf '[{"repository":"watched/repo","number":12,"baseSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","headSha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","state":"open","draft":false,"reviewers":["reviewer"]},{"repository":"watched/repo","number":13,"baseSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","headSha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","state":"open","draft":false,"reviewers":[]},{"repository":"watched/repo","number":14,"baseSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","headSha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","state":"open","draft":true}]'
    ;;
  repos/missing/repo/pulls\?*)
    printf 'gh: Resource not found (HTTP 404)\n' >&2
    exit 1
    ;;
  repos/external/repo/issues/21/comments\?*)
    if [ -f posted ]; then
      printf '{"count":1,"match":{"html_url":"https://github.com/external/repo/pull/21#issuecomment-55"}}'
    else
      printf '{"count":0,"match":null}'
    fi
    ;;
  repos/external/repo/issues/21/comments)
    [ "$5" = --method ] && [ "$6" = POST ]
    cat > posted
    printf '{"html_url":"https://github.com/external/repo/pull/21#issuecomment-55"}'
    ;;
  *) exit 1 ;;
esac
`

func fakeReviewCLI(t *testing.T, name, script string) (string, string) {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, name)
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return directory, executable
}

func reviewFixtureFile(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCLIOutputBoundsProcessStdout(t *testing.T) {
	const limit = 128
	for _, tc := range []struct {
		name      string
		size      int
		truncated bool
	}{
		{"empty", 0, false},
		{"exact", limit, false},
		{"one byte over", limit + 1, true},
		{"several pipe buffers", 1 << 20, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := strings.Repeat("x", tc.size)
			cmd := exec.CommandContext(t.Context(), "cat")
			cmd.Stdin = strings.NewReader(payload)
			out := cliOutput{limit: limit}
			cmd.Stdout = &out
			if err := cmd.Run(); err != nil {
				t.Fatal(err)
			}
			if out.String() != payload[:min(tc.size, limit)] || out.truncated != tc.truncated {
				t.Fatalf("captured %d of %d bytes; truncated = %v", len(out.String()), tc.size, out.truncated)
			}
		})
	}
}

func TestGitHubDiscoveryPinsAccountAndMergesSources(t *testing.T) {
	t.Setenv("GH_TOKEN", "ambient-token")
	t.Setenv("GITHUB_TOKEN", "other-ambient-token")
	directory, executable := fakeReviewCLI(t, "gh", fixtureGH)
	reviewFixtureFile(t, directory, "signed-in")
	gh := GitHubCLI{Directory: directory, Executable: executable}
	if _, err := gh.Token(t.Context(), "someone-else"); !errors.Is(err, ErrGitHubAccountChange) {
		t.Fatalf("changed account result = %v", err)
	}
	targets, err := gh.Discover(t.Context(), []string{"missing/repo", "watched/repo"}, "Reviewer")
	if err == nil || !strings.Contains(err.Error(), "missing/repo") || strings.Contains(err.Error(), "fixture-token") {
		t.Fatalf("partial failure = %v", err)
	}
	if len(targets) != 4 || !targets[3].Draft || targets[3].Number != 14 ||
		targets[0].Repository != "external/repo" || targets[0].Number != 21 || targets[0].Source != sourceRequested ||
		targets[1].Repository != "watched/repo" || targets[1].Number != 12 || targets[1].Source != sourceRequested ||
		targets[2].Repository != "watched/repo" || targets[2].Number != 13 || targets[2].Source != sourceRepository {
		t.Fatalf("unexpected discovered targets: %+v", targets)
	}
}

func TestGitHubPublishingReconcilesAndRefusesChangedHead(t *testing.T) {
	directory, executable := fakeReviewCLI(t, "gh", fixtureGH)
	reviewFixtureFile(t, directory, "signed-in")
	gh := GitHubCLI{Directory: directory, Executable: executable}
	target := Target{Repository: "external/repo", Number: 21, Source: sourceRequested,
		HeadSHA: strings.Repeat("b", 40), BaseSHA: strings.Repeat("a", 40)}
	marker := "<!-- axv-github-review:fixture -->"
	reviewFixtureFile(t, directory, "new-head")
	_, err := gh.Publish(t.Context(), target, "reviewer", marker, "Finding")
	if !errors.Is(err, ErrTargetChanged) {
		t.Fatalf("stale publication result = %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "new-head")); err != nil {
		t.Fatal(err)
	}
	first, err := gh.Publish(t.Context(), target, "reviewer", marker, "Finding")
	if err != nil || first != "https://github.com/external/repo/pull/21#issuecomment-55" {
		t.Fatalf("publication result = %q, %v", first, err)
	}
	second, err := gh.Publish(t.Context(), target, "reviewer", marker, "Should never replace the existing comment")
	if err != nil || second != first {
		t.Fatalf("reconciled publication result = %q, %v", second, err)
	}
	posted, err := os.ReadFile(filepath.Join(directory, "posted"))
	if err != nil || !strings.Contains(string(posted), "Finding") || strings.Contains(string(posted), "Should never") {
		t.Fatalf("published body = %s, %v", posted, err)
	}
}

func TestGitHubDeviceLoginOutlivesInitiatingRequest(t *testing.T) {
	directory, executable := fakeReviewCLI(t, "gh", fixtureGH)
	a := &Auth{Context: t.Context(), GitHub: GitHubCLI{Directory: directory, Executable: executable}}
	t.Cleanup(func() { a.Stop("github") })
	ctx, cancel := context.WithCancel(t.Context())
	err := a.Connect(ctx, "github")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	c := waitReviewConnection(t, a, func(c Connection) bool {
		return c.Attempt != nil && c.Attempt.Code != ""
	})
	if c.Connected || c.Attempt.URL != gitHubDeviceURL || c.Attempt.Code != "AB12-CD34" {
		t.Fatalf("unexpected device login prompt: %+v", c)
	}
	reviewFixtureFile(t, directory, "signed-in")
	c = waitReviewConnection(t, a, func(c Connection) bool { return c.Connected })
	if c.Account != "reviewer" || c.Attempt == nil || c.Attempt.Status != "complete" || c.Attempt.Code != "" {
		t.Fatalf("unexpected completed login: %+v", c)
	}
}

func waitReviewConnection(t *testing.T, a *Auth, ready func(Connection) bool) Connection {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		result := a.Inspect(t.Context(), "github")
		if ready(result) {
			return result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("GitHub device login did not reach the expected state: %+v", a.Inspect(t.Context(), "github"))
	return Connection{}
}

func TestReviewDevicePromptsAndCodexSubscriptionMode(t *testing.T) {
	const prompt = "Open: https://example.invalid/not-a-device\n  \x1b[94mhttps://auth.openai.com/codex/device\x1b[0m\n" +
		" Enter this one-time code\n  \x1b[94mAB12-CD34\x1b[0m\n"
	for _, expected := range []string{"AB12-CD34", "AB12-CD345"} {
		if url, code := parseDevicePrompt("codex", strings.ReplaceAll(prompt, "AB12-CD34", expected)); url != codexDeviceURL || code != expected {
			t.Fatalf("parsed Codex prompt = %q %q", url, code)
		}
	}
	if url, _ := parseDevicePrompt("codex", strings.ReplaceAll(prompt, "auth.openai.com", "auth.openai.com.invalid")); url != "" {
		t.Fatalf("unexpected device URL = %q", url)
	}
	dir, executable := fakeReviewCLI(t, "codex", `#!/bin/sh
set -eu
[ -z "${OPENAI_API_KEY:-}" ] && [ -z "${CODEX_API_KEY:-}" ]
[ "$1" = "-c" ] && [ "$2" = 'cli_auth_credentials_store="auto"' ] && [ "$3" = login ] && [ "$4" = status ]
if [ -f other-method ]; then printf 'Logged in using an API key\n' >&2; else printf 'Logged in using ChatGPT\n' >&2; fi
`)
	t.Setenv("OPENAI_API_KEY", "ambient-key")
	t.Setenv("CODEX_API_KEY", "ambient-codex-key")
	a := &Auth{Context: t.Context(), WorkDir: dir, CodexExecutable: executable}
	if err := a.Connect(t.Context(), "codex"); err != nil {
		t.Fatal(err)
	}
	if status := a.Inspect(t.Context(), "codex"); !status.Connected || status.Account != "ChatGPT" {
		t.Fatalf("Codex login status = %+v", status)
	}
	a.Stop("codex")
	reviewFixtureFile(t, dir, "other-method")
	if err := a.Connect(t.Context(), "codex"); err == nil || !strings.Contains(err.Error(), "different sign-in method") {
		t.Fatalf("non-ChatGPT login result = %v", err)
	}
}

func TestCommitChecksRequireExactHeadAndCompleteResponse(t *testing.T) {
	target := workerTestTarget("watched/repo", 12, sourceRepository)
	script := strings.Replace(fixtureGH, "  search/issues)", `  repos/watched/repo/commits/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/check-runs\?filter=latest\&per_page=100\&page=1)
    cat checks.json
    ;;
  search/issues)`, 1)
	dir, path := fakeReviewCLI(t, "gh", script)
	reviewFixtureFile(t, dir, "signed-in")
	gh := GitHubCLI{Directory: dir, Executable: path}
	raw := `{"total":1,"runs":[{"id":123,"sha":"` + target.HeadSHA + `","name":"unit","url":"https://github.com/watched/repo/runs/123","status":"completed","conclusion":"success"}]}`
	for i, data := range []string{raw, strings.Replace(raw, target.HeadSHA, target.BaseSHA, 1), strings.Replace(raw, `"total":1`, `"total":2`, 1)} {
		if err := os.WriteFile(filepath.Join(dir, "checks.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := gh.Checks(t.Context(), target, "reviewer", Checks{})
		if i == 0 {
			if err != nil || got.Status != "passed" || len(got.Runs) != 1 || got.Runs[0].URL == "" {
				t.Fatal(got, err)
			}
		} else if err == nil {
			t.Fatal("accepted incomplete or wrong-SHA checks", got)
		}
	}
}

// Exercise the same JSON projection used by gh api, including an absent body.
func TestReviewPreflightCapturesBoundedAuthorMetadata(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is unavailable")
	}
	script := strings.Replace(fixtureGH, "  search/issues)", `  repos/intent/repo/pulls/9)
    [ "$5" = --jq ]
    jq -c "$6" pr.json
    ;;
  search/issues)`, 1)
	dir, path := fakeReviewCLI(t, "gh", script)
	reviewFixtureFile(t, dir, "signed-in")
	g := GitHubCLI{Directory: dir, Executable: path}
	target := workerTestTarget("intent/repo", 9, sourceRepository)
	for _, tc := range []struct {
		desc      any
		expected  string
		truncated bool
	}{
		{nil, "", false},
		{"Keep working profiles compatible.", "Keep working profiles compatible.", false},
		{strings.Repeat("a", maxAuthorDescriptionBytes), strings.Repeat("a", maxAuthorDescriptionBytes), false},
		{strings.Repeat("a", maxAuthorDescriptionBytes-1) + "界", strings.Repeat("a", maxAuthorDescriptionBytes-1), true},
		{strings.Repeat("界", maxAuthorDescriptionBytes), strings.Repeat("界", maxAuthorDescriptionBytes/3), true},
	} {
		pr := map[string]any{
			"number": 9, "title": strings.Repeat("t", maxAuthorTitleBytes-1) + "界", "body": tc.desc, "state": "open", "draft": false,
			"base": map[string]any{"repo": map[string]string{"full_name": target.Repository}, "sha": target.BaseSHA},
			"head": map[string]string{"sha": target.HeadSHA},
		}
		data, err := json.Marshal(pr)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "pr.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		found, err := g.Current(t.Context(), target, "reviewer")
		if err != nil {
			t.Fatal(err)
		}
		author := found.authorContext
		if found.HeadSHA != target.HeadSHA || found.BaseSHA != target.BaseSHA || author == nil || author.Title != strings.Repeat("t", maxAuthorTitleBytes-1) || !author.TitleTruncated || author.Description != tc.expected || author.DescriptionTruncated != tc.truncated || !utf8.ValidString(author.Description) {
			t.Fatalf("incorrect title/body budget or SHA (truncated=%v)", tc.truncated)
		}
	}
}
