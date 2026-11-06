package guest

import (
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

const githubGitConfigLimit = 64 << 10
const githubGitRefsLimit = 1 << 20

type githubGitMetadata struct {
	info fs.FileInfo
	data string
}

type githubGitSnapshot struct {
	state protocol.GitHubGitState
	files map[string]githubGitMetadata
}

type githubGitReader struct {
	volume *os.Root
	files  map[string]githubGitMetadata
}

func githubGitInvalid() error {
	return projectProblem(409, "github_git_invalid", "The project's Git metadata is malformed or does not remain inside its project volume.")
}

func githubGitUnsupported() error {
	return projectProblem(409, "github_git_unsupported", "This Git metadata format is not supported by the read-only project panel.")
}

func githubGitChanged() error {
	return projectProblem(409, "github_git_changed", "The project's Git metadata changed during this read. Refresh the original project environment explicitly.")
}

func sameGitHubGitSnapshot(a, b githubGitSnapshot) bool {
	if !reflect.DeepEqual(a.state, b.state) || len(a.files) != len(b.files) {
		return false
	}
	for name, before := range a.files {
		after, ok := b.files[name]
		if !ok || before.data != after.data || (before.info == nil) != (after.info == nil) {
			return false
		}
		if before.info != nil && (!os.SameFile(before.info, after.info) || before.info.Mode() != after.info.Mode()) {
			return false
		}
	}
	return true
}

func (r *githubGitReader) remember(name string, info fs.FileInfo, data string) error {
	value := githubGitMetadata{info: info, data: data}
	if previous, exists := r.files[name]; exists {
		if (previous.info == nil) != (info == nil) || previous.data != data || (info != nil && (!os.SameFile(previous.info, info) || previous.info.Mode() != info.Mode())) {
			return githubGitChanged()
		}
	}
	r.files[name] = value
	return nil
}

// Metadata paths are resolved through the mounted volume, including a linked
// worktree's shared refs. Every path component must retain a direct location.
func (r *githubGitReader) directory(name string) error {
	if name == "" {
		name = "."
	}
	parents := []string{}
	for current := name; ; current = path.Dir(current) {
		parents = append(parents, current)
		if current == "." {
			break
		}
	}
	for i := len(parents) - 1; i >= 0; i-- {
		current := parents[i]
		info, err := r.volume.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return githubGitInvalid()
		}
		if err := r.remember(current, info, ""); err != nil {
			return err
		}
	}
	return nil
}

func (r *githubGitReader) read(name string, limit int64) (string, error) {
	if err := r.directory(path.Dir(name)); err != nil {
		return "", err
	}
	file, info, err := projectRegular(r.volume, name)
	if errors.Is(err, fs.ErrNotExist) {
		if err := r.remember(name, nil, ""); err != nil {
			return "", err
		}
		return "", fs.ErrNotExist
	}
	if err != nil {
		return "", githubGitInvalid()
	}
	defer file.Close()
	if info.Size() > limit {
		return "", githubGitUnsupported()
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > limit || !projectText(data) {
		return "", githubGitInvalid()
	}
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return "", githubGitChanged()
	}
	if err := r.remember(name, info, string(data)); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *service) projectGitHubGit(scope protocol.ProjectFileScope) (protocol.GitHubGitState, error) {
	snapshot, err := s.projectGitHubGitSnapshot(scope)
	return snapshot.state, err
}

// This reader deliberately never starts Git or follows configuration includes.
// Only the registered root's metadata can select the worktree and its HEAD.
func (s *service) projectGitHubGitSnapshot(scope protocol.ProjectFileScope) (githubGitSnapshot, error) {
	result := githubGitSnapshot{state: protocol.GitHubGitState{Remotes: []protocol.GitHubRemote{}}}
	root, original, _, err := s.openProjectFiles(scope, false)
	if err != nil {
		return result, err
	}
	defer root.Close()
	volume, err := os.OpenRoot(s.config.ProjectRoot)
	if err != nil {
		return result, err
	}
	defer volume.Close()
	selected := scope.Root
	if selected == "" {
		selected = "."
	}
	current, err := volume.Lstat(selected)
	if err != nil || !os.SameFile(original, current) {
		return result, githubGitChanged()
	}
	reader := &githubGitReader{volume: volume, files: map[string]githubGitMetadata{}}
	if err := reader.directory(scope.Root); err != nil {
		return result, err
	}
	gitFile := path.Join(scope.Root, ".git")
	info, err := volume.Lstat(gitFile)
	if errors.Is(err, fs.ErrNotExist) {
		if err := reader.remember(gitFile, nil, ""); err != nil {
			return result, err
		}
		result.files = reader.files
		return result, s.verifyProjectFiles(scope, original)
	}
	if err != nil {
		return result, err
	}
	gitDir := gitFile
	if info.IsDir() {
		if err := reader.directory(gitDir); err != nil {
			return result, err
		}
	} else {
		text, err := reader.read(gitFile, 4096)
		if err != nil {
			return result, err
		}
		if !strings.HasPrefix(text, "gitdir: ") || strings.ContainsAny(text, "\n\r") {
			return result, githubGitInvalid()
		}
		gitDir, err = s.restoreGitPath(scope.Root, strings.TrimPrefix(text, "gitdir: "))
		if err != nil {
			return result, githubGitInvalid()
		}
		if err := reader.directory(gitDir); err != nil {
			return result, githubGitInvalid()
		}
	}
	commonDir := gitDir
	if text, err := reader.read(path.Join(gitDir, "commondir"), 4096); err == nil {
		commonDir, err = s.restoreGitPath(gitDir, text)
		if err != nil || strings.ContainsAny(text, "\n\r") {
			return result, githubGitInvalid()
		}
		if err := reader.directory(commonDir); err != nil {
			return result, githubGitInvalid()
		}
		if gitDir != gitFile {
			back, err := reader.read(path.Join(gitDir, "gitdir"), 4096)
			if err != nil {
				return result, githubGitInvalid()
			}
			backPath, err := s.restoreGitPath(gitDir, back)
			if err != nil || backPath != gitFile || strings.ContainsAny(back, "\n\r") {
				return result, githubGitInvalid()
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	for _, dir := range []string{gitDir, commonDir} {
		name := path.Join(dir, "reftable")
		if _, err := volume.Lstat(name); err == nil {
			return result, githubGitUnsupported()
		} else if !errors.Is(err, fs.ErrNotExist) {
			return result, err
		}
		if err := reader.remember(name, nil, ""); err != nil {
			return result, err
		}
	}
	config := githubGitConfig{}
	if text, err := reader.read(path.Join(commonDir, "config"), githubGitConfigLimit); err == nil {
		config, err = parseGitHubGitConfig(text)
		if err != nil {
			return result, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	if config.worktreeConfig {
		if text, err := reader.read(path.Join(gitDir, "config.worktree"), githubGitConfigLimit); err == nil {
			private, err := parseGitHubGitConfig(text)
			if err != nil {
				return result, err
			}
			config.remotes = append(config.remotes, private.remotes...)
			config.partial = config.partial || private.partial
		} else if !errors.Is(err, fs.ErrNotExist) {
			return result, err
		}
	}
	result.state.Repository = true
	result.state.RemotesPartial = config.partial
	seen := map[string]bool{}
	for _, remote := range config.remotes {
		if repository := gitHubRemoteRepository(remote.url); repository != "" {
			key := remote.name + "\x00" + repository
			if !seen[key] {
				seen[key] = true
				result.state.Remotes = append(result.state.Remotes, protocol.GitHubRemote{Name: remote.name, Repository: repository})
			}
		}
	}
	sort.Slice(result.state.Remotes, func(i, j int) bool {
		a, b := result.state.Remotes[i], result.state.Remotes[j]
		if a.Name == b.Name {
			return a.Repository < b.Repository
		}
		return a.Name < b.Name
	})
	if len(result.state.Remotes) > 64 {
		result.state.Remotes = result.state.Remotes[:64]
		result.state.RemotesPartial = true
	}
	head, err := reader.read(path.Join(gitDir, "HEAD"), 4096)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return result, githubGitInvalid()
		}
		return result, err
	}
	if strings.HasPrefix(head, "ref: ") {
		ref := strings.TrimPrefix(head, "ref: ")
		if !strings.HasPrefix(ref, "refs/heads/") || !githubGitRef(ref) {
			return result, githubGitInvalid()
		}
		result.state.Branch = strings.TrimPrefix(ref, "refs/heads/")
		sha, err := reader.resolveRef(gitDir, commonDir, ref, config.objectFormat, map[string]bool{})
		if errors.Is(err, fs.ErrNotExist) {
			result.state.Unborn = true
		} else if err != nil {
			return result, err
		} else {
			result.state.HeadSHA = sha
		}
	} else {
		if !githubGitSHA(head, config.objectFormat) {
			return result, githubGitInvalid()
		}
		result.state.Detached = true
		result.state.HeadSHA = strings.ToLower(head)
	}
	// Re-read HEAD so a concurrent branch switch cannot combine its old branch
	// name with a ref from the new checkout.
	if _, err := reader.read(path.Join(gitDir, "HEAD"), 4096); err != nil {
		return result, err
	}
	if err := s.verifyProjectFiles(scope, original); err != nil {
		return result, err
	}
	result.files = reader.files
	return result, nil
}

func githubGitSHA(value, format string) bool {
	size := 40
	if format == "sha256" {
		size = 64
	}
	if len(value) != size {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func githubGitRef(value string) bool {
	if !strings.HasPrefix(value, "refs/") || len(value) > 1024 || strings.HasSuffix(value, ".") || strings.Contains(value, "@{") || strings.Contains(value, "..") || strings.ContainsAny(value, "~^:?*[\\") || strings.IndexFunc(value, func(c rune) bool { return unicode.IsControl(c) || unicode.IsSpace(c) }) >= 0 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func (r *githubGitReader) resolveRef(gitDir, commonDir, ref, format string, seen map[string]bool) (string, error) {
	if !githubGitRef(ref) || seen[ref] || len(seen) >= 8 {
		return "", githubGitInvalid()
	}
	seen[ref] = true
	private := strings.HasPrefix(ref, "refs/bisect/") || strings.HasPrefix(ref, "refs/worktree/") || strings.HasPrefix(ref, "refs/rewritten/")
	refDir := commonDir
	if private {
		refDir = gitDir
	}
	text, err := r.read(path.Join(refDir, ref), 4096)
	if err == nil {
		if strings.HasPrefix(text, "ref: ") {
			return r.resolveRef(gitDir, commonDir, strings.TrimPrefix(text, "ref: "), format, seen)
		}
		if !githubGitSHA(text, format) {
			return "", githubGitInvalid()
		}
		return strings.ToLower(text), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	// Git never packs worktree-private refs. A missing private target must not
	// acquire another worktree's value from shared metadata or packed refs.
	if private {
		return "", fs.ErrNotExist
	}
	text, err = r.read(path.Join(commonDir, "packed-refs"), githubGitRefsLimit)
	if err != nil {
		return "", err
	}
	found := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "^") {
			if !githubGitSHA(strings.TrimPrefix(line, "^"), format) {
				return "", githubGitInvalid()
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !githubGitSHA(fields[0], format) || !githubGitRef(fields[1]) {
			return "", githubGitInvalid()
		}
		if fields[1] == ref {
			if found != "" {
				return "", githubGitInvalid()
			}
			found = strings.ToLower(fields[0])
		}
	}
	if found == "" {
		return "", fs.ErrNotExist
	}
	return found, nil
}

type githubGitRemoteConfig struct {
	name string
	url  string
}

type githubGitConfig struct {
	objectFormat   string
	worktreeConfig bool
	partial        bool
	remotes        []githubGitRemoteConfig
}

var githubGitSection = regexp.MustCompile(`^\[([A-Za-z][A-Za-z0-9.-]*)(?:[ \t]+"((?:[^"\\]|\\["\\])*)")?\][ \t]*(?:[#;].*)?$`)
var githubGitKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

func parseGitHubGitConfig(text string) (githubGitConfig, error) {
	result := githubGitConfig{objectFormat: "sha1"}
	section, subsection := "", ""
	continuation := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		continued := strings.HasSuffix(line, "\\") && !strings.HasSuffix(line, "\\\\")
		if continuation || continued {
			result.partial = true
			continuation = continued
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			match := githubGitSection.FindStringSubmatch(line)
			if match == nil {
				return result, githubGitInvalid()
			}
			section, subsection = strings.ToLower(match[1]), match[2]
			if subsection == "" {
				if base, name, ok := strings.Cut(section, "."); ok {
					section, subsection = base, name
				}
			}
			if section == "include" || section == "includeif" {
				result.partial = true
			}
			continue
		}
		key, raw, assigned := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !githubGitKey.MatchString(key) || section == "" {
			return result, githubGitInvalid()
		}
		value := "true"
		if assigned {
			var err error
			value, err = githubGitConfigValue(raw)
			if err != nil {
				return result, err
			}
		}
		if section == "extensions" {
			switch key {
			case "refstorage":
				if !strings.EqualFold(value, "files") {
					return result, githubGitUnsupported()
				}
			case "objectformat":
				if value != "sha1" && value != "sha256" {
					return result, githubGitUnsupported()
				}
				result.objectFormat = value
			case "worktreeconfig":
				result.worktreeConfig = value == "true" || value == "yes" || value == "on" || value == "1"
			}
		}
		if section == "core" && key == "repositoryformatversion" && value != "0" && value != "1" {
			return result, githubGitUnsupported()
		}
		if section == "remote" && key == "url" && assigned && subsection != "" && len(subsection) <= 256 && strings.IndexFunc(subsection, unicode.IsControl) < 0 {
			result.remotes = append(result.remotes, githubGitRemoteConfig{name: subsection, url: value})
		}
	}
	return result, nil
}

func githubGitConfigValue(raw string) (string, error) {
	var value strings.Builder
	quoted, escaped := false, false
	for _, c := range strings.TrimSpace(raw) {
		if escaped {
			switch c {
			case '\\', '"':
				value.WriteRune(c)
			case 'n':
				value.WriteByte('\n')
			case 't':
				value.WriteByte('\t')
			case 'b':
				value.WriteByte('\b')
			default:
				return "", githubGitInvalid()
			}
			escaped = false
			continue
		}
		switch {
		case c == '\\':
			escaped = true
		case c == '"':
			quoted = !quoted
		case !quoted && (c == '#' || c == ';'):
			return strings.TrimSpace(value.String()), nil
		default:
			value.WriteRune(c)
		}
	}
	if quoted || escaped {
		return "", githubGitInvalid()
	}
	return strings.TrimSpace(value.String()), nil
}

var githubRepositoryOwner = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
var githubRepositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

func gitHubRemoteRepository(value string) string {
	if strings.ContainsAny(value, "\\?#") {
		return ""
	}
	name := ""
	if strings.HasPrefix(value, "git@github.com:") {
		name = strings.TrimPrefix(value, "git@github.com:")
	} else {
		u, err := url.Parse(value)
		if err != nil || !strings.EqualFold(u.Host, "github.com") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" {
			return ""
		}
		if u.Scheme == "https" {
			if u.User != nil {
				return ""
			}
		} else if u.Scheme == "ssh" {
			if u.User == nil || u.User.Username() != "git" {
				return ""
			}
			if _, password := u.User.Password(); password {
				return ""
			}
		} else {
			return ""
		}
		name = strings.TrimPrefix(u.Path, "/")
	}
	name = strings.TrimSuffix(name, ".git")
	owner, repository, ok := strings.Cut(name, "/")
	if !ok || !githubRepositoryOwner.MatchString(owner) || !githubRepositoryName.MatchString(repository) || repository == "." || repository == ".." {
		return ""
	}
	return owner + "/" + repository
}
