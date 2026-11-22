//go:build !windows

package githubreview

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntryPreflightUsesPinnedCompleteGitTrees(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is unavailable")
	}
	target := workerTestTarget("owner/alpha", 7, sourceRepository)
	root, child := strings.Repeat("c", 40), strings.Repeat("d", 40)
	script := strings.Replace(fixtureGH, "  search/issues)", fmt.Sprintf(`  repos/owner/alpha/git/commits/%s)
    [ "$5" = --jq ] && jq -c "$6" commit.json
    ;;
  repos/owner/alpha/git/trees/%s)
    [ "$5" = --jq ] && jq -c "$6" root.json
    ;;
  repos/owner/alpha/git/trees/%s)
    [ "$5" = --jq ] && jq -c "$6" child.json
    ;;
  search/issues)`, target.HeadSHA, root, child), 1)
	dir, exe := fakeReviewCLI(t, "gh", script)
	reviewFixtureFile(t, dir, "signed-in")
	write := func(file, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	commit := fmt.Sprintf(`{"sha":"%s","tree":{"sha":"%s"}}`, target.HeadSHA, root)
	write("commit.json", commit)
	rootTree := fmt.Sprintf(`{"sha":"%s","truncated":false,"tree":[{"path":"tests","type":"tree","mode":"040000","sha":"%s"},{"path":"link","type":"blob","mode":"120000","sha":"%s"}]}`, root, child, child)
	write("root.json", rootTree)
	write("child.json", fmt.Sprintf(`{"sha":"%s","truncated":false,"tree":[{"path":"verify.py","type":"blob","mode":"100644","sha":"%s"}]}`, child, root))
	gh := GitHubCLI{Directory: dir, Executable: exe}
	open := func() testEntryCheck {
		t.Helper()
		check, err := gh.testEntryCheck(t.Context(), "reviewer", target)
		if err != nil {
			t.Fatal(err)
		}
		return check
	}
	check := open()
	for _, tc := range []struct {
		path         string
		legacy, want bool
	}{
		{"tests/verify.py", false, true}, {"tests/none", false, false}, {"tests", false, false}, {"link", false, false}, {"link/verify.py", true, true}, {`tests/x")`, false, false},
	} {
		ok, err := check(tc.path, tc.legacy)
		if ok != tc.want || (err != nil && validTestPath(tc.path)) {
			t.Fatalf("entry %q = %v, %v", tc.path, ok, err)
		}
	}
	for _, bad := range []string{strings.Replace(rootTree, `"truncated":false`, `"truncated":true`, 1), strings.Replace(rootTree, root, child, 1), strings.Replace(rootTree, `"040000"`, `"160000"`, 1)} {
		write("root.json", bad)
		if _, err := open()("tests/verify.py", false); err == nil {
			t.Fatal("inconsistent tree treated as complete")
		}
	}
	write("commit.json", strings.Replace(commit, target.HeadSHA, target.BaseSHA, 1))
	if _, err := gh.testEntryCheck(t.Context(), "reviewer", target); err == nil {
		t.Fatal("accepted tree from different head")
	}
}
