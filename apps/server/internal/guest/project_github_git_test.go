package guest

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func githubGitWrite(t *testing.T, s *service, name, data string) {
	t.Helper()
	filename := filepath.Join(s.config.ProjectRoot, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func githubGitFixture(t *testing.T) (*service, protocol.ProjectFileScope) {
	t.Helper()
	s := testProjectFileService(t)
	s.capabilities = append(s.capabilities, "project-github")
	githubGitWrite(t, s, "repo/.git/config", "[core]\n repositoryformatversion = 0\n")
	githubGitWrite(t, s, "repo/.git/HEAD", "ref: refs/heads/feature/main\n")
	githubGitWrite(t, s, "repo/.git/refs/heads/feature/main", strings.Repeat("a", 40)+"\n")
	return s, projectFileScope(t, s, "repo")
}

func githubGitState(t *testing.T, s *service, scope protocol.ProjectFileScope) protocol.GitHubGitState {
	t.Helper()
	state, err := s.projectGitHubGit(scope)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertGitHubMetadataError(t *testing.T, s *service, scope protocol.ProjectFileScope, code string) {
	t.Helper()
	_, err := s.projectGitHubGit(scope)
	var problem *projectFileError
	if !errors.As(err, &problem) || problem.code != code {
		t.Fatalf("metadata error = %v; want %s", err, code)
	}
}

func TestProjectGitHubGitReadsMetadataAndOnlyCleanRemoteCandidates(t *testing.T) {
	s, scope := githubGitFixture(t)
	marker := filepath.Join(s.config.ProjectRoot, "should-not-run")
	githubGitWrite(t, s, "included-config", "[remote \"included\"]\n url = https://github.com/private/included\n")
	githubGitWrite(t, s, "repo/.git/config", "[core]\n repositoryformatversion = 0\n fsmonitor = touch "+marker+"\n hooksPath = /untrusted\n"+
		"[include]\n path = "+filepath.Join(s.config.ProjectRoot, "included-config")+"\n"+
		"[remote \"origin\"]\n url = https://github.com/cofy-x/axv.git\n"+
		"[remote \"ssh\"]\n url = git@github.com:cofy-x/axv.git\n"+
		"[remote \"secret\"]\n url = https://credential-secret@github.com/private/repo.git\n"+
		"[remote \"local\"]\n url = /private/host-repository\n"+
		"[remote \"other\"]\n url = https://github.com.attacker.example/private/repo\n")
	state := githubGitState(t, s, scope)
	want := protocol.GitHubGitState{Repository: true, Branch: "feature/main", HeadSHA: strings.Repeat("a", 40), RemotesPartial: true,
		Remotes: []protocol.GitHubRemote{{Name: "origin", Repository: "cofy-x/axv"}, {Name: "ssh", Repository: "cofy-x/axv"}}}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("metadata = %+v; want %+v", state, want)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("reading metadata executed a repository command")
	}
	data, err := json.Marshal(state)
	if err != nil || strings.Contains(string(data), "credential-secret") || strings.Contains(string(data), "/private/") || strings.Contains(string(data), "included") {
		t.Fatalf("remote candidates leaked excluded configuration: %s, %v", data, err)
	}
}

func TestProjectGitHubGitHeadStates(t *testing.T) {
	for _, mode := range []string{"loose", "packed", "loose-before-packed", "detached", "unborn", "symbolic", "sha256", "not-repository"} {
		t.Run(mode, func(t *testing.T) {
			s, scope := githubGitFixture(t)
			sha := strings.Repeat("a", 40)
			switch mode {
			case "packed", "loose-before-packed":
				githubGitWrite(t, s, "repo/.git/packed-refs", "# pack-refs with: peeled fully-peeled sorted\n"+strings.Repeat("b", 40)+" refs/heads/feature/main\n"+strings.Repeat("c", 40)+" refs/tags/release\n^"+strings.Repeat("d", 40)+"\n")
				if mode == "packed" {
					if err := os.Remove(filepath.Join(s.config.ProjectRoot, "repo/.git/refs/heads/feature/main")); err != nil {
						t.Fatal(err)
					}
					sha = strings.Repeat("b", 40)
				}
			case "detached":
				githubGitWrite(t, s, "repo/.git/HEAD", sha+"\n")
			case "unborn":
				githubGitWrite(t, s, "repo/.git/HEAD", "ref: refs/heads/new\n")
				sha = ""
			case "symbolic":
				githubGitWrite(t, s, "repo/.git/refs/heads/feature/main", "ref: refs/heads/current\n")
				githubGitWrite(t, s, "repo/.git/refs/heads/current", sha+"\n")
			case "sha256":
				sha = strings.Repeat("e", 64)
				githubGitWrite(t, s, "repo/.git/config", "[core]\n repositoryformatversion = 1\n[extensions]\n objectFormat = sha256\n")
				githubGitWrite(t, s, "repo/.git/refs/heads/feature/main", sha+"\n")
			case "not-repository":
				if err := os.RemoveAll(filepath.Join(s.config.ProjectRoot, "repo/.git")); err != nil {
					t.Fatal(err)
				}
			}
			state := githubGitState(t, s, scope)
			if mode == "not-repository" {
				if state.Repository || state.HeadSHA != "" || state.Branch != "" || state.Detached || state.Unborn || len(state.Remotes) != 0 {
					t.Fatalf("non-repository acquired Git state: %+v", state)
				}
				return
			}
			branch := "feature/main"
			if mode == "detached" {
				branch = ""
			} else if mode == "unborn" {
				branch = "new"
			}
			if !state.Repository || state.HeadSHA != sha || state.Branch != branch || state.Detached != (mode == "detached") || state.Unborn != (mode == "unborn") {
				t.Fatalf("%s metadata = %+v", mode, state)
			}
		})
	}
}

func TestProjectGitHubGitReadsLinkedWorktreePrivateHeadAndSharedRefs(t *testing.T) {
	s, _ := githubGitFixture(t)
	githubGitWrite(t, s, "repo/.git/config", "[core]\n repositoryformatversion = 1\n[extensions]\n worktreeConfig = true\n[remote \"origin\"]\n url = https://github.com/cofy-x/axv.git\n")
	githubGitWrite(t, s, "feature/.git", "gitdir: ../repo/.git/worktrees/feature\n")
	githubGitWrite(t, s, "repo/.git/worktrees/feature/commondir", "../..\n")
	githubGitWrite(t, s, "repo/.git/worktrees/feature/gitdir", filepath.Join(s.config.ProjectRoot, "feature/.git")+"\n")
	githubGitWrite(t, s, "repo/.git/worktrees/feature/HEAD", "ref: refs/heads/linked\n")
	githubGitWrite(t, s, "repo/.git/refs/heads/linked", strings.Repeat("b", 40)+"\n")
	githubGitWrite(t, s, "repo/.git/worktrees/feature/config.worktree", "[remote \"fork\"]\n url = ssh://git@github.com/contributor/axv.git\n")
	scope := projectFileScope(t, s, "feature")
	state := githubGitState(t, s, scope)
	if state.Branch != "linked" || state.HeadSHA != strings.Repeat("b", 40) || len(state.Remotes) != 2 || state.Remotes[0].Repository != "contributor/axv" {
		t.Fatalf("linked worktree state = %+v", state)
	}
	githubGitWrite(t, s, "repo/.git/worktrees/feature/gitdir", filepath.Join(s.config.ProjectRoot, "another/.git")+"\n")
	assertGitHubMetadataError(t, s, scope, "github_git_invalid")
}

func TestProjectGitHubGitReadsLinkedWorktreePrivateSymbolicRefs(t *testing.T) {
	for _, namespace := range []string{"bisect", "worktree", "rewritten"} {
		t.Run(namespace, func(t *testing.T) {
			s, _ := githubGitFixture(t)
			gitDir := "repo/.git/worktrees/feature"
			privateRef := "refs/" + namespace + "/private"
			sha := strings.Repeat("b", 40)
			githubGitWrite(t, s, "feature/.git", "gitdir: ../"+gitDir+"\n")
			githubGitWrite(t, s, gitDir+"/commondir", "../..\n")
			githubGitWrite(t, s, gitDir+"/gitdir", filepath.Join(s.config.ProjectRoot, "feature/.git")+"\n")
			githubGitWrite(t, s, gitDir+"/HEAD", "ref: refs/heads/linked\n")
			githubGitWrite(t, s, "repo/.git/refs/heads/linked", "ref: "+privateRef+"\n")
			githubGitWrite(t, s, gitDir+"/"+privateRef, sha+"\n")
			githubGitWrite(t, s, "repo/.git/"+privateRef, strings.Repeat("c", 40)+"\n")
			packed := strings.Repeat("d", 40) + " " + privateRef + "\n"
			githubGitWrite(t, s, "repo/.git/packed-refs", packed)
			githubGitWrite(t, s, gitDir+"/packed-refs", packed)
			scope := projectFileScope(t, s, "feature")
			state := githubGitState(t, s, scope)
			if state.Branch != "linked" || state.HeadSHA != sha || state.Unborn {
				t.Fatalf("private symbolic ref acquired another worktree's commit: %+v", state)
			}
			if err := os.Remove(filepath.Join(s.config.ProjectRoot, gitDir, privateRef)); err != nil {
				t.Fatal(err)
			}
			state = githubGitState(t, s, scope)
			if state.Branch != "linked" || state.HeadSHA != "" || !state.Unborn {
				t.Fatalf("missing private symbolic ref acquired shared or packed metadata: %+v", state)
			}
		})
	}
}

func TestProjectGitHubGitRejectsUnsafeOrUnsupportedMetadata(t *testing.T) {
	for _, mode := range []string{"git-symlink", "head-symlink", "ref-directory-symlink", "config-symlink", "outside-gitdir", "outside-commondir", "ref-traversal", "symbolic-cycle", "invalid-sha", "missing-head", "reftable-config", "reftable-directory", "unknown-format", "large-config", "large-packed-refs"} {
		t.Run(mode, func(t *testing.T) {
			s, scope := githubGitFixture(t)
			root := s.config.ProjectRoot
			code := "github_git_invalid"
			switch mode {
			case "git-symlink", "head-symlink", "ref-directory-symlink", "config-symlink":
				target := map[string]string{"git-symlink": "repo/.git", "head-symlink": "repo/.git/HEAD", "ref-directory-symlink": "repo/.git/refs", "config-symlink": "repo/.git/config"}[mode]
				if err := os.Rename(filepath.Join(root, target), filepath.Join(root, "moved")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "moved"), filepath.Join(root, target)); err != nil {
					t.Fatal(err)
				}
			case "outside-gitdir":
				if err := os.RemoveAll(filepath.Join(root, "repo/.git")); err != nil {
					t.Fatal(err)
				}
				githubGitWrite(t, s, "repo/.git", "gitdir: /outside/private-git\n")
			case "outside-commondir":
				githubGitWrite(t, s, "repo/.git/commondir", "../../../outside\n")
			case "ref-traversal":
				githubGitWrite(t, s, "repo/.git/HEAD", "ref: refs/heads/../../private\n")
			case "symbolic-cycle":
				githubGitWrite(t, s, "repo/.git/refs/heads/feature/main", "ref: refs/heads/feature/main\n")
			case "invalid-sha":
				githubGitWrite(t, s, "repo/.git/refs/heads/feature/main", "arbitrary-private-file-text\n")
			case "missing-head":
				if err := os.Remove(filepath.Join(root, "repo/.git/HEAD")); err != nil {
					t.Fatal(err)
				}
			case "reftable-config":
				githubGitWrite(t, s, "repo/.git/config", "[core]\n repositoryFormatVersion = 1\n[extensions]\n refStorage = reftable\n")
				code = "github_git_unsupported"
			case "reftable-directory":
				if err := os.Mkdir(filepath.Join(root, "repo/.git/reftable"), 0700); err != nil {
					t.Fatal(err)
				}
				code = "github_git_unsupported"
			case "unknown-format":
				githubGitWrite(t, s, "repo/.git/config", "[core]\n repositoryFormatVersion = 42\n")
				code = "github_git_unsupported"
			case "large-config":
				githubGitWrite(t, s, "repo/.git/config", strings.Repeat("#", githubGitConfigLimit+1))
				code = "github_git_unsupported"
			case "large-packed-refs":
				if err := os.Remove(filepath.Join(root, "repo/.git/refs/heads/feature/main")); err != nil {
					t.Fatal(err)
				}
				githubGitWrite(t, s, "repo/.git/packed-refs", strings.Repeat("#", githubGitRefsLimit+1))
				code = "github_git_unsupported"
			}
			assertGitHubMetadataError(t, s, scope, code)
		})
	}
}

func TestProjectGitHubGitSnapshotDetectsReplacementEvenWithSameHead(t *testing.T) {
	s, scope := githubGitFixture(t)
	before, err := s.projectGitHubGitSnapshot(scope)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.projectGitHubGitSnapshot(scope)
	if err != nil || !sameGitHubGitSnapshot(before, unchanged) {
		t.Fatalf("unchanged metadata failed comparison: %v", err)
	}
	filename := filepath.Join(s.config.ProjectRoot, "repo/.git/HEAD")
	if err := os.Rename(filename, filename+".old"); err != nil {
		t.Fatal(err)
	}
	githubGitWrite(t, s, "repo/.git/HEAD", "ref: refs/heads/feature/main\n")
	after, err := s.projectGitHubGitSnapshot(scope)
	if err != nil || !reflect.DeepEqual(before.state, after.state) || sameGitHubGitSnapshot(before, after) {
		t.Fatalf("replaced HEAD metadata was accepted: %v", err)
	}
}

func TestGitHubRemoteRepositoryAcceptsOnlyCleanGitHubSelectors(t *testing.T) {
	for _, value := range []string{"https://github.com/cofy-x/axv.git", "https://github.com/cofy-x/axv", "ssh://git@github.com/cofy-x/axv.git", "git@github.com:cofy-x/axv.git"} {
		if got := gitHubRemoteRepository(value); got != "cofy-x/axv" {
			t.Errorf("clean remote %q = %q", value, got)
		}
	}
	for _, value := range []string{"https://secret@github.com/o/r", "https://github.com/o/r?secret", "https://github.com/o/r?", "https://github.com/o/r#secret", "https://github.com/o/r#", "https://github.com/o%2Fr", "https://github.com:443/o/r", "https://github.com.attacker.example/o/r", "ssh://git:secret@github.com/o/r", "ssh://other@github.com/o/r", "git@github.com:o/r/extra", "file:///private/repo", "../private/repo", "https://github.com/o/.."} {
		if got := gitHubRemoteRepository(value); got != "" {
			t.Errorf("unsafe remote %q = %q", value, got)
		}
	}
}

func projectGitHubRequest(t *testing.T, s *service, action string, req protocol.ProjectGitHubRequest) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/project-github/"+action, strings.NewReader(string(data)))
	r.Header.Set("Authorization", "Bearer "+s.config.Token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}

func TestProjectGitHubRouteFencesScopeAndDoesNotRequireTaskOrProviderState(t *testing.T) {
	s, scope := githubGitFixture(t)
	req := protocol.ProjectGitHubRequest{ProjectFileScope: scope}
	for range 3 {
		response := projectGitHubRequest(t, s, "git", req)
		var observation protocol.ProjectGitHubObservation
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &observation) != nil || observation.Git.HeadSHA != strings.Repeat("a", 40) {
			t.Fatalf("Git route response = %d %s", response.Code, response.Body)
		}
		if _, err := time.Parse(time.RFC3339Nano, observation.ObservedAt); err != nil {
			t.Fatalf("invalid observation timestamp: %q", observation.ObservedAt)
		}
		if s.tasks != nil || observation.Account.ID != "" || observation.Repository.ID != "" {
			t.Fatal("metadata observation acquired task or provider state")
		}
	}
	for _, test := range []struct{ field, code string }{{"boot", "boot_changed"}, {"instance", "instance_changed"}, {"root", "project_changed"}} {
		changed := req
		switch test.field {
		case "boot":
			changed.BootID = "other-boot"
		case "instance":
			changed.InstanceID = "other-instance"
		case "root":
			changed.RootIdentity = "other-root"
		}
		assertProblem(t, projectGitHubRequest(t, s, "git", changed), 409, test.code)
	}
	for _, test := range []struct {
		action string
		req    protocol.ProjectGitHubRequest
	}{{"git", protocol.ProjectGitHubRequest{ProjectFileScope: scope, Repository: "o/r"}}, {"review", req}, {"snapshot", protocol.ProjectGitHubRequest{ProjectFileScope: scope, RepositoryID: "repo-id"}}, {"run", req}} {
		assertProblem(t, projectGitHubRequest(t, s, test.action, test.req), 400, "invalid_request")
	}
	s.capabilities = []string{"project-files"}
	assertProblem(t, projectGitHubRequest(t, s, "git", req), 501, "unsupported")
}
