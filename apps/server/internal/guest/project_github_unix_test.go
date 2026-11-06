//go:build linux || darwin

package guest

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func githubShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func TestProjectGitHubRouteCombinesGuestMetadataAndBoundedProviderRead(t *testing.T) {
	s, scope := githubGitFixture(t)
	payload, _ := githubProviderResponse()
	githubProviderFixture(t, githubProviderJSON(t, payload), "")
	for _, action := range []string{"review", "snapshot"} {
		req := protocol.ProjectGitHubRequest{ProjectFileScope: scope}
		if action == "review" {
			req.Repository = "octo/work"
		} else {
			req.RepositoryID, req.AccountID = "R_selected", "U_selected"
		}
		response := projectGitHubRequest(t, s, action, req)
		var observation protocol.ProjectGitHubObservation
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &observation) != nil {
			t.Fatalf("%s response = %d %s", action, response.Code, response.Body)
		}
		if observation.Git.Branch != "feature/main" || observation.Git.HeadSHA != githubProviderLocalSHA || observation.Account.ID != "U_selected" || observation.Repository.ID != "R_selected" || s.tasks != nil {
			t.Fatalf("%s observation lost original Git identities or allocated task state: %+v", action, observation)
		}
		if action == "review" {
			if observation.LocalChecks != nil || len(observation.PullRequests) != 0 {
				t.Fatal("review fetched commit or pull-request checks before association")
			}
		} else if observation.LocalChecks == nil || observation.LocalChecks.SHA != observation.Git.HeadSHA {
			t.Fatal("snapshot checks lost their captured commit identity")
		}
	}
}

func TestProjectGitHubRouteRejectsMetadataOrRootReplacementDuringProviderRead(t *testing.T) {
	for _, mode := range []string{"head-changed", "head-replaced", "metadata-removed", "config-changed", "root-replaced"} {
		t.Run(mode, func(t *testing.T) {
			s, scope := githubGitFixture(t)
			root := filepath.Join(s.config.ProjectRoot, "repo")
			head := filepath.Join(root, ".git/HEAD")
			extra := ""
			code := "github_git_changed"
			switch mode {
			case "head-changed":
				extra = "printf '%s\\n' 'ref: refs/heads/other' > " + githubShellLiteral(head) + "\nprintf '%s\\n' '" + githubProviderHeadSHA + "' > " + githubShellLiteral(filepath.Join(root, ".git/refs/heads/other"))
			case "head-replaced":
				extra = "mv " + githubShellLiteral(head) + " " + githubShellLiteral(head+".old") + "\nprintf '%s\\n' 'ref: refs/heads/feature/main' > " + githubShellLiteral(head)
			case "metadata-removed":
				extra = "mv " + githubShellLiteral(filepath.Join(root, ".git")) + " " + githubShellLiteral(filepath.Join(root, "metadata.old"))
			case "config-changed":
				extra = "printf '%s\\n' '# metadata edited during network read' >> " + githubShellLiteral(filepath.Join(root, ".git/config"))
			case "root-replaced":
				extra = "mv " + githubShellLiteral(root) + " " + githubShellLiteral(root+".old") + "\nmkdir " + githubShellLiteral(root)
				code = "project_changed"
			}
			payload, _ := githubProviderResponse()
			githubProviderFixture(t, githubProviderJSON(t, payload), extra)
			response := projectGitHubRequest(t, s, "snapshot", protocol.ProjectGitHubRequest{ProjectFileScope: scope, RepositoryID: "R_selected", AccountID: "U_selected"})
			assertProblem(t, response, 409, code)
			if strings.Contains(response.Body.String(), "headSha") || strings.Contains(response.Body.String(), "localChecks") || s.tasks != nil {
				t.Fatal("a stale provider observation was published or acquired task state")
			}
		})
	}
}
