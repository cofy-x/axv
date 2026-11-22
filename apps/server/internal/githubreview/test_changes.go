package githubreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ChangedTestPaths uses immutable commits for the same three-dot comparison as
// the review. Deletions and the old names of moved files can select a test too.
func (g GitHubCLI) ChangedTestPaths(ctx context.Context, account string, target Target) ([]string, error) {
	if !target.valid() {
		return nil, ErrInvalid
	}
	token, err := g.Token(ctx, account)
	if err != nil {
		return nil, err
	}
	comparison := target.BaseSHA + "..." + target.HeadSHA
	endpoint := fmt.Sprintf("repos/%s/compare/%s?per_page=1", target.Repository, comparison)
	var result struct {
		URL, Base, Merge string
		Count            *int
		Files            []struct{ Name, Status, Previous string }
	}
	// GitHub returns at most 300 files on the first compare page, even when
	// there are more pages of commits. An exact 300 also needs verification.
	const filter = `{url:.html_url,base:.base_commit.sha,merge:.merge_base_commit.sha,count:(.files|length),files:(if (.files|type) == "array" and (.files|length) < 300 then [.files[] | {name:.filename,status:.status,previous:.previous_filename}] elif (.files|type) == "array" then [] else null end)}`
	if err = g.api(ctx, token, &result, nil, endpoint, "--jq", filter); err != nil {
		return nil, err
	}
	if !strings.EqualFold(result.URL, "https://github.com/"+target.Repository+"/compare/"+comparison) ||
		result.Base != target.BaseSHA || !commitSHA.MatchString(result.Merge) || result.Count == nil || result.Files == nil || *result.Count < 0 {
		return nil, errors.New("Could not verify the comparison for test selection.")
	}
	if *result.Count >= 300 {
		return g.changedTestPathsFromGit(ctx, target, token, result.Merge)
	}
	if *result.Count != len(result.Files) {
		return nil, errors.New("The changed-file list for test selection is incomplete.")
	}
	paths := make([]string, 0, len(result.Files))
	for _, file := range result.Files {
		if file.Name == "" || (file.Status == "renamed" && file.Previous == "") {
			return nil, errors.New("The changed-file list for test selection is incomplete.")
		}
		paths = append(paths, file.Name)
		if file.Previous != "" {
			paths = append(paths, file.Previous)
		}
	}
	return paths, nil
}

func (g GitHubCLI) changedTestPathsFromGit(ctx context.Context, target Target, token, merge string) ([]string, error) {
	work, err := makeReviewWorkspace(g.Directory)
	if err != nil {
		return nil, errors.New("Could not create a private workspace to check the changed files.")
	}
	defer os.RemoveAll(work)
	git := reviewGit{dir: work}
	base, err := git.fetch(ctx, target, token)
	if err != nil {
		return nil, err
	}
	if base != merge {
		return nil, errors.New("Git and GitHub returned different merge bases for test selection.")
	}
	// --no-renames returns both paths of a rename as a deletion and addition.
	data, err := git.output(ctx, 8<<20, "diff", "--name-only", "--no-renames", "--no-ext-diff", "--no-textconv", "-z", base, target.HeadSHA, "--")
	if err != nil {
		return nil, reviewProcessError(ctx, "Could not read the complete changed-file list from the pinned Git commits.")
	}
	if len(data) == 0 {
		return nil, nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), nil
}
