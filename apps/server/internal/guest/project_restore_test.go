package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreGitChecksOriginalLinkedWorktreePaths(t *testing.T) {
	s := testProjectFileService(t)
	s.capabilities = append(s.capabilities, "project-restore")
	root := s.config.ProjectRoot
	for _, dir := range []string{"repo/.git/worktrees/feature", "feature"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"repo/.git/config":                      "[core]\n\trepositoryformatversion = 0\n\tbare = false\n",
		"repo/.git/worktrees/feature/commondir": "../..\n",
		"repo/.git/worktrees/feature/gitdir":    filepath.Join(root, "feature/.git") + "\n",
		"feature/.git":                          "gitdir: " + filepath.Join(root, "repo/.git/worktrees/feature") + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, project := range []string{"repo", "feature"} {
		scope := projectFileScope(t, s, project)
		response := projectFileRequest(t, s, "restore-check", scope)
		if response.Code != 200 {
			t.Fatalf("valid restored %s rejected: %d %s", project, response.Code, response.Body.String())
		}
	}
	if err := os.WriteFile(filepath.Join(root, "repo/.git/worktrees/feature/gitdir"), []byte("/outside/.git\n"), 0600); err != nil {
		t.Fatal(err)
	}
	response := projectFileRequest(t, s, "restore-check", projectFileScope(t, s, "repo"))
	if response.Code != 409 || !strings.Contains(response.Body.String(), "original locations") {
		t.Fatalf("outside worktree adopted: %d %s", response.Code, response.Body.String())
	}
}

func TestRestoreGitRejectsSymlinkAndExternalConfig(t *testing.T) {
	for _, mode := range []string{"symlink", "include", "worktree", "worktree-comment", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			s := testProjectFileService(t)
			s.capabilities = append(s.capabilities, "project-restore")
			root := s.config.ProjectRoot
			if err := os.MkdirAll(filepath.Join(root, "repo/.git"), 0700); err != nil {
				t.Fatal(err)
			}
			scope := projectFileScope(t, s, "repo")
			switch mode {
			case "symlink":
				if err := os.Remove(filepath.Join(root, "repo/.git")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../other", filepath.Join(root, "repo/.git")); err != nil {
					t.Fatal(err)
				}
			case "include":
				if err := os.WriteFile(filepath.Join(root, "repo/.git/config"), []byte("[include]\n path = /outside/config\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "worktree":
				if err := os.WriteFile(filepath.Join(root, "repo/.git/config"), []byte("[core]\n worktree = /outside/repo\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "worktree-comment":
				if err := os.WriteFile(filepath.Join(root, "repo/.git/config"), []byte("[CoRe] # retained comment\n worktree = /outside/repo\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				if err := os.Rename(filepath.Join(root, "repo"), filepath.Join(root, "old")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "repo"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			response := projectFileRequest(t, s, "restore-check", scope)
			if response.Code == 200 {
				t.Fatalf("unsafe restore %s accepted", mode)
			}
		})
	}
}

func TestRestoreRequiresIndependentCapability(t *testing.T) {
	s := testProjectFileService(t)
	response := projectFileRequest(t, s, "restore-check", projectFileScope(t, s, ""))
	if response.Code != 501 || !strings.Contains(response.Body.String(), "project-restore") {
		t.Fatalf("project-files implicitly enabled restore: %d %s", response.Code, response.Body.String())
	}
}

func TestRestoreValidatesEveryLinkedWorktreeConfiguration(t *testing.T) {
	for _, mode := range []string{"commondir-outside", "commondir-other-repository", "worktree-outside", "worktree-other-directory", "include", "symlink-config"} {
		t.Run(mode, func(t *testing.T) {
			s := testProjectFileService(t)
			s.capabilities = append(s.capabilities, "project-restore")
			root := s.config.ProjectRoot
			for _, dir := range []string{"repo/.git/worktrees/feature", "feature", "other"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				"repo/.git/config":                      "[core]\n repositoryformatversion = 0\n[extensions]\n worktreeConfig = true\n",
				"repo/.git/worktrees/feature/commondir": "../..\n",
				"repo/.git/worktrees/feature/gitdir":    filepath.Join(root, "feature/.git") + "\n",
				"feature/.git":                          "gitdir: " + filepath.Join(root, "repo/.git/worktrees/feature") + "\n",
			}
			switch mode {
			case "commondir-outside":
				files["repo/.git/worktrees/feature/commondir"] = "/outside/repository\n"
			case "commondir-other-repository":
				files["repo/.git/worktrees/feature/commondir"] = filepath.Join(root, "other") + "\n"
			case "worktree-outside":
				files["repo/.git/worktrees/feature/config.worktree"] = "[core]\n worktree = /outside/repository\n"
			case "worktree-other-directory":
				files["repo/.git/worktrees/feature/config.worktree"] = "[core]\n worktree = " + filepath.Join(root, "other") + "\n"
			case "include":
				files["repo/.git/worktrees/feature/config.worktree"] = "[include]\n path = /outside/config\n"
			case "symlink-config":
				files["other/config"] = "[core]\n bare = false\n"
				if err := os.Symlink(filepath.Join(root, "other/config"), filepath.Join(root, "repo/.git/worktrees/feature/config.worktree")); err != nil {
					t.Fatal(err)
				}
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			response := projectFileRequest(t, s, "restore-check", projectFileScope(t, s, "repo"))
			if response.Code == 200 {
				t.Fatalf("linked worktree %s bypassed restored project validation", mode)
			}
		})
	}
}

func TestRestoreAcceptsSeparateGitDirectoryInsideVolume(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is unavailable for real metadata validation")
	}
	s := testProjectFileService(t)
	s.capabilities = append(s.capabilities, "project-restore")
	root := s.config.ProjectRoot
	command := exec.CommandContext(t.Context(), git, "-c", "init.defaultBranch=main", "init", "--separate-git-dir", filepath.Join(root, "metadata"), filepath.Join(root, "worktree"))
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create repository: %v %s", err, output)
	}
	response := projectFileRequest(t, s, "restore-check", projectFileScope(t, s, "worktree"))
	if response.Code != 200 {
		t.Fatalf("valid separate Git directory rejected: %d %s", response.Code, response.Body.String())
	}
}
