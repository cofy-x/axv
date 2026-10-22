//go:build linux

package guest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func TestTypedInitializationRestartKeepsCreatedRootWithoutReexecution(t *testing.T) {
	s, in := typedInitialization(t, "captured-before-restart", "")
	if _, err := s.tasks.submit(in); err != nil {
		t.Fatal(err)
	}
	final := waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	root, groups := s.tasks.root, s.tasks.groups
	s.tasks.close()
	data, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal guestTaskJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	record := journal.Attempts[in.AttemptID]
	record.Status, record.LaunchIntent = "accepted", false
	record.FinishedAt = nil
	data, err = json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(filepath.Join(root, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s.tasks, err = openProjectTasks(s, root, groups, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.tasks.submit(in)
	if err != nil || recovered.Status != "interrupted" || recovered.CreatedRoot == nil || *recovered.CreatedRoot != *final.CreatedRoot {
		t.Fatalf("restart lost captured root or reexecuted initialization: %+v %v", recovered, err)
	}
}

func typedInitialization(t *testing.T, directory, repository string) (*service, protocol.TaskSubmit) {
	t.Helper()
	s, scope, _ := taskFixture(t)
	s.capabilities = append(s.capabilities, "project-initialization")
	in := taskSubmission(scope, "typed-init-original", "unused")
	in.Spec.Argv = []string{}
	in.Spec.Initialization = &protocol.ProjectInitialization{Directory: directory, Repository: repository, Name: "New project"}
	return s, in
}

func TestTypedInitializationCapturesRootBeforeCloneAndSuppressesRawOutput(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "git-started")
	script := `#!/bin/sh
printf '%s\n' 'secret-from-server' >&2
printf '%s' "$PWD" > "$AXV_INIT_CAPTURE"
exit 128
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AXV_INIT_CAPTURE", marker)
	s, in := typedInitialization(t, "literal';touch marker;#", "https://example.com/repo.git")
	if _, err := s.tasks.submit(in); err != nil {
		t.Fatal(err)
	}
	result := waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	if result.Status != "failed" || result.CreatedRoot == nil || !strings.Contains(result.Reason, "Clone failed") {
		t.Fatalf("typed clone result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(s.config.ProjectRoot, in.Spec.Initialization.Directory)); err != nil {
		t.Fatal("partial destination lost", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != filepath.Join(s.config.ProjectRoot, in.Spec.Initialization.Directory) {
		t.Fatalf("Git escaped captured child directory: %q %v", data, err)
	}
	if result.LogBytes != 0 || strings.Contains(result.Reason, "secret-from-server") {
		t.Fatal("raw remote credentials entered task history")
	}
	// Journal result identity is durable even when clone failed.
	state, err := os.ReadFile(filepath.Join(s.tasks.root, "state.json"))
	if err != nil || !strings.Contains(string(state), result.CreatedRoot.RootIdentity) {
		t.Fatal("created root receipt was not persisted", err)
	}
	replay, err := s.tasks.submit(in)
	if err != nil || replay.CreatedRoot == nil || *replay.CreatedRoot != *result.CreatedRoot {
		t.Fatalf("replay changed created root: %+v %v", replay, err)
	}
}

func TestTypedInitializationDoesNotAdoptReplacementDirectory(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	s, in := typedInitialization(t, "new-project", "https://example.com/repo.git")
	child := filepath.Join(s.config.ProjectRoot, in.Spec.Initialization.Directory)
	script := `#!/bin/sh
mv -- "$AXV_INIT_CHILD" "$AXV_INIT_CHILD-old"
mkdir -- "$AXV_INIT_CHILD"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AXV_INIT_CHILD", child)
	if _, err := s.tasks.submit(in); err != nil {
		t.Fatal(err)
	}
	result := waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	if result.Status != "failed" || result.CreatedRoot == nil || !strings.Contains(result.Reason, "registered identity") {
		t.Fatalf("replacement was adopted: %+v", result)
	}
}

func TestTypedInitializationCreateOnlyAndOrdinaryArgvAreDistinct(t *testing.T) {
	s, in := typedInitialization(t, "new-project", "")
	if _, err := s.tasks.submit(in); err != nil {
		t.Fatal(err)
	}
	result := waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	if result.Status != "succeeded" || result.CreatedRoot == nil {
		t.Fatalf("empty project not captured: %+v", result)
	}
	second := in
	second.TaskID = "task-typed-init-next"
	second.AttemptID = "attempt-typed-init-next"
	if _, err := s.tasks.submit(second); err != nil {
		t.Fatal(err)
	}
	failed := waitTask(t, s, taskSubmitIdentity(second), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	if failed.Status != "failed" || failed.CreatedRoot != nil {
		t.Fatalf("existing destination was reused: %+v", failed)
	}
	forged := in
	forged.Spec.Argv = []string{"/bin/true"}
	if err := validateGuestTaskSpec(forged.Spec); err == nil {
		t.Fatal("ordinary argv mixed with initialization intent")
	}
}
