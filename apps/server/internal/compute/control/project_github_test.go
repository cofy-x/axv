package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func githubObservation() protocol.ProjectGitHubObservation {
	return protocol.ProjectGitHubObservation{
		Git:          protocol.GitHubGitState{Repository: true, HeadSHA: strings.Repeat("a", 40), Branch: "feature", Remotes: []protocol.GitHubRemote{}},
		Account:      protocol.GitHubAccount{ID: "user-node", Login: "developer"},
		Repository:   protocol.GitHubRepository{ID: "repo-node", NameWithOwner: "example/project", URL: "https://github.com/example/project"},
		PullRequests: []protocol.GitHubPullRequest{}, PullRequestsComplete: true, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func githubProject(t *testing.T) (*projectFixture, compute.ProjectBindingResult) {
	t.Helper()
	f := newProjectFixture(t, "project-github")
	b := f.registered(t)
	f.mu.Lock()
	f.file = func(w http.ResponseWriter, r *http.Request) {
		var in protocol.ProjectGitHubRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Root != "repo" || in.RootIdentity != "inode:123" || in.BootID != b.Binding.BootID || in.InstanceID != b.Binding.InstanceID || !strings.HasPrefix(r.URL.Path, "/v1/project-github/") {
			t.Error("GitHub read escaped its typed original project scope")
			http.Error(w, "wrong scope", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(githubObservation())
	}
	f.mu.Unlock()
	return f, b
}

func associateGitHub(t *testing.T, f *projectFixture, b compute.ProjectBindingResult) compute.ProjectGitHubState {
	t.Helper()
	result, err := f.service.ProjectGitHub(t.Context(), b.Project.ID, "associate", compute.ProjectGitHubRequest{Binding: b.Binding, Repository: "example/project", RepositoryID: "repo-node", AccountID: "user-node"})
	if err != nil {
		t.Fatal(err)
	}
	return result.(compute.ProjectGitHubState)
}

func TestProjectGitHubAssociationPersistsWithoutChangingFileBinding(t *testing.T) {
	f, b := githubProject(t)
	state := associateGitHub(t, f, b)
	if state.Revision != 1 || state.Association.Repository.ID != "repo-node" || state.Association.MachineID != b.Binding.MachineID || state.Association.Profile != "axv" {
		t.Fatalf("association = %#v", state)
	}
	current, err := f.service.ProjectBinding(t.Context(), b.Project.ID, b.Binding.HostID)
	if err != nil || current.Binding != b.Binding || current.Project.Revision != b.Project.Revision {
		t.Fatalf("GitHub setup invalidated file/editor binding: %#v %v", current, err)
	}
	reopened, err := OpenStore(f.service.store.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	p, err := reopened.Project(t.Context(), b.Project.ID)
	if err != nil || p.GitHubRevision != 1 || p.GitHub == nil || p.GitHub.Account.ID != "user-node" {
		t.Fatalf("association did not persist independently: %#v %v", p, err)
	}
	if _, err = f.service.store.setProjectGitHub(t.Context(), b.Project.ID, 0, nil); compute.ErrorCode(err) != "github_association_changed" {
		t.Fatalf("old revision overwrote association: %v", err)
	}
	cleared, err := f.service.ProjectGitHub(t.Context(), b.Project.ID, "clear", compute.ProjectGitHubRequest{Binding: b.Binding, Revision: 1})
	if err != nil || cleared.(compute.ProjectGitHubState).Revision != 2 || cleared.(compute.ProjectGitHubState).Association != nil {
		t.Fatalf("clear = %#v %v", cleared, err)
	}
}

func TestProjectGitHubRefreshUsesNoTaskOrOperationReceipts(t *testing.T) {
	f, b := githubProject(t)
	associateGitHub(t, f, b)
	// Fill the host's actual task-history quota. This is independent of the
	// guest's command capacity, and must not prevent a metadata refresh.
	for i := range 256 {
		if _, err := f.service.store.db.Exec("INSERT INTO project_tasks(id,project_id,data) VALUES(?,?,?)", fmt.Sprintf("task-quota-%03d", i), b.Project.ID, `{}`); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.service.store.admitTask(t.Context(), b.Project.ID, "", "github-quota-probe", "probe", b.Binding, compute.TaskSpec{Argv: []string{"true"}, Limits: compute.TaskLimits{CPUs: 1, MemoryMiB: 64, MaxLogBytes: 1024}})
	if compute.ErrorCode(err) != "capacity_exceeded" {
		t.Fatalf("task history quota was not exhausted: %v", err)
	}
	counts := func() []int {
		var values []int
		for _, table := range []string{"project_tasks", "task_attempts", "task_requests", "operations", "project_requests"} {
			var n int
			if err := f.service.store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
				t.Fatal(err)
			}
			values = append(values, n)
		}
		return values
	}
	before := counts()
	for range 3 {
		result, err := f.service.ProjectGitHub(t.Context(), b.Project.ID, "refresh", compute.ProjectGitHubRequest{Binding: b.Binding, Revision: 1})
		if err != nil || result.(compute.ProjectGitHubResult).Binding != b.Binding {
			t.Fatalf("refresh = %#v %v", result, err)
		}
	}
	after := counts()
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("read allocated durable work: %v -> %v", before, after)
		}
	}
}

func TestProjectGitHubRejectsChangedReviewAndGuestWithoutPersisting(t *testing.T) {
	f, b := githubProject(t)
	wrong := compute.ProjectGitHubRequest{Binding: b.Binding, Repository: "example/project", RepositoryID: "different-repo", AccountID: "user-node"}
	if _, err := f.service.ProjectGitHub(t.Context(), b.Project.ID, "associate", wrong); compute.ErrorCode(err) != "github_connection_changed" {
		t.Fatalf("different reviewed identity accepted: %v", err)
	}
	f.mu.Lock()
	f.file = func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.health.InstanceID = "replacement-instance"
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(githubObservation())
	}
	f.mu.Unlock()
	wrong.RepositoryID = "repo-node"
	if _, err := f.service.ProjectGitHub(t.Context(), b.Project.ID, "associate", wrong); compute.ErrorCode(err) != "binding_changed" {
		t.Fatalf("late old-guest response accepted: %v", err)
	}
	state, err := f.service.ProjectGitHubState(t.Context(), b.Project.ID, b.Binding.HostID)
	if err != nil || state.Revision != 0 || state.Association != nil {
		t.Fatalf("failed review wrote settings: %#v %v", state, err)
	}
}

func TestProjectGitHubRejectsLateResponseAfterReassociation(t *testing.T) {
	f, b := githubProject(t)
	state := associateGitHub(t, f, b)
	started, finish := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.file = func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish
		_ = json.NewEncoder(w).Encode(githubObservation())
	}
	f.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := f.service.ProjectGitHub(context.Background(), b.Project.ID, "refresh", compute.ProjectGitHubRequest{Binding: b.Binding, Revision: 1})
		done <- err
	}()
	<-started
	_, err := f.service.store.setProjectGitHub(t.Context(), b.Project.ID, 1, state.Association)
	close(finish)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; compute.ErrorCode(err) != "github_association_changed" {
		t.Fatalf("late result was adopted after config changed: %v", err)
	}
}

func TestProjectGitHubDoesNotAdoptReplacementVMOrIncompatibleGuest(t *testing.T) {
	f, b := githubProject(t)
	state := associateGitHub(t, f, b)
	copy := *state.Association
	copy.MachineID = "original-removed-machine"
	if _, err := f.service.store.setProjectGitHub(t.Context(), b.Project.ID, 1, &copy); err != nil {
		t.Fatal(err)
	}
	before := f.calls.Load()
	if _, err := f.service.ProjectGitHub(t.Context(), b.Project.ID, "refresh", compute.ProjectGitHubRequest{Binding: b.Binding, Revision: 2}); compute.ErrorCode(err) != "github_connection_changed" || f.calls.Load() != before {
		t.Fatalf("replacement VM reused credentials: %v", err)
	}
	old := newProjectFixture(t)
	bound := old.registered(t)
	if _, err := old.service.ProjectGitHub(t.Context(), bound.Project.ID, "git", compute.ProjectGitHubRequest{Binding: bound.Binding}); compute.ErrorCode(err) != "unsupported" {
		t.Fatalf("missing image capability accepted: %v", err)
	}
}

func TestProjectGitHubDoesNotExposeGuestProviderDiagnostics(t *testing.T) {
	f, b := githubProject(t)
	f.mu.Lock()
	f.file = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(compute.Error{Code: "github_forbidden", Message: "unsafe-provider-credential"})
	}
	f.mu.Unlock()
	_, err := f.service.ProjectGitHub(t.Context(), b.Project.ID, "review", compute.ProjectGitHubRequest{Binding: b.Binding, Repository: "example/project"})
	if compute.ErrorCode(err) != "github_forbidden" || strings.Contains(err.Error(), "unsafe-provider-credential") {
		t.Fatalf("provider diagnostics were not sanitized: %v", err)
	}
}
