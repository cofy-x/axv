package guest

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func (s *service) checkRestoredProject(w http.ResponseWriter, r *http.Request) {
	var scope protocol.ProjectFileScope
	if err := decodeProjectFile(w, r, &scope, false); err != nil {
		projectFailure(w, err)
		return
	}
	root, original, identity, err := s.openProjectFiles(scope, false)
	if err != nil {
		projectFailure(w, err)
		return
	}
	defer root.Close()
	if err = s.checkRestoredGit(scope.Root); err == nil {
		err = s.verifyProjectFiles(scope, original)
	}
	if err != nil {
		projectFailure(w, err)
		return
	}
	respond(w, http.StatusOK, protocol.ProjectFileRoot{Root: scope.Root, RootIdentity: identity})
}

func restorePathError() error {
	return projectProblem(409, "project_changed", "Restored Git/worktree paths must resolve to their original locations inside the copied project volume; repair them explicitly before binding.")
}

// Resolve only filesystem metadata. Never execute Git hooks, repository config,
// commands or a checkout while validating a recovered project.
func (s *service) restoreGitPath(base, value string) (string, error) {
	if strings.ContainsAny(value, "\\\x00\n\r") || value == "" {
		return "", restorePathError()
	}
	var resolved string
	if path.IsAbs(value) {
		prefix := strings.TrimSuffix(s.config.ProjectRoot, "/") + "/"
		if !strings.HasPrefix(value, prefix) {
			return "", restorePathError()
		}
		resolved = strings.TrimPrefix(value, prefix)
	} else {
		resolved = path.Join(base, value)
	}
	resolved = path.Clean(resolved)
	if resolved == "." {
		return "", nil
	}
	if _, err := projectRelative(resolved, false); err != nil {
		return "", restorePathError()
	}
	return resolved, nil
}

func restoreMetadataFile(volume *os.Root, name string) (string, error) {
	// Every component, including the final metadata file, must retain its direct
	// location. os.Root additionally confines any filesystem race to this volume.
	for current := name; current != "." && current != ""; current = path.Dir(current) {
		info, err := volume.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", restorePathError()
		}
	}
	f, info, err := projectRegular(volume, name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info.Size() > 64<<10 {
		return "", restorePathError()
	}
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 64<<10 || !projectText(data) {
		return "", restorePathError()
	}
	return strings.TrimSpace(string(data)), nil
}

func restoreDirectory(volume *os.Root, name string) error {
	if name == "" {
		name = "."
	}
	for current := name; current != "." && current != ""; current = path.Dir(current) {
		info, err := volume.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return restorePathError()
		}
	}
	return nil
}

func (s *service) checkRestoredGit(projectRoot string) error {
	volume, err := os.OpenRoot(s.config.ProjectRoot)
	if err != nil {
		return err
	}
	defer volume.Close()
	gitFile := path.Join(projectRoot, ".git")
	info, err := volume.Lstat(gitFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	gitDir := gitFile
	if !info.IsDir() {
		text, err := restoreMetadataFile(volume, gitFile)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(text, "gitdir: ") {
			return restorePathError()
		}
		gitDir, err = s.restoreGitPath(projectRoot, strings.TrimPrefix(text, "gitdir: "))
		if err != nil {
			return err
		}
	}
	if err := restoreDirectory(volume, gitDir); err != nil {
		return err
	}
	commonDir := gitDir
	linked := false
	if text, err := restoreMetadataFile(volume, path.Join(gitDir, "commondir")); err == nil {
		linked = true
		commonDir, err = s.restoreGitPath(gitDir, text)
		if err != nil {
			return err
		}
		if err := restoreDirectory(volume, commonDir); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if gitDir != gitFile && linked {
		back, err := restoreMetadataFile(volume, path.Join(gitDir, "gitdir"))
		if err != nil {
			return err
		}
		resolved, err := s.restoreGitPath(gitDir, back)
		if err != nil || resolved != gitFile {
			return restorePathError()
		}
	}
	configDirs := []string{gitDir}
	if commonDir != gitDir {
		configDirs = append(configDirs, commonDir)
	}
	for _, dir := range configDirs {
		var expected *string
		if dir == gitDir {
			expected = &projectRoot
		}
		if err := s.checkRestoredGitConfig(volume, dir, expected); err != nil {
			return err
		}
	}
	if err := restoreDirectory(volume, path.Join(commonDir, "worktrees")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	worktrees, err := volume.OpenRoot(path.Join(commonDir, "worktrees"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer worktrees.Close()
	f, err := worktrees.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(257)
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) > 256 {
		return projectProblem(409, "project_changed", "Restore validation supports at most 256 linked worktrees.")
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return restorePathError()
		}
		admin := path.Join(commonDir, "worktrees", entry.Name())
		text, err := restoreMetadataFile(volume, path.Join(admin, "gitdir"))
		if err != nil {
			return err
		}
		target, err := s.restoreGitPath(admin, text)
		if err != nil || path.Base(target) != ".git" {
			return restorePathError()
		}
		back, err := restoreMetadataFile(volume, target)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(back, "gitdir: ") {
			return restorePathError()
		}
		resolved, err := s.restoreGitPath(path.Dir(target), strings.TrimPrefix(back, "gitdir: "))
		if err != nil || resolved != admin {
			return restorePathError()
		}
		common, err := restoreMetadataFile(volume, path.Join(admin, "commondir"))
		if err != nil {
			return err
		}
		resolved, err = s.restoreGitPath(admin, common)
		if err != nil || resolved != commonDir {
			return restorePathError()
		}
		expected := path.Dir(target)
		if expected == "." {
			expected = ""
		}
		if err := s.checkRestoredGitConfig(volume, admin, &expected); err != nil {
			return err
		}
	}
	return nil
}

// Check each linked worktree's private configuration, not only the selected
// worktree and shared repository. Git can enable independent core.worktree and
// includes through extensions.worktreeConfig.
func (s *service) checkRestoredGitConfig(volume *os.Root, dir string, expectedWorktree *string) error {
	for _, name := range []string{"config", "config.worktree"} {
		text, err := restoreMetadataFile(volume, path.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		section := ""
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				end := strings.IndexByte(line, ']')
				if end < 0 {
					return restorePathError()
				}
				tail := strings.TrimSpace(line[end+1:])
				if tail != "" && !strings.HasPrefix(tail, "#") && !strings.HasPrefix(tail, ";") {
					return restorePathError()
				}
				section = "[" + strings.ToLower(strings.TrimSpace(line[1:end])) + "]"
				if strings.HasPrefix(section, "[include") {
					return projectProblem(409, "project_changed", "Restored Git config includes require explicit review before binding; remove external includes from this recovered copy.")
				}
				continue
			}
			key, value, found := strings.Cut(line, "=")
			if section == "[core]" && found && strings.EqualFold(strings.TrimSpace(key), "worktree") {
				value = strings.TrimSpace(value)
				if strings.ContainsAny(value, "\"#;\\") {
					return restorePathError()
				}
				resolved, err := s.restoreGitPath(dir, value)
				if err != nil {
					return err
				}
				if err := restoreDirectory(volume, resolved); err != nil {
					return err
				}
				if expectedWorktree != nil && resolved != *expectedWorktree {
					return restorePathError()
				}
			}
		}
	}
	return nil
}
