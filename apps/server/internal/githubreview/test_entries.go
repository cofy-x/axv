package githubreview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

var errTestTree = errors.New("GitHub did not return the complete requested Git tree.")

// Read only tree metadata, bound to the exact commit rather than a mutable ref.
// Missing paths on a verified tree and errors reading a tree must stay distinct.
func (g GitHubCLI) testEntryCheck(ctx context.Context, account string, target Target) (testEntryCheck, error) {
	if !target.valid() {
		return nil, ErrInvalid
	}
	token, err := g.Token(ctx, account)
	if err != nil {
		return nil, err
	}
	prefix := "repos/" + target.Repository + "/git/"
	var commit struct{ SHA, Tree string }
	if err := g.api(ctx, token, &commit, nil, prefix+"commits/"+target.HeadSHA, "--jq", `{sha:.sha,tree:.tree.sha}`); err != nil {
		return nil, err
	}
	if commit.SHA != target.HeadSHA || !commitSHA.MatchString(commit.Tree) {
		return nil, errTestTree
	}
	type entry struct{ Path, Mode, Type, SHA string }
	cache := map[string]entry{}
	return func(file string, legacy bool) (bool, error) {
		if !validTestPath(file) {
			return false, ErrInvalid
		}
		tree := commit.Tree
		parts := strings.Split(file, "/")
		for i, part := range parts {
			cacheKey := tree + "/" + part
			node, cached := cache[cacheKey]
			if !cached {
				// gh applies this literal path filter locally, keeping the subprocess's
				// output bounded even in a very wide directory.
				name, _ := json.Marshal(part)
				filter := `{sha,truncated,tree:(.tree|map(select(.path==` + string(name) + `)))}`
				var result struct {
					SHA       string
					Truncated *bool
					Tree      []entry
				}
				if err := g.api(ctx, token, &result, nil, prefix+"trees/"+tree, "--jq", filter); err != nil {
					return false, err
				}
				if result.SHA != tree || result.Truncated == nil || *result.Truncated || result.Tree == nil || len(result.Tree) > 1 {
					return false, errTestTree
				}
				if len(result.Tree) == 1 {
					node = result.Tree[0]
					if node.Path != part || !commitSHA.MatchString(node.SHA) {
						return false, errTestTree
					}
				}
				cache[cacheKey] = node
			}
			if node.Path == "" {
				return false, nil
			}
			switch node.Type {
			case "tree":
				if node.Mode != "040000" {
					return false, errTestTree
				}
				tree = node.SHA
			case "blob":
				if node.Mode == "120000" {
					// Legacy script invocations can follow symlinks inside their
					// isolated checkout. Named guards require a regular file.
					return legacy, nil
				}
				if node.Mode != "100644" && node.Mode != "100755" {
					return false, errTestTree
				}
				return i == len(parts)-1, nil
			case "commit":
				if node.Mode != "160000" {
					return false, errTestTree
				}
				return false, nil // VM checkouts do not populate submodules.
			default:
				return false, errTestTree
			}
		}
		return legacy, nil // a legacy interpreter can run a directory (e.g. Python __main__.py)
	}, nil
}
