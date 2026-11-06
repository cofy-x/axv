package guest

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func (s *service) projectGitHub(w http.ResponseWriter, r *http.Request) {
	var req protocol.ProjectGitHubRequest
	if err := decodeProjectFile(w, r, &req, false); err != nil {
		projectGitHubFailure(w, err)
		return
	}
	action := r.PathValue("action")
	if err := validateProjectGitHubRequest(action, req); err != nil {
		projectGitHubFailure(w, err)
		return
	}
	root, original, _, err := s.openProjectFiles(req.ProjectFileScope, false)
	if err != nil {
		projectGitHubFailure(w, err)
		return
	}
	defer root.Close()
	before, err := s.projectGitHubGitSnapshot(req.ProjectFileScope)
	if err != nil {
		projectGitHubFailure(w, err)
		return
	}
	observation := protocol.ProjectGitHubObservation{Git: before.state, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if action != "git" {
		observation, err = s.readProjectGitHub(r.Context(), req, before.state)
		if err != nil {
			projectGitHubFailure(w, err)
			return
		}
	}
	after, err := s.projectGitHubGitSnapshot(req.ProjectFileScope)
	if err == nil && !sameGitHubGitSnapshot(before, after) {
		err = githubGitChanged()
	}
	if err == nil {
		err = s.verifyProjectFiles(req.ProjectFileScope, original)
	}
	if err != nil {
		projectGitHubFailure(w, err)
		return
	}
	observation.Git = before.state
	respond(w, http.StatusOK, observation)
}

func projectGitHubFailure(w http.ResponseWriter, err error) {
	var problem *projectFileError
	if !errors.As(err, &problem) {
		err = projectProblem(503, "github_git_unavailable", "The project's Git metadata could not be read in its original environment.")
	}
	projectFailure(w, err)
}

func validateProjectGitHubRequest(action string, req protocol.ProjectGitHubRequest) error {
	for _, value := range []string{req.Repository, req.RepositoryID, req.AccountID} {
		if !utf8.ValidString(value) || len(value) > 256 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return projectProblem(400, "invalid_request", "GitHub selectors require bounded valid text without control characters.")
		}
	}
	valid := false
	switch action {
	case "git":
		valid = req.Repository == "" && req.RepositoryID == "" && req.AccountID == ""
	case "review":
		valid = gitHubRemoteRepository("https://github.com/"+req.Repository) == req.Repository && req.Repository != "" && req.RepositoryID == "" && req.AccountID == ""
	case "snapshot":
		valid = req.Repository == "" && req.RepositoryID != "" && req.AccountID != ""
	}
	if !valid {
		return projectProblem(400, "invalid_request", "Supply only the repository selector for review, the reviewed repository/account IDs for snapshot, or no selectors for Git metadata.")
	}
	return nil
}
