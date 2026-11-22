package githubreview

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Read only the latest GitHub check runs on the captured PR head. A partial
// page set, unfamiliar state or mismatched SHA cannot produce a passing result.
func (g GitHubCLI) Checks(ctx context.Context, target Target, account string, previous Checks) (Checks, error) {
	if !target.valid() {
		return Checks{}, ErrInvalid
	}
	token, err := g.Token(ctx, account)
	if err != nil {
		return Checks{}, err
	}
	var runs []CheckRun
	seen := map[int64]bool{}
	actions := map[string]int64{}
	for page := 1; page <= 3; page++ {
		var raw struct {
			Total *int `json:"total"`
			Runs  []struct {
				ID  int64
				SHA string
				App string
				CheckRun
			} `json:"runs"`
		}
		endpoint := fmt.Sprintf("repos/%s/commits/%s/check-runs?filter=latest&per_page=100&page=%d", target.Repository, target.HeadSHA, page)
		if err := g.api(ctx, token, &raw, nil, endpoint, "--jq", `{total:.total_count,runs:[.check_runs[]|{id,sha:.head_sha,name,status,conclusion,url:.html_url,app:.app.slug}]}`); err != nil {
			return Checks{}, err
		}
		if raw.Total == nil || *raw.Total < 0 || raw.Runs == nil {
			return Checks{}, errors.New("GitHub returned an incomplete check run list.")
		}
		for _, item := range raw.Runs {
			if item.SHA != target.HeadSHA || item.ID <= 0 || seen[item.ID] || item.Name == "" || len(item.Name) > 512 {
				return Checks{}, errors.New("GitHub returned inconsistent check runs for this commit.")
			}
			seen[item.ID] = true
			u, err := url.Parse(item.URL)
			if err != nil || u.Scheme != "https" || u.Host != githubHost || u.User != nil || !strings.HasPrefix(strings.ToLower(u.Path), "/"+strings.ToLower(target.Repository)+"/") || len(item.URL) > 2048 {
				item.URL = ""
			}
			if item.App == "github-actions" && item.URL != "" && item.Status == "completed" && item.Conclusion != "skipped" {
				actions[item.URL] = item.ID
			}
			runs = append(runs, item.CheckRun)
		}
		if len(runs) == *raw.Total {
			g.observeActionsJobs(ctx, token, target, runs, actions, previous)
			return Checks{Status: checksStatus(runs), Runs: runs}, nil
		}
		if len(raw.Runs) < githubPageSize {
			break
		}
	}
	return Checks{}, errors.New("GitHub check runs changed during pagination or exceeded the 300-run evidence limit.")
}

func checksStatus(runs []CheckRun) string {
	if len(runs) == 0 {
		return "empty"
	}
	pending, passed := false, false
	for _, run := range runs {
		if run.Status != "completed" {
			pending = true
			continue
		}
		switch run.Conclusion {
		case "success":
			passed = true
		case "neutral", "skipped":
		case "action_required", "cancelled", "failure", "stale", "timed_out", "startup_failure":
			return "failed"
		default:
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	if passed {
		return "passed"
	}
	return "neutral"
}
