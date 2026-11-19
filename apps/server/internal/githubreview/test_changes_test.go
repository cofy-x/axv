//go:build !windows

package githubreview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestChangedTestPathsRequiresCompletePinnedComparison(t *testing.T) {
	target := workerTestTarget("owner/repo", 23, sourceRepository)
	apiPath := "repos/" + target.Repository + "/compare/" + target.BaseSHA + "..." + target.HeadSHA + "?per_page=1"
	dir, exe := fakeReviewCLI(t, "gh", fmt.Sprintf(`#!/bin/sh
set -eu
case "$1:$2" in
auth:token) echo fixture-token ;;
api:--hostname)
  case "$4" in
  user) echo '{"login":"reviewer"}' ;;
  '%s') cat files.json ;;
  *) exit 1 ;;
  esac ;;
*) exit 1 ;;
esac
`, apiPath))
	g := GitHubCLI{Directory: dir, Executable: exe}
	gitDir, _ := fakeReviewCLI(t, "git", fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
init|fetch) exit 0 ;;
merge-base) printf '%%s\n' '%s' ;;
diff) printf 'added.go\0last/new.go\0' ;;
*) exit 1 ;;
esac
`, target.BaseSHA))
	t.Setenv("PATH", gitDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	expectedURL := "https://github.com/owner/repo/compare/" + target.BaseSHA + "..." + target.HeadSHA
	result := map[string]any{"url": expectedURL, "base": target.BaseSHA, "merge": target.BaseSHA, "count": 2, "files": []map[string]string{{"name": "gone/file", "status": "removed"}, {"name": "new/file", "status": "renamed", "previous": "old/file"}}}
	read := func() ([]string, error) {
		t.Helper()
		b, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "files.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		return g.ChangedTestPaths(t.Context(), "reviewer", target)
	}
	if paths, err := read(); err != nil || !slices.Equal(paths, []string{"gone/file", "new/file", "old/file"}) {
		t.Fatalf("rename/removal selection = %v, %v", paths, err)
	}
	result["url"] = strings.Replace(expectedURL, target.HeadSHA, strings.Repeat("d", 40), 1)
	if _, err := read(); err == nil {
		t.Fatal("accepted response for another head")
	}
	result["url"] = expectedURL
	result["count"] = 3
	if _, err := read(); err == nil {
		t.Fatal("accepted incomplete page as no match")
	}
	result["count"] = 300
	result["files"] = []map[string]string{}
	if paths, err := read(); err != nil || !slices.Contains(paths, "last/new.go") {
		t.Fatalf("did not fall back to pinned Git diff at cap: %v %v", paths, err)
	}
	result["merge"] = strings.Repeat("d", 40)
	if _, err := read(); err == nil {
		t.Fatal("accepted different Git and GitHub merge bases")
	}
}
