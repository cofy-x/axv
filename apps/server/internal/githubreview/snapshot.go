package githubreview

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxReviewPatch = 192 << 10
	maxSourceFile  = 32 << 10
	maxSourceGroup = 48 << 10
)

var (
	reviewHunk = regexp.MustCompile(`^@@ -([0-9]+)(?:,[0-9]+)? \+([0-9]+)(?:,[0-9]+)? @@`)
)

type reviewSource struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

type reviewSnapshot struct {
	patch   string
	anchors reviewAnchors
	source  []reviewSource
}

type reviewAnchors struct {
	base map[string]map[int]bool
	head map[string]map[int]bool
}

func (a reviewAnchors) empty() bool { return len(a.base) == 0 && len(a.head) == 0 }

type reviewGit struct {
	dir string
}

// Keep the object store private and never check out PR content. GitHub is the
// only remote and only fetch receives its credential.
func (g reviewGit) command(ctx context.Context, token string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = g.dir
	command.WaitDelay = time.Second
	command.Env = append(cliEnvironment("git", ""),
		"HOME="+g.dir, "USERPROFILE="+g.dir, "XDG_CONFIG_HOME="+g.dir,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ALLOW_PROTOCOL=https", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_ATTR_NOSYSTEM=1",
		"LC_ALL=C", "GIT_CONFIG_COUNT=4",
		"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0="+os.DevNull,
		"GIT_CONFIG_KEY_1=credential.helper", "GIT_CONFIG_VALUE_1=",
		"GIT_CONFIG_KEY_2=http.followRedirects", "GIT_CONFIG_VALUE_2=false",
		"GIT_CONFIG_KEY_3=core.attributesFile", "GIT_CONFIG_VALUE_3="+os.DevNull,
	)
	if token != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		command.Env = append(command.Env, "GIT_CONFIG_COUNT=5",
			"GIT_CONFIG_KEY_4=http.https://github.com/.extraHeader",
			"GIT_CONFIG_VALUE_4=Authorization: Basic "+encoded)
	}
	return command
}

func (g reviewGit) fetch(ctx context.Context, target Target, token string) (string, error) {
	if !target.valid() {
		return "", errors.New("GitHub returned an invalid review target")
	}
	if err := g.command(ctx, "", "init", "--bare", "--template=", "--object-format=sha1").Run(); err != nil {
		return "", reviewProcessError(ctx, "Could not initialize the PR source cache. Check that Git is installed.")
	}
	remote := "https://github.com/" + target.Repository + ".git"
	for _, depth := range []string{"64", "256", "1024"} {
		fetch := g.command(ctx, token, "fetch", "--no-tags", "--no-recurse-submodules", "--no-auto-gc", "--no-write-fetch-head", "--depth="+depth, "--",
			remote, target.BaseSHA, target.HeadSHA)
		if err := fetch.Run(); err != nil {
			return "", reviewProcessError(ctx, "Could not fetch the exact PR commits. Check GitHub access and whether the PR has changed.")
		}
		base, err := g.output(ctx, 64, "merge-base", target.BaseSHA, target.HeadSHA)
		if err == nil && commitSHA.MatchString(strings.TrimSpace(string(base))) {
			return strings.TrimSpace(string(base)), nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return "", errors.New("Could not find a common ancestor within 1,024 commits of the PR revisions")
}

func (g reviewGit) output(ctx context.Context, limit int, args ...string) ([]byte, error) {
	command := g.command(ctx, "", args...)
	output := cliOutput{limit: limit}
	command.Stdout = &output
	err := command.Run()
	if output.truncated {
		return nil, errReviewOutputLimit
	}
	if err != nil {
		return nil, err
	}
	return output.data, nil
}

func (g reviewGit) snapshot(ctx context.Context, base, head string) (reviewSnapshot, error) {
	patch, err := g.output(ctx, maxReviewPatch, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--submodule=short", "--find-renames", base, head, "--")
	if errors.Is(err, errReviewOutputLimit) {
		return reviewSnapshot{}, errors.New("The PR diff exceeds the 192 KiB review limit")
	}
	if err != nil {
		return reviewSnapshot{}, reviewProcessError(ctx, "Could not read the exact PR diff")
	}
	anchors, changed := reviewDiffAnchors(patch)
	if anchors.empty() {
		return reviewSnapshot{patch: string(patch), anchors: anchors}, nil
	}
	source, err := g.reviewSources(ctx, head, changed)
	if err != nil {
		return reviewSnapshot{}, err
	}
	return reviewSnapshot{patch: string(patch), anchors: anchors, source: source}, nil
}

// Use exact locations from the patch, mapping context to the head side and
// deleted lines to the base side. An old path can differ after a rename.
func reviewDiffAnchors(patch []byte) (reviewAnchors, []string) {
	anchors := reviewAnchors{base: map[string]map[int]bool{}, head: map[string]map[int]bool{}}
	var paths []string
	seen := map[string]bool{}
	var oldPath, newPath string
	var oldLine, newLine int
	inHunk := false
	for entry := range strings.SplitSeq(string(patch), "\n") {
		switch {
		case strings.HasPrefix(entry, "diff --git "):
			oldPath, newPath, inHunk = "", "", false
		case !inHunk && strings.HasPrefix(entry, "--- "):
			oldPath = reviewPatchPath(strings.TrimPrefix(entry, "--- "), "a/")
		case !inHunk && strings.HasPrefix(entry, "+++ "):
			newPath = reviewPatchPath(strings.TrimPrefix(entry, "+++ "), "b/")
		case strings.HasPrefix(entry, "@@ "):
			match := reviewHunk.FindStringSubmatch(entry)
			inHunk = len(match) == 3
			if inHunk {
				oldLine, _ = strconv.Atoi(match[1])
				newLine, _ = strconv.Atoi(match[2])
				filename := newPath
				if filename == "" {
					filename = oldPath
				}
				if filename != "" && !seen[filename] {
					seen[filename] = true
					paths = append(paths, filename)
				}
			}
		case inHunk && entry != "":
			switch entry[0] {
			case '+':
				addReviewAnchor(anchors.head, newPath, newLine)
				newLine++
			case '-':
				addReviewAnchor(anchors.base, oldPath, oldLine)
				oldLine++
			case ' ':
				addReviewAnchor(anchors.head, newPath, newLine)
				oldLine++
				newLine++
			}
		}
	}
	return anchors, paths
}

func addReviewAnchor(side map[string]map[int]bool, filename string, line int) {
	if filename == "" || line <= 0 {
		return
	}
	if side[filename] == nil {
		side[filename] = map[int]bool{}
	}
	side[filename][line] = true
}

func reviewPatchPath(raw, prefix string) string {
	raw = strings.TrimSuffix(raw, "\t")
	if strings.HasPrefix(raw, `"`) {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return ""
		}
		raw = unquoted
	}
	filename, ok := strings.CutPrefix(raw, prefix)
	if !ok || !validReviewPath(filename) {
		return ""
	}
	return filename
}

func validReviewPath(filename string) bool {
	return filename != "" && filename != "." && filename != ".." && !strings.HasPrefix(filename, "../") &&
		!path.IsAbs(filename) && path.Clean(filename) == filename &&
		!strings.ContainsAny(filename, "\x00\r\n\t") && utf8.ValidString(filename)
}

type reviewBlob struct {
	path string
	sha  string
	size int
}

func reviewTree(tree []byte) []reviewBlob {
	var blobs []reviewBlob
	for entry := range strings.SplitSeq(string(tree), "\x00") {
		meta, filename, ok := strings.Cut(entry, "\t")
		parts := strings.Fields(meta)
		if !ok || len(parts) != 4 || (parts[0] != "100644" && parts[0] != "100755") || parts[1] != "blob" || !commitSHA.MatchString(parts[2]) {
			continue
		}
		size, err := strconv.Atoi(parts[3])
		if err == nil && size > 0 && size <= maxSourceFile && validReviewPath(filename) {
			blobs = append(blobs, reviewBlob{path: filename, sha: parts[2], size: size})
		}
	}
	return blobs
}

func (g reviewGit) reviewSources(ctx context.Context, head string, changed []string) ([]reviewSource, error) {
	tree, err := g.output(ctx, 8<<20, "ls-tree", "-r", "-l", "-z", "--full-tree", head)
	if err != nil {
		return nil, reviewProcessError(ctx, "Could not enumerate the exact PR source tree within its size limit")
	}
	blobs := reviewTree(tree)
	byPath := make(map[string]reviewBlob, len(blobs))
	for _, blob := range blobs {
		byPath[blob.path] = blob
	}
	changedPaths := make(map[string]bool, len(changed))
	directories := map[string]bool{}
	for _, filename := range changed {
		changedPaths[filename] = true
		directories[path.Dir(filename)] = true
	}
	var direct, related []reviewBlob
	for _, filename := range changed {
		if blob, ok := byPath[filename]; ok {
			direct = append(direct, blob)
		}
	}
	for _, blob := range blobs {
		if !changedPaths[blob.path] && directories[path.Dir(blob.path)] {
			related = append(related, blob)
		}
	}
	var source []reviewSource
	for _, group := range [][]reviewBlob{direct, related} {
		budget, files := maxSourceGroup, 12
		for _, blob := range group {
			if files == 0 || budget == 0 {
				break
			}
			if blob.size > budget {
				continue
			}
			data, err := g.output(ctx, maxSourceFile, "cat-file", "blob", blob.sha)
			if err != nil {
				return nil, reviewProcessError(ctx, "Could not read related source from the exact PR commit")
			}
			files--
			if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
				continue
			}
			budget -= len(data)
			source = append(source, reviewSource{Path: blob.path, Text: string(data)})
		}
	}
	return source, nil
}

var errReviewOutputLimit = errors.New("review command output limit exceeded")

// Hide subprocess diagnostics, which can include provider responses, source,
// or network details. Keep cancellation machine-readable for the scheduler.
func reviewProcessError(ctx context.Context, message string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New(message)
}

func makeReviewWorkspace(parent string) (string, error) {
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	parent, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "review-")
}

func readReviewOutput(filename string, limit int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if int64(len(data)) > limit {
		return nil, errReviewOutputLimit
	}
	return data, err
}
