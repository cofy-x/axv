package githubreview

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

type TestProfile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	EntryPath string   `json:"entryPath"`
	Command   []string `json:"command"`
	AppliesTo []string `json:"appliesTo,omitempty"`
}

func (p *TestProfile) UnmarshalJSON(data []byte) error {
	return decodeFields(data, map[string]any{"id": &p.ID, "name": &p.Name, "entryPath": &p.EntryPath, "command": &p.Command, "appliesTo": &p.AppliesTo}, "appliesTo")
}

var testProfileID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func validTestPath(p string) bool {
	if p == "" || len(p) > 512 || p == "." || path.IsAbs(p) || path.Clean(p) != p ||
		strings.TrimSpace(p) != p || strings.ContainsAny(p, "\\*?[]") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return false
	}
	for _, component := range strings.Split(p, "/") {
		if component == ".." || strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}

func validTestCommand(argv []string) bool {
	if len(argv) > 32 {
		return false
	}
	for _, arg := range argv {
		if strings.TrimSpace(arg) == "" || len(arg) > 512 || strings.IndexFunc(arg, unicode.IsControl) >= 0 {
			return false
		}
	}
	return true
}

func validTestProfiles(profiles []TestProfile) bool {
	if len(profiles) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, p := range profiles {
		name := strings.TrimSpace(p.Name)
		if !testProfileID.MatchString(p.ID) || seen[p.ID] || name == "" || len(name) > 80 || strings.IndexFunc(p.Name, unicode.IsControl) >= 0 ||
			!validTestPath(p.EntryPath) || len(p.Command) == 0 || !validTestCommand(p.Command) || len(p.AppliesTo) > 16 {
			return false
		}
		seen[p.ID] = true
		for _, scope := range p.AppliesTo {
			if !validTestPath(strings.TrimSuffix(scope, "/")) {
				return false
			}
		}
	}
	return true
}

func normalizeTestProfiles(profiles []TestProfile) []TestProfile {
	out := slices.Clone(profiles)
	for i, p := range out {
		out[i].Name = strings.TrimSpace(p.Name)
		out[i].AppliesTo = slices.Clone(p.AppliesTo)
		for j, scope := range p.AppliesTo {
			out[i].AppliesTo[j] = strings.TrimSuffix(scope, "/")
		}
	}
	return out
}

// Existing unguarded commands still run as configured. Infer only a direct
// invocation of a repository script, never options, shell code or Go packages.
func legacyTestEntry(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	p := ""
	if strings.HasPrefix(argv[0], "./") {
		p = argv[0]
	} else if len(argv) >= 2 {
		switch argv[0] {
		case "python", "python3":
			if path.Ext(argv[1]) == ".py" {
				p = argv[1]
			}
		case "bash", "sh":
			if ext := path.Ext(argv[1]); ext == ".sh" || ext == ".bash" {
				p = argv[1]
			}
		case "node":
			if ext := path.Ext(argv[1]); ext == ".js" || ext == ".cjs" || ext == ".mjs" {
				p = argv[1]
			}
		}
		if strings.HasPrefix(p, "-") || !strings.Contains(p, "/") {
			return ""
		}
	}
	p = strings.TrimPrefix(p, "./")
	if validTestPath(p) {
		return p
	}
	return ""
}

func newTestAttempt(policy RepositoryPolicy, testID, baseSHA string, number int, now time.Time) (VMTestAttempt, error) {
	a := VMTestAttempt{Attempt: number, Status: "queued", UpdatedAt: now.UTC(), Selection: "automatic"}
	if len(policy.TestProfiles) > 0 {
		a.SelectionBaseSHA = baseSHA
	}
	if testID != "" {
		for _, p := range policy.TestProfiles {
			if p.ID == testID {
				a.Selection = "manual"
				a.PlanHash = reviewDigest(p)
				a.TestID, a.TestName, a.EntryPath, a.Command = p.ID, p.Name, p.EntryPath, p.Command
				return a, nil
			}
		}
		return a, ErrRetryStale
	}
	if len(policy.TestProfiles) > 0 {
		a.PlanHash = reviewDigest(policy.TestProfiles)
		return a, nil
	}
	if len(policy.TestCommand) == 0 {
		return a, ErrRetryStale
	}
	a.Command = policy.TestCommand
	a.EntryPath = legacyTestEntry(a.Command)
	return a, nil
}

func (a VMTestAttempt) matchesPolicy(policy RepositoryPolicy, command []string) bool {
	if a.PlanHash == "" {
		return len(policy.TestProfiles) == 0 && slices.Equal(command, policy.TestCommand)
	}
	if a.Selection == "manual" {
		for _, p := range policy.TestProfiles {
			if p.ID == a.TestID {
				return a.PlanHash == reviewDigest(p)
			}
		}
		return false
	}
	return a.PlanHash == reviewDigest(policy.TestProfiles)
}

func matchedTestScope(scopes, changes []string) string {
	for _, scope := range scopes {
		for _, changed := range changes {
			if changed == scope || strings.HasPrefix(changed, scope+"/") {
				return scope
			}
		}
	}
	return ""
}

// The provider checks an immutable Git tree. Transport and authentication
// failures are errors; neither can prove that an entry is absent.
type testEntryCheck func(path string, legacy bool) (bool, error)
type testEntryProvider interface {
	testEntryCheck(context.Context, string, Target) (testEntryCheck, error)
}
type testChangeProvider interface {
	ChangedTestPaths(context.Context, string, Target) ([]string, error)
}

func (s *Service) selectTest(ctx context.Context, job Job, policy RepositoryPolicy, a *VMTestAttempt) error {
	profiles := policy.TestProfiles
	legacy := len(profiles) == 0
	if legacy {
		if a.EntryPath == "" {
			return nil
		}
		profiles = []TestProfile{{EntryPath: a.EntryPath, Command: a.Command}}
	}
	head := job.HeadSHA[:12]
	var check testEntryCheck
	var changes []string
	checkedChanges := false
	var reasons []string
	for _, profile := range profiles {
		if a.Selection == "manual" && profile.ID != a.TestID {
			continue
		}
		scope := ""
		if a.Selection != "manual" && len(profile.AppliesTo) > 0 {
			if !checkedChanges {
				provider, ok := s.github.(testChangeProvider)
				if !ok {
					return errors.New("Changed paths are unavailable for test selection.")
				}
				var err error
				changes, err = provider.ChangedTestPaths(ctx, job.account, job.Target)
				if err != nil {
					return fmt.Errorf("Could not verify changes for test selection: %w", err)
				}
				checkedChanges = true
			}
			scope = matchedTestScope(profile.AppliesTo, changes)
			if scope == "" {
				reasons = append(reasons, profile.Name+" did not match the changed paths.")
				continue
			}
		}
		if check == nil {
			provider, ok := s.github.(testEntryProvider)
			if !ok {
				return errors.New("The test entry could not be checked at the PR head.")
			}
			var err error
			check, err = provider.testEntryCheck(ctx, job.account, job.Target)
			if err != nil {
				return fmt.Errorf("Could not verify the test entry at %s: %w", head, err)
			}
		}
		exists, err := check(profile.EntryPath, legacy)
		if err != nil {
			return fmt.Errorf("Could not verify %s at %s: %w", profile.EntryPath, head, err)
		}
		if !exists {
			reasons = append(reasons, fmt.Sprintf("No matching entry %s at %s.", profile.EntryPath, head))
			continue
		}
		a.TestID, a.TestName, a.EntryPath, a.Command = profile.ID, profile.Name, profile.EntryPath, profile.Command
		if scope != "" {
			reasons = append(reasons, "Changes matched "+scope+".")
		}
		reasons = append(reasons, fmt.Sprintf("Selected %s at %s.", profile.EntryPath, head))
		a.SelectionReason = strings.Join(reasons, " ")
		return nil
	}
	a.Status = "not_applicable"
	a.SelectionReason = strings.Join(reasons, " ")
	return nil
}
