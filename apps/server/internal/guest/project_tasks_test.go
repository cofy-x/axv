//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"golang.org/x/sys/unix"
)

// Fake controls exercise receipt/crash/log lifecycle without claiming kernel
// enforcement. Native guest acceptance separately checks real delegated cgroups.
type fakeTaskGroups struct {
	mu     sync.Mutex
	groups map[string]*fakeTaskGroup
	kills  map[string]int
}

type fakeTaskGroup struct {
	owner  *fakeTaskGroups
	name   string
	cmd    *exec.Cmd
	reason string
}

func (m *fakeTaskGroups) Create(name string, _ protocol.TaskLimits) (taskGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.groups[name]; exists {
		return nil, os.ErrExist
	}
	g := &fakeTaskGroup{owner: m, name: name}
	m.groups[name] = g
	return g, nil
}
func (m *fakeTaskGroups) Existing(name string) (taskGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := m.groups[name]
	if g == nil {
		return nil, os.ErrNotExist
	}
	return g, nil
}
func (g *fakeTaskGroup) Configure(cmd *exec.Cmd) error {
	g.cmd = cmd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func (g *fakeTaskGroup) Kill() error {
	g.owner.mu.Lock()
	g.owner.kills[g.name]++
	g.owner.mu.Unlock()
	if g.cmd != nil && g.cmd.Process != nil {
		if err := syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
	}
	return nil
}
func (g *fakeTaskGroup) Events() (string, bool, error) {
	g.owner.mu.Lock()
	defer g.owner.mu.Unlock()
	return g.reason, false, nil
}
func (g *fakeTaskGroup) Remove() error {
	g.owner.mu.Lock()
	defer g.owner.mu.Unlock()
	delete(g.owner.groups, g.name)
	return nil
}
func (g *fakeTaskGroup) Close() error { return nil }

func taskFixture(t *testing.T) (*service, protocol.ProjectFileScope, *fakeTaskGroups) {
	t.Helper()
	s := testService(t, "project-files", "project-tasks")
	parent := t.TempDir()
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "tasks")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	owner := struct {
		Version   int    `json:"version"`
		MachineID string `json:"machineId"`
	}{1, s.config.MachineID}
	data, _ := json.Marshal(owner)
	if err := atomicfile.Write(filepath.Join(parent, "tasks.owner"), data, 0644); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(guestTaskJournal{Version: 1, MachineID: s.config.MachineID, Attempts: map[string]*guestTaskRecord{}})
	if err := atomicfile.Write(filepath.Join(root, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	groups := &fakeTaskGroups{groups: map[string]*fakeTaskGroup{}, kills: map[string]int{}}
	var err error
	s.tasks, err = openProjectTasks(s, root, groups, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	return s, projectFileScope(t, s, ""), groups
}

func taskSubmission(scope protocol.ProjectFileScope, suffix, script string) protocol.TaskSubmit {
	return protocol.TaskSubmit{ProjectFileScope: scope, TaskID: "task-" + suffix, AttemptID: "attempt-" + suffix, Spec: protocol.TaskSpec{Argv: []string{"/bin/sh", "-c", script}, Limits: protocol.TaskLimits{TimeoutSeconds: 5, CPUs: 1, MemoryMiB: 64, MaxProcesses: 8, MaxLogBytes: 1 << 20}}}
}

func waitTask(t *testing.T, s *service, id protocol.TaskIdentity, predicate func(protocol.TaskExecution) bool) protocol.TaskExecution {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		s.tasks.mu.Lock()
		record, err := s.tasks.inspectLocked(id)
		var result protocol.TaskExecution
		if err == nil {
			result = copyTaskExecution(record)
		}
		s.tasks.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if predicate(result) {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatalf("task condition timed out: %+v", result)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func taskHTTP(t *testing.T, s *service, action string, input any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/project-tasks/"+action, strings.NewReader(string(data)))
	r.Header.Set("Authorization", "Bearer "+s.config.Token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}

func TestProjectTasksDurableReceiptAndByteExactLogs(t *testing.T) {
	s, scope, _ := taskFixture(t)
	in := taskSubmission(scope, "durable", `printf x >> launches; printf '\377A\000B'; printf ERR >&2`)
	first, err := s.tasks.submit(in)
	if err != nil || first.Status != "accepted" {
		t.Fatalf("acceptance = %+v, %v", first, err)
	}
	if _, err := s.tasks.submit(in); err != nil {
		t.Fatal(err)
	}
	changed := in
	changed.Spec.Argv = []string{"/bin/false"}
	w := taskHTTP(t, s, "submit", changed)
	assertProblem(t, w, 409, "idempotency_conflict")
	final := waitTask(t, s, first.TaskIdentity, func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	if final.Status != "succeeded" || final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("final receipt = %+v", final)
	}
	log, err := s.tasks.logs(protocol.TaskLogRequest{TaskIdentity: first.TaskIdentity, Limit: 3})
	if err != nil || string(log.Data) != "\xffA\x00" || log.NextOffset != 3 {
		t.Fatalf("first exact log page = %+v, %v", log, err)
	}
	log, err = s.tasks.logs(protocol.TaskLogRequest{TaskIdentity: first.TaskIdentity, Offset: log.NextOffset})
	if err != nil || string(log.Data) != "BERR" || log.NextOffset != 7 {
		t.Fatalf("second exact log page = %+v, %v", log, err)
	}
	data, err := os.ReadFile(filepath.Join(s.config.ProjectRoot, "launches"))
	if err != nil || string(data) != "x" {
		t.Fatalf("duplicate receipt repeated execution: %q, %v", data, err)
	}
	root := s.tasks.root
	s.tasks.close()
	replacement := newService(context.Background(), s.config, s.bootID, []string{"project-tasks"}, func() error { return nil }, nil)
	t.Cleanup(replacement.close)
	replacement.tasks, err = openProjectTasks(replacement, root, &fakeTaskGroups{groups: map[string]*fakeTaskGroup{}, kills: map[string]int{}}, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	if w := taskHTTP(t, replacement, "inspect", first.TaskIdentity); w.Code != 200 {
		t.Fatalf("historical receipt unavailable after executor restart: %d: %s", w.Code, w.Body)
	}
	assertProblem(t, taskHTTP(t, replacement, "cancel", first.TaskIdentity), 409, "binding_changed")
	if _, err := replacement.tasks.submit(in); err != nil {
		t.Fatalf("durable receipt replay requires no relaunch: %v", err)
	}
}

func TestProjectTasksCancellationAddressesOnlyOriginalAttempt(t *testing.T) {
	s, scope, groups := taskFixture(t)
	first := taskSubmission(scope, "cancel-one", "printf ready; sleep 30")
	second := taskSubmission(scope, "cancel-two", "printf ready; sleep 30")
	for _, in := range []protocol.TaskSubmit{first, second} {
		if _, err := s.tasks.submit(in); err != nil {
			t.Fatal(err)
		}
		waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return e.Status == "running" && e.LogBytes > 0 })
	}
	stale := taskSubmitIdentity(first)
	stale.InstanceID += "-old"
	assertProblem(t, taskHTTP(t, s, "cancel", stale), 409, "binding_changed")
	if _, err := s.tasks.cancel(taskSubmitIdentity(first)); err != nil {
		t.Fatal(err)
	}
	final := waitTask(t, s, taskSubmitIdentity(first), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	if final.Status != "cancelled" {
		t.Fatalf("cancelled receipt = %+v", final)
	}
	waitTask(t, s, taskSubmitIdentity(second), func(e protocol.TaskExecution) bool { return e.Status == "running" })
	groups.mu.Lock()
	killedSecond := groups.kills[taskGroupName(second.AttemptID)]
	groups.mu.Unlock()
	if killedSecond != 0 {
		t.Fatal("cancelling one task killed another task's group")
	}
	if _, err := s.tasks.cancel(taskSubmitIdentity(second)); err != nil {
		t.Fatal(err)
	}
	waitTask(t, s, taskSubmitIdentity(second), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
}

func TestProjectTaskLogTimeoutAndResourceFailures(t *testing.T) {
	for _, test := range []struct{ name, script, expected string }{
		{"log", "head -c 2048 /dev/zero", "log_limit"},
		{"timeout", "sleep 30", "timeout"},
		{"memory", "printf ready; sleep 30", "memory_limit"},
		{"process", "printf ready; sleep 30", "process_limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, scope, groups := taskFixture(t)
			in := taskSubmission(scope, "limits-"+test.name, test.script)
			if test.name == "log" {
				in.Spec.Limits.MaxLogBytes = 1024
			}
			if test.name == "timeout" {
				in.Spec.Limits.TimeoutSeconds = 1
			}
			if _, err := s.tasks.submit(in); err != nil {
				t.Fatal(err)
			}
			if test.name == "memory" || test.name == "process" {
				waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return e.Status == "running" && e.LogBytes > 0 })
				groups.mu.Lock()
				groups.groups[taskGroupName(in.AttemptID)].reason = test.expected
				groups.mu.Unlock()
			}
			final := waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
			if final.Status != "failed" || final.Reason != test.expected {
				t.Fatalf("resource failure = %+v", final)
			}
			if test.name == "log" && (!final.LogLimited || final.LogBytes != 1024) {
				t.Fatalf("log was not bounded exactly: %+v", final)
			}
		})
	}
}

func TestProjectTasksRecoverAcceptedAndRunningWithoutRelaunch(t *testing.T) {
	for _, status := range []string{"accepted", "running"} {
		t.Run(status, func(t *testing.T) {
			s, scope, groups := taskFixture(t)
			root := s.tasks.root
			s.tasks.close()
			in := taskSubmission(scope, "recover-"+status, "printf bad >> relaunched")
			record := &guestTaskRecord{TaskExecution: protocol.TaskExecution{TaskIdentity: taskSubmitIdentity(in), Spec: in.Spec, Status: status, CreatedAt: time.Now().UTC()}}
			if status == "running" {
				record.LaunchIntent, record.LogReady = true, true
				if err := os.WriteFile(filepath.Join(root, in.AttemptID+".log"), []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
				record.LogBytes = 8
			}
			journal := guestTaskJournal{Version: 1, MachineID: s.config.MachineID, Attempts: map[string]*guestTaskRecord{in.AttemptID: record}}
			data, _ := json.Marshal(journal)
			if err := atomicfile.Write(filepath.Join(root, "state.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			s.tasks, err = openProjectTasks(s, root, groups, uint32(os.Geteuid()))
			if err != nil {
				t.Fatal(err)
			}
			actual := waitTask(t, s, record.TaskIdentity, func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
			if actual.Status != "interrupted" {
				t.Fatalf("recovery invented an outcome: %+v", actual)
			}
			if _, err := s.tasks.submit(in); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(s.config.ProjectRoot, "relaunched")); !os.IsNotExist(err) {
				t.Fatalf("recovery relaunched old work: %v", err)
			}
		})
	}
}

func TestProjectTaskStateErrorsAreVisibleWithoutReset(t *testing.T) {
	for _, change := range []string{"missing", "invalid", "owner", "symlink"} {
		t.Run(change, func(t *testing.T) {
			s, _, groups := taskFixture(t)
			root := s.tasks.root
			s.tasks.close()
			name := filepath.Join(root, "state.json")
			switch change {
			case "missing":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
			case "invalid":
				if err := os.WriteFile(name, []byte("not JSON"), 0600); err != nil {
					t.Fatal(err)
				}
			case "owner":
				if err := os.WriteFile(filepath.Join(filepath.Dir(root), "tasks.owner"), []byte(`{"version":1,"machineId":"other"}`), 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), name); err != nil {
					t.Fatal(err)
				}
			}
			if tasks, err := openProjectTasks(s, root, groups, uint32(os.Geteuid())); err == nil {
				tasks.close()
				t.Fatal("invalid established state was adopted or reset")
			}
		})
	}
	s, scope, groups := taskFixture(t)
	s.tasks.publish = func(string, []byte, os.FileMode) error { return syscall.ENOSPC }
	assertProblem(t, taskHTTP(t, s, "submit", taskSubmission(scope, "disk-full", "printf bad")), 507, "disk_full")
	groups.mu.Lock()
	defer groups.mu.Unlock()
	if len(groups.groups) != 0 {
		t.Fatal("task launched despite failed durable acceptance")
	}
}

func TestProjectTaskStateRejectsFIFOsWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"journal", "log", "owner"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := taskFixture(t)
			s.tasks.close()
			name := filepath.Join(s.tasks.root, "state.json")
			mode := uint32(0600)
			if kind == "log" {
				name = filepath.Join(s.tasks.root, "attempt-special.log")
			} else if kind == "owner" {
				name, mode = filepath.Join(filepath.Dir(s.tasks.root), "tasks.owner"), 0644
			}
			if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(name, mode); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if kind == "owner" {
					done <- checkTaskState(s.tasks.root, s.config.MachineID, uint32(os.Geteuid()))
					return
				}
				file, err := openTaskRegular(name, os.O_RDONLY)
				if file != nil {
					file.Close()
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("task state accepted a FIFO as a regular file")
				}
			case <-time.After(time.Second):
				// Release a regressed blocking reader before failing the test.
				writer, err := os.OpenFile(name, os.O_WRONLY|unix.O_NONBLOCK, 0)
				if err == nil {
					writer.Close()
				}
				t.Fatal("invalid FIFO state blocked validation instead of returning an error")
			}
		})
	}
}

func TestProjectTaskRequestEnvelopeAndBindingValidation(t *testing.T) {
	s, scope, _ := taskFixture(t)
	in := taskSubmission(scope, "escaped", "")
	in.Spec.Argv = []string{"/bin/echo", strings.Repeat("\x01", (32<<10)-len("/bin/echo"))}
	w := taskHTTP(t, s, "submit", in)
	if w.Code != 200 {
		t.Fatalf("max escaped argv rejected: %d: %s", w.Code, w.Body)
	}
	waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	for index, change := range []func(*protocol.TaskSubmit){
		func(in *protocol.TaskSubmit) { in.BootID += "old" },
		func(in *protocol.TaskSubmit) { in.InstanceID += "old" },
		func(in *protocol.TaskSubmit) { in.RootIdentity += "old" },
		func(in *protocol.TaskSubmit) { in.Spec.Directory = "../outside" },
		func(in *protocol.TaskSubmit) { in.AttemptID = "../outside" },
	} {
		request := taskSubmission(scope, fmt.Sprintf("reject-%d", index), "printf bad")
		change(&request)
		if w := taskHTTP(t, s, "submit", request); w.Code < 400 {
			t.Fatalf("invalid task binding/path accepted: %s", w.Body)
		}
	}
}

func TestDelegatedTaskPathAndEventParsing(t *testing.T) {
	valid := "0::/system.slice/axv-guest.service/supervisor\n"
	if path, err := delegatedTaskPath(valid); err != nil || path != "system.slice/axv-guest.service" {
		t.Fatalf("valid delegated path = %q, %v", path, err)
	}
	for _, value := range []string{"0::/", "0::/system.slice/other.service/supervisor", "0::/system.slice/axv-guest.service", "0::/a/../axv-guest.service/supervisor", valid + valid} {
		if _, err := delegatedTaskPath(value); err == nil {
			t.Fatalf("unsafe cgroup parent accepted: %q", value)
		}
	}
	if count, err := taskEventCount("oom 0\noom_kill 3\n", "oom_kill"); err != nil || count != 3 {
		t.Fatalf("events = %d, %v", count, err)
	}
	if _, err := taskEventCount("max bad", "max"); err == nil {
		t.Fatal("invalid cgroup event accepted")
	}
}

func TestProjectTasksBoundRetainedMetadataAndLogReservations(t *testing.T) {
	for _, kind := range []string{"count", "metadata", "logs"} {
		t.Run(kind, func(t *testing.T) {
			s, scope, _ := taskFixture(t)
			count := 32
			if kind == "count" {
				count = maxGuestTaskRecords
			}
			if kind == "metadata" {
				count = 40
			}
			s.tasks.mu.Lock()
			for i := 0; i < count; i++ {
				in := taskSubmission(scope, fmt.Sprintf("retained-%04d", i), "true")
				if kind == "logs" {
					in.Spec.Limits.MaxLogBytes = 8 << 20
				}
				if kind == "metadata" {
					in.Spec.Argv = []string{"/bin/echo", strings.Repeat("\x01", (32<<10)-len("/bin/echo"))}
				}
				now := time.Now().UTC()
				s.tasks.journal.Attempts[in.AttemptID] = &guestTaskRecord{TaskExecution: protocol.TaskExecution{TaskIdentity: taskSubmitIdentity(in), Spec: in.Spec, Status: "failed", CreatedAt: now, FinishedAt: &now}}
			}
			s.tasks.mu.Unlock()
			assertProblem(t, taskHTTP(t, s, "submit", taskSubmission(scope, "beyond-quota", "printf bad")), 503, "task_limit")
			if s.tasks.fault != nil {
				t.Fatalf("capacity rejection poisoned existing task history: %v", s.tasks.fault)
			}
		})
	}
}

func TestProjectTaskActiveLimitAndRequestDetachment(t *testing.T) {
	s, scope, _ := taskFixture(t)
	for i := 0; i < 8; i++ {
		in := taskSubmission(scope, fmt.Sprintf("concurrent-%d", i), "printf ready; sleep 30")
		if _, err := s.tasks.submit(in); err != nil {
			t.Fatal(err)
		}
	}
	assertProblem(t, taskHTTP(t, s, "submit", taskSubmission(scope, "concurrent-nine", "printf bad")), 503, "task_limit")
	s.tasks.close()
	// A separate guest fixture proves cancelling the submit request after its
	// receipt does not become an instruction to stop the accepted command.
	s, scope, _ = taskFixture(t)
	in := taskSubmission(scope, "request-detach", "sleep 0.1; printf survived")
	data, _ := json.Marshal(in)
	ctx, cancel := context.WithCancel(t.Context())
	r := httptest.NewRequest("POST", "/v1/project-tasks/submit", strings.NewReader(string(data))).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+s.config.Token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	cancel()
	if w.Code != 200 {
		t.Fatalf("acceptance: %d: %s", w.Code, w.Body)
	}
	final := waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	if final.Status != "succeeded" || final.LogBytes != 8 {
		t.Fatalf("request cancellation stopped guest task: %+v", final)
	}
}

func TestProjectTaskCapabilityReflectsActualStateSupport(t *testing.T) {
	s := testService(t, "project-tasks")
	s.taskError = errors.New("delegated memory controller unavailable")
	w := guestRequest(s, "GET", "/v1/health", "")
	var health protocol.Health
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(health.Capabilities, ","), "project-tasks") || !strings.Contains(health.Reason, "memory controller") {
		t.Fatalf("unusable task capability advertised: %s", w.Body)
	}
	assertProblem(t, taskHTTP(t, s, "inspect", protocol.TaskIdentity{}), 501, "unsupported")
	s, _, _ = taskFixture(t)
	s.tasks.mu.Lock()
	s.tasks.fault = errors.New("sync failed")
	s.tasks.mu.Unlock()
	w = guestRequest(s, "GET", "/v1/health", "")
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(health.Capabilities, ","), "project-tasks") || !strings.Contains(health.Reason, "sync failed") {
		t.Fatalf("failed durable state still advertised: %s", w.Body)
	}
}

func TestTaskImageProvisioningPreservesEstablishedState(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("image provisioning fixture requires Python 3")
	}
	source, err := os.ReadFile("../../../../images/guest/prepare-tasks.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"unchanged", "missing", "wrong-machine", "missing-owner", "symlink"} {
		t.Run(change, func(t *testing.T) {
			base := t.TempDir()
			parent := filepath.Join(base, "state")
			root := filepath.Join(parent, "tasks")
			if err := os.Mkdir(parent, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(parent, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(base, "guest.json")
			if err := os.WriteFile(config, []byte(`{"machineId":"vm-fixture"}`), 0600); err != nil {
				t.Fatal(err)
			}
			// Relocate only owned fixture paths and owner IDs. The same production
			// first-use, durability and refusal logic runs under the test identity.
			code := strings.ReplaceAll(string(source), "Path('/var/lib/axv')", "Path("+strconv.Quote(parent)+")")
			code = strings.ReplaceAll(code, "Path('/etc/axv/guest.json')", "Path("+strconv.Quote(config)+")")
			uid := strconv.Itoa(os.Geteuid())
			code = strings.ReplaceAll(code, "os.geteuid() != 0", "os.geteuid() != "+uid)
			code = strings.ReplaceAll(code, "require(parent, 0,", "require(parent, "+uid+",")
			code = strings.ReplaceAll(code, "require(root, 1000,", "require(root, "+uid+",")
			code = strings.ReplaceAll(code, "require(marker, 0,", "require(marker, "+uid+",")
			code = strings.ReplaceAll(code, "require(root / 'state.json', 1000,", "require(root / 'state.json', "+uid+",")
			code = strings.ReplaceAll(code, "0o600, 1000)", "0o600, "+uid+")")
			code = strings.ReplaceAll(code, "0o644, 0)", "0o644, "+uid+")")
			code = strings.ReplaceAll(code, "owner, owner)", "owner, "+strconv.Itoa(os.Getegid())+")")
			script := filepath.Join(base, "prepare-tasks.py")
			if err := os.WriteFile(script, []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(python, script).CombinedOutput(); err != nil {
				t.Fatalf("first initialization: %v: %s", err, output)
			}
			journal := filepath.Join(root, "state.json")
			original, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "missing":
				if err := os.Remove(journal); err != nil {
					t.Fatal(err)
				}
			case "wrong-machine":
				if err := os.WriteFile(config, []byte(`{"machineId":"another-machine"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-owner":
				if err := os.Remove(filepath.Join(parent, "tasks.owner")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(journal); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(base, "nonexistent"), journal); err != nil {
					t.Fatal(err)
				}
			}
			output, err := exec.Command(python, script).CombinedOutput()
			if change == "unchanged" {
				if err != nil {
					t.Fatalf("repeat initialization: %v: %s", err, output)
				}
				actual, _ := os.ReadFile(journal)
				if string(actual) != string(original) {
					t.Fatal("repeat initialization changed task state")
				}
			} else if err == nil {
				t.Fatalf("provisioning silently accepted/reset %s established state", change)
			}
			if change == "missing" {
				if _, err := os.Stat(journal); !os.IsNotExist(err) {
					t.Fatalf("missing established state was reseeded: %v", err)
				}
			}
		})
	}
}

func TestProjectTaskWatchdogDoesNotWaitForReceiptSync(t *testing.T) {
	s, scope, groups := taskFixture(t)
	in := taskSubmission(scope, "blocked-receipt", "sleep 30")
	in.Spec.Limits.TimeoutSeconds = 1
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.tasks.publish = func(name string, data []byte, mode os.FileMode) error {
		if strings.Contains(string(data), `"status":"running"`) {
			once.Do(func() { close(entered); <-release })
		}
		return atomicfile.Write(name, data, mode)
	}
	if _, err := s.tasks.submit(in); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("running receipt was not reached")
	}
	// The state mutex is held by the simulated fsync. Enforcement must still
	// reach only this attempt's cgroup while that durable write is stalled.
	deadline := time.Now().Add(2500 * time.Millisecond)
	killed := false
	for time.Now().Before(deadline) {
		groups.mu.Lock()
		killed = groups.kills[taskGroupName(in.AttemptID)] > 0
		groups.mu.Unlock()
		if killed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if !killed {
		t.Fatal("journal sync blocked deadline enforcement")
	}
	final := waitTask(t, s, taskSubmitIdentity(in), func(e protocol.TaskExecution) bool { return guestTaskFinal(e.Status) })
	if final.Status != "failed" || final.Reason != "timeout" {
		t.Fatalf("deadline result = %+v", final)
	}
}

func TestTaskCgroupControlsStayOnOriginalDirectoryHandle(t *testing.T) {
	fixture := t.TempDir()
	root, err := os.OpenRoot(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	manager := &linuxTaskGroups{root: root}
	name, other := taskGroupName("original-attempt"), taskGroupName("another-attempt")
	for _, child := range []string{name, other} {
		if err := os.Mkdir(filepath.Join(fixture, child), 0700); err != nil {
			t.Fatal(err)
		}
		for file, value := range map[string]string{"cgroup.kill": "0", "cgroup.events": "populated 1\n", "memory.events": "oom_kill 0\n", "pids.events": "max 0\n"} {
			if err := os.WriteFile(filepath.Join(fixture, child, file), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	group, err := manager.Existing(name)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	cmd := exec.Command("/bin/true")
	if err := group.Configure(cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.UseCgroupFD || cmd.SysProcAttr.Pdeathsig != 0 {
		t.Fatal("task launch must use atomic cgroup placement without creating-thread lifetime signals")
	}
	if err := group.Kill(); err != nil {
		t.Fatal(err)
	}
	for child, expected := range map[string]string{name: "1", other: "0"} {
		data, err := os.ReadFile(filepath.Join(fixture, child, "cgroup.kill"))
		if err != nil || string(data) != expected {
			t.Fatalf("cancellation changed another cgroup: %q %v", data, err)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture, name, "memory.events"), []byte("oom_kill 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if reason, populated, err := group.Events(); err != nil || reason != "memory_limit" || !populated {
		t.Fatalf("memory event: %q %v %v", reason, populated, err)
	}
	if err := os.WriteFile(filepath.Join(fixture, name, "memory.events"), []byte("oom_kill 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, name, "pids.events"), []byte("max 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if reason, _, err := group.Events(); err != nil || reason != "process_limit" {
		t.Fatalf("pids event: %q %v", reason, err)
	}
}

func TestTaskCgroupCleanupRemovesOnlyOriginalNestedDirectories(t *testing.T) {
	fixture := t.TempDir()
	root, err := os.OpenRoot(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	manager := &linuxTaskGroups{root: root}
	name, sibling := taskGroupName("cleanup-original"), taskGroupName("cleanup-sibling")
	for _, directory := range []string{filepath.Join(name, "tool", "worker"), filepath.Join(name, "another"), sibling} {
		if err := os.MkdirAll(filepath.Join(fixture, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := filepath.Join(fixture, sibling, "unrelated")
	if err := os.WriteFile(sentinel, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	group, err := manager.Existing(name)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	if err := group.Remove(); err != nil {
		t.Fatalf("empty nested task groups blocked cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture, name)); !os.IsNotExist(err) {
		t.Fatalf("original attempt group was not removed: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "retained" {
		t.Fatalf("cleanup touched another attempt: %q, %v", data, err)
	}
}

func TestTaskCgroupCleanupFailsWithoutDeletingUnknownContents(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "count", "depth"} {
		t.Run(kind, func(t *testing.T) {
			fixture := t.TempDir()
			child := filepath.Join(fixture, "child")
			if err := os.Mkdir(child, 0700); err != nil {
				t.Fatal(err)
			}
			remaining := maxTaskGroupDescendants
			sentinel := filepath.Join(child, "unknown")
			switch kind {
			case "file":
				if err := os.WriteFile(sentinel, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), sentinel); err != nil {
					t.Fatal(err)
				}
			case "count":
				remaining = 1
				if err := os.Mkdir(filepath.Join(fixture, "another-child"), 0700); err != nil {
					t.Fatal(err)
				}
			case "depth":
				for i := 0; i < maxTaskGroupDepth; i++ {
					child = filepath.Join(child, "child")
					if err := os.Mkdir(child, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			root, err := os.OpenRoot(fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := removeTaskGroupChildren(root, 0, &remaining); err == nil {
				t.Fatal("unsafe or unbounded group cleanup was accepted")
			}
			if kind == "file" || kind == "symlink" {
				if _, err := os.Lstat(sentinel); err != nil {
					t.Fatalf("cleanup removed unknown contents: %v", err)
				}
			}
		})
	}
}
