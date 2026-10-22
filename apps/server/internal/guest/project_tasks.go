package guest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

const maxGuestTaskRecords = 256
const maxGuestTaskJournal = 8 << 20
const maxGuestTaskLogs = 256 << 20
const guestTaskFinalHeadroom = 32 << 10

var guestTaskID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

type taskGroup interface {
	Configure(*exec.Cmd) error
	Kill() error
	Events() (string, bool, error)
	Remove() error
	Close() error
}

type taskGroups interface {
	Create(string, protocol.TaskLimits) (taskGroup, error)
	Existing(string) (taskGroup, error)
}

type guestTaskRecord struct {
	protocol.TaskExecution
	LogReady     bool   `json:"logReady"`
	LaunchIntent bool   `json:"launchIntent"`
	StopReason   string `json:"stopReason,omitempty"`
}

type guestTaskJournal struct {
	Version   int                         `json:"version"`
	MachineID string                      `json:"machineId"`
	Attempts  map[string]*guestTaskRecord `json:"attempts"`
}

type guestTaskRuntime struct {
	wake     chan struct{}
	done     chan struct{}
	timedOut atomic.Bool
}

type projectTasks struct {
	service   *service
	mu        sync.Mutex
	root      string
	journal   guestTaskJournal
	groups    taskGroups
	lock      *os.File
	active    map[string]*guestTaskRuntime
	closed    bool
	fault     error
	closeOnce sync.Once
	wg        sync.WaitGroup
	// Tests inject publication failures without weakening the production path.
	publish func(string, []byte, os.FileMode) error
}

func guestTaskFinal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled" || status == "interrupted"
}

func taskGroupName(attempt string) string { return "attempt-" + projectDigest([]byte(attempt)) }

func validateGuestTaskIdentity(id protocol.TaskIdentity) error {
	if !guestTaskID.MatchString(id.TaskID) || !guestTaskID.MatchString(id.AttemptID) || id.BootID == "" || id.InstanceID == "" || id.RootIdentity == "" {
		return projectProblem(400, "invalid_request", "A complete original task, attempt and guest identity is required.")
	}
	for _, value := range []string{id.BootID, id.InstanceID, id.RootIdentity} {
		if len(value) > 128 || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return projectProblem(400, "invalid_request", "Task environment identities must be bounded UTF-8 without control characters.")
		}
	}
	_, err := projectRelative(id.Root, true)
	return err
}

func validateGuestTaskSpec(spec protocol.TaskSpec) error {
	if _, err := projectRelative(spec.Directory, true); err != nil {
		return err
	}
	if spec.Initialization != nil {
		if len(spec.Argv) != 0 || spec.Directory != "" || spec.Initialization.Validate() != nil {
			return projectProblem(400, "invalid_request", "Typed initialization cannot contain an ordinary command or starting directory.")
		}
	} else if len(spec.Argv) == 0 || len(spec.Argv) > 128 || spec.Argv[0] == "" {
		return projectProblem(400, "invalid_request", "Supply 1–128 task arguments and a nonempty executable.")
	}
	size := 0
	for _, arg := range spec.Argv {
		size += len(arg)
		if !utf8.ValidString(arg) || strings.ContainsRune(arg, 0) {
			return projectProblem(400, "invalid_request", "Task arguments require valid UTF-8 without NUL.")
		}
	}
	l := spec.Limits
	if size > 32<<10 || l.TimeoutSeconds < 1 || l.TimeoutSeconds > 86400 || l.CPUs < 1 || l.CPUs > 128 || l.MemoryMiB < 64 || l.MemoryMiB > 1<<20 || l.MaxProcesses < 2 || l.MaxProcesses > 256 || l.MaxLogBytes < 1024 || l.MaxLogBytes > 8<<20 {
		return projectProblem(400, "invalid_request", "Task arguments or explicit resource limits are outside their supported range.")
	}
	return nil
}

func taskSubmitIdentity(in protocol.TaskSubmit) protocol.TaskIdentity {
	return protocol.TaskIdentity{ProjectFileScope: in.ProjectFileScope, TaskID: in.TaskID, AttemptID: in.AttemptID}
}

func copyTaskExecution(record *guestTaskRecord) protocol.TaskExecution {
	result := record.TaskExecution
	result.Spec.Argv = append([]string{}, result.Spec.Argv...)
	if result.Spec.Initialization != nil {
		intent := *result.Spec.Initialization
		result.Spec.Initialization = &intent
	}
	if result.CreatedRoot != nil {
		root := *result.CreatedRoot
		result.CreatedRoot = &root
	}
	return result
}

func (t *projectTasks) persistLocked() error {
	data, err := json.Marshal(t.journal)
	if err == nil && len(data)+1 > maxGuestTaskJournal {
		err = errors.New("guest task journal exceeds its eight-MiB metadata budget")
	}
	if err == nil {
		err = t.publish(filepath.Join(t.root, "state.json"), append(data, '\n'), 0600)
	}
	if err != nil {
		t.fault = fmt.Errorf("persist guest task state: %w", err)
	}
	return err
}

func openProjectTasks(s *service, root string, groups taskGroups, markerUID uint32) (*projectTasks, error) {
	if err := checkTaskState(root, s.config.MachineID, markerUID); err != nil {
		return nil, err
	}
	lock, err := filelock.TryLock(filepath.Join(root, ".lock"))
	if err != nil {
		return nil, err
	}
	t := &projectTasks{service: s, root: root, groups: groups, lock: lock, active: map[string]*guestTaskRuntime{}, publish: atomicfile.Write}
	data, err := readTaskState(filepath.Join(root, "state.json"), maxGuestTaskJournal)
	if err == nil && (!jsonunicode.Valid(data) || len(data) == 0) {
		err = errors.New("guest task journal is not valid Unicode JSON")
	}
	if err == nil {
		err = decodeTaskState(data, &t.journal)
	}
	if err == nil && (t.journal.Version != 1 || t.journal.MachineID != s.config.MachineID || t.journal.Attempts == nil || len(t.journal.Attempts) > maxGuestTaskRecords) {
		err = errors.New("guest task journal has an unknown format, owner or capacity")
	}
	if err == nil {
		err = t.recover()
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	return t, nil
}

func (t *projectTasks) recover() error {
	changed := false
	reservedLogs := int64(0)
	entries, err := os.ReadDir(t.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "state.json" || entry.Name() == ".lock" || strings.HasPrefix(entry.Name(), ".axv-write-") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".log")
		if id == entry.Name() || t.journal.Attempts[id] == nil {
			return errors.New("guest task state contains unrecorded files; refusing to adopt or delete them")
		}
	}
	for id, record := range t.journal.Attempts {
		if record == nil || id != record.AttemptID || validateGuestTaskIdentity(record.TaskIdentity) != nil || validateGuestTaskSpec(record.Spec) != nil || record.CreatedAt.IsZero() || record.LogBytes < 0 || record.LogBytes > record.Spec.Limits.MaxLogBytes || record.Reason != boundedTaskReason(record.Reason) || (!guestTaskFinal(record.Status) && record.Status != "accepted" && record.Status != "running") || (record.Status == "running" && (!record.LogReady || !record.LaunchIntent)) || (guestTaskFinal(record.Status) && record.FinishedAt == nil) {
			return errors.New("guest task journal contains an invalid attempt; refusing to reset it")
		}
		if err := validateInitializationResult(record); err != nil {
			return err
		}
		reservedLogs += record.Spec.Limits.MaxLogBytes
		if reservedLogs > maxGuestTaskLogs {
			return errors.New("guest task journal exceeds its retained log quota")
		}
		log, err := t.openLog(record, false)
		if err == nil {
			info, statErr := log.Stat()
			log.Close()
			if statErr != nil || info.Size() < record.LogBytes || info.Size() > record.Spec.Limits.MaxLogBytes || (!record.LogReady && info.Size() != 0) {
				return errors.New("guest task log does not match its durable receipt")
			}
			if record.LogBytes != info.Size() {
				record.LogBytes, changed = info.Size(), true
			}
		} else if !errors.Is(err, os.ErrNotExist) || record.LogReady || record.LaunchIntent || record.LogBytes != 0 {
			return fmt.Errorf("guest task log is unavailable: %w", err)
		}
		group, err := t.groups.Existing(taskGroupName(id))
		if err == nil {
			if record.BootID != t.service.bootID {
				group.Close()
				return errors.New("an old-boot task unexpectedly has a current cgroup; refusing to adopt it")
			}
			if err = stopTaskGroup(group); err != nil {
				group.Close()
				return fmt.Errorf("recover original task cgroup: %w", err)
			}
			group.Close()
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !guestTaskFinal(record.Status) {
			now := time.Now().UTC()
			record.Status, record.Reason, record.FinishedAt = "interrupted", "The guest executor stopped; this attempt was not relaunched.", &now
			changed = true
		}
	}
	if changed {
		return t.persistLocked()
	}
	return nil
}

func (t *projectTasks) openLog(record *guestTaskRecord, create bool) (*os.File, error) {
	return openTaskLog(t.root, record.AttemptID+".log", create)
}

func (t *projectTasks) inspectLocked(id protocol.TaskIdentity) (*guestTaskRecord, error) {
	if err := validateGuestTaskIdentity(id); err != nil {
		return nil, err
	}
	if t.fault != nil {
		return nil, projectProblem(503, "task_state_unavailable", t.fault.Error())
	}
	record := t.journal.Attempts[id.AttemptID]
	if record == nil {
		return nil, projectProblem(404, "task_not_found", "The original guest task attempt was not found.")
	}
	if record.TaskIdentity != id {
		return nil, projectProblem(409, "binding_changed", "The task request does not match its original execution identity.")
	}
	return record, nil
}

func (t *projectTasks) submit(in protocol.TaskSubmit) (protocol.TaskExecution, error) {
	id := taskSubmitIdentity(in)
	if err := validateGuestTaskIdentity(id); err != nil {
		return protocol.TaskExecution{}, err
	}
	if err := validateGuestTaskSpec(in.Spec); err != nil {
		return protocol.TaskExecution{}, err
	}
	if in.Spec.Initialization != nil && !image.Has(t.service.capabilities, "project-initialization") {
		return protocol.TaskExecution{}, projectProblem(501, "unsupported", "The guest image does not declare typed project initialization.")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fault != nil || t.closed {
		return protocol.TaskExecution{}, projectProblem(503, "task_state_unavailable", "The task executor is stopping or its durable state is unavailable.")
	}
	if record := t.journal.Attempts[in.AttemptID]; record != nil {
		original, _ := json.Marshal(record.Spec)
		requested, _ := json.Marshal(in.Spec)
		if record.TaskIdentity != id || string(original) != string(requested) {
			return protocol.TaskExecution{}, projectProblem(409, "idempotency_conflict", "The attempt ID was already accepted with different task inputs.")
		}
		return copyTaskExecution(record), nil
	}
	if len(t.journal.Attempts) >= maxGuestTaskRecords || len(t.active) >= 8 {
		return protocol.TaskExecution{}, projectProblem(503, "task_limit", "The guest allows eight active tasks and 256 retained attempt records.")
	}
	root, _, _, err := t.service.openProjectFiles(in.ProjectFileScope, false)
	if err != nil {
		return protocol.TaskExecution{}, err
	}
	defer root.Close()
	dir := in.Spec.Directory
	if dir == "" {
		dir = "."
	}
	selected, _, err := projectDirectory(root, dir)
	if err != nil {
		return protocol.TaskExecution{}, err
	}
	defer selected.Close()
	reserved, pending := int64(0), in.Spec.Limits.MaxLogBytes
	for id, existing := range t.journal.Attempts {
		reserved += existing.Spec.Limits.MaxLogBytes
		if _, active := t.active[id]; active {
			pending += existing.Spec.Limits.MaxLogBytes - existing.LogBytes
		}
	}
	if reserved+in.Spec.Limits.MaxLogBytes > maxGuestTaskLogs {
		return protocol.TaskExecution{}, projectProblem(503, "task_limit", "The guest's 256-MiB retained task-log reservation is full.")
	}
	if err := taskFreeSpace(t.root, selected, pending); err != nil {
		return protocol.TaskExecution{}, err
	}
	record := &guestTaskRecord{TaskExecution: protocol.TaskExecution{TaskIdentity: id, Spec: in.Spec, Status: "accepted", CreatedAt: time.Now().UTC()}}
	record.Spec.Argv = append([]string{}, in.Spec.Argv...)
	if in.Spec.Initialization != nil {
		intent := *in.Spec.Initialization
		record.Spec.Initialization = &intent
	}
	t.journal.Attempts[in.AttemptID] = record
	encoded, err := json.Marshal(t.journal)
	// A valid 4096-byte reason can expand to 24 KiB when JSON escapes '<', '>'
	// and '&'. Reserve that final growth before durably accepting execution.
	if err != nil || len(encoded)+1+guestTaskFinalHeadroom*len(t.journal.Attempts) > maxGuestTaskJournal {
		delete(t.journal.Attempts, in.AttemptID)
		return protocol.TaskExecution{}, projectProblem(503, "task_limit", "The guest task journal metadata budget is full.")
	}
	if err := t.persistLocked(); err != nil {
		return protocol.TaskExecution{}, err
	}
	runtime := &guestTaskRuntime{wake: make(chan struct{}, 1), done: make(chan struct{})}
	t.active[in.AttemptID] = runtime
	t.wg.Add(1)
	go t.run(record, runtime)
	return copyTaskExecution(record), nil
}

func (t *projectTasks) requestStopLocked(record *guestTaskRecord, reason string) error {
	if guestTaskFinal(record.Status) {
		return nil
	}
	if record.StopReason == "" {
		record.StopReason = reason
		if err := t.persistLocked(); err != nil {
			return err
		}
	}
	if runtime := t.active[record.AttemptID]; runtime != nil {
		select {
		case runtime.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (t *projectTasks) cancel(id protocol.TaskIdentity) (protocol.TaskExecution, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	record, err := t.inspectLocked(id)
	if err != nil {
		return protocol.TaskExecution{}, err
	}
	if id.BootID != t.service.bootID || id.InstanceID != t.service.instanceID {
		return protocol.TaskExecution{}, projectProblem(409, "binding_changed", "The original task executor is no longer current; cancellation was not redirected.")
	}
	if err := t.requestStopLocked(record, "cancelled"); err != nil {
		return protocol.TaskExecution{}, err
	}
	return copyTaskExecution(record), nil
}

func (t *projectTasks) logs(in protocol.TaskLogRequest) (protocol.TaskLog, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	record, err := t.inspectLocked(in.TaskIdentity)
	if err != nil {
		return protocol.TaskLog{}, err
	}
	if in.Limit == 0 {
		in.Limit = 64 << 10
	}
	if in.Offset < 0 || in.Offset > record.LogBytes || in.Limit < 1 || in.Limit > 64<<10 {
		return protocol.TaskLog{}, projectProblem(400, "invalid_request", "Use a retained log offset and a limit of 1–65536 bytes.")
	}
	result := protocol.TaskLog{Offset: in.Offset, NextOffset: in.Offset, LogBytes: record.LogBytes, LogLimited: record.LogLimited, Data: []byte{}}
	if record.LogBytes == in.Offset {
		return result, nil
	}
	file, err := t.openLog(record, false)
	if err != nil {
		return result, err
	}
	defer file.Close()
	result.Data = make([]byte, min(int64(in.Limit), record.LogBytes-in.Offset))
	n, err := file.ReadAt(result.Data, in.Offset)
	if err != nil || n != len(result.Data) {
		return result, fmt.Errorf("read original task log: %w", err)
	}
	result.NextOffset += int64(n)
	return result, nil
}

func (t *projectTasks) close() {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		for id := range t.active {
			_ = t.requestStopLocked(t.journal.Attempts[id], "executor_stopped")
		}
		t.mu.Unlock()
		t.wg.Wait()
		if groups, ok := t.groups.(io.Closer); ok {
			groups.Close()
		}
		t.lock.Close()
	})
}

func (s *service) projectTaskRequest(w http.ResponseWriter, r *http.Request, action string) {
	if s.tasks == nil {
		reason := "Project tasks require a compatible image, durable guest state and delegated cgroup v2 controllers."
		if s.taskError != nil {
			reason = s.taskError.Error()
		}
		projectFailure(w, projectProblem(501, "unsupported", reason))
		return
	}
	var result any
	var err error
	switch action {
	case "submit":
		var in protocol.TaskSubmit
		if err = decodeProjectJSON(w, r, &in, 256<<10, false); err == nil {
			result, err = s.tasks.submit(in)
		}
	case "inspect", "cancel":
		var in protocol.TaskIdentity
		if err = decodeProjectFile(w, r, &in, false); err == nil {
			if action == "cancel" {
				result, err = s.tasks.cancel(in)
			} else {
				s.tasks.mu.Lock()
				var record *guestTaskRecord
				if record, err = s.tasks.inspectLocked(in); err == nil {
					result = copyTaskExecution(record)
				}
				s.tasks.mu.Unlock()
			}
		}
	case "logs":
		var in protocol.TaskLogRequest
		if err = decodeProjectFile(w, r, &in, false); err == nil {
			result, err = s.tasks.logs(in)
		}
	}
	if err != nil {
		projectFailure(w, err)
		return
	}
	respond(w, 200, result)
}

// The task's process lifetime belongs to the guest supervisor, not a request.
// Loss of this supervisor makes an attempt interrupted; recovery never relaunches it.
func (t *projectTasks) run(record *guestTaskRecord, runtime *guestTaskRuntime) {
	defer t.wg.Done()
	defer close(runtime.done)
	var group taskGroup
	var log *os.File
	var cmd *exec.Cmd
	var finished chan error
	processExited := false
	var runErr error
	defer func() {
		var cleanupErr error
		if group != nil {
			if err := stopTaskGroup(group); err != nil {
				cleanupErr = fmt.Errorf("clean original task cgroup: %w", err)
				runErr = errors.Join(runErr, cleanupErr)
			}
			group.Close()
		}
		if record.Spec.Initialization != nil && runErr == nil {
			runErr = t.service.verifyInitializedRoot(record)
		}
		if log != nil {
			runErr = errors.Join(runErr, log.Sync(), log.Close())
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		status, reason := "succeeded", ""
		if runErr != nil {
			status, reason = "failed", runErr.Error()
			var exit *exec.ExitError
			if record.Spec.Initialization != nil && record.Spec.Initialization.Repository != "" && errors.As(runErr, &exit) {
				reason = "Clone failed. Check the repository URL and guest authentication. The created directory was preserved for inspection."
			}
		}
		if record.StopReason != "" {
			status, reason = "failed", record.StopReason
			if reason == "cancelled" {
				status = "cancelled"
			} else if reason == "executor_stopped" {
				status = "interrupted"
			}
		}
		if record.StopReason == "" && runtime.timedOut.Load() {
			status, reason = "failed", "timeout"
		}
		if cleanupErr != nil {
			status, reason = "interrupted", "Task termination could not be confirmed: "+cleanupErr.Error()
			t.fault = cleanupErr
		}
		if processExited && cmd != nil && cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			record.ExitCode = &code
		}
		now := time.Now().UTC()
		record.Status, record.Reason, record.FinishedAt = status, boundedTaskReason(reason), &now
		_ = t.persistLocked()
		delete(t.active, record.AttemptID)
	}()
	root, rootInfo, _, err := t.service.openProjectFiles(record.ProjectFileScope, false)
	if err != nil {
		runErr = err
		return
	}
	defer root.Close()
	name, _ := projectRelative(record.Spec.Directory, true)
	if intent := record.Spec.Initialization; intent != nil {
		t.mu.Lock()
		if record.StopReason != "" || t.closed || t.fault != nil {
			t.mu.Unlock()
			return
		}
		var created *protocol.ProjectFileRoot
		created, err = t.createInitializationRoot(record, root, rootInfo)
		if err == nil {
			record.CreatedRoot = created
			err = t.persistLocked()
		}
		t.mu.Unlock()
		if err != nil {
			runErr = err
			return
		}
		name = intent.Directory
	}
	dir, dirInfo, err := projectDirectory(root, name)
	if err != nil {
		runErr = err
		return
	}
	defer dir.Close()
	cwd, err := dir.Open(".")
	if err != nil {
		runErr = err
		return
	}
	defer cwd.Close()
	group, err = t.groups.Create(taskGroupName(record.AttemptID), record.Spec.Limits)
	if err != nil {
		runErr = err
		return
	}
	log, err = t.openLog(record, true)
	if err == nil {
		err = log.Sync()
	}
	if err == nil {
		err = syncTaskDirectory(t.root)
	}
	if err != nil {
		runErr = err
		return
	}
	writer := &guestTaskLogWriter{tasks: t, record: record, file: log}
	argv := record.Spec.Argv
	if record.Spec.Initialization != nil {
		argv, _ = initializationCommand(record.Spec.Initialization)
	}
	cmd = exec.Command(argv[0], argv[1:]...)
	cmd.Dir = taskCommandDirectory(cwd)
	cmd.Stdout, cmd.Stderr = writer, writer
	if record.Spec.Initialization != nil {
		configureInitializationCommand(cmd, record.Spec.Initialization)
	}
	cmd.WaitDelay = 2 * time.Second
	if err = group.Configure(cmd); err != nil {
		runErr = err
		return
	}
	if err = t.service.verifyProjectFiles(record.ProjectFileScope, rootInfo); err == nil {
		var selected *os.Root
		var current os.FileInfo
		selected, current, err = projectDirectory(root, name)
		if selected != nil {
			selected.Close()
		}
		if err == nil && !os.SameFile(dirInfo, current) {
			err = projectProblem(409, "project_changed", "The task starting directory changed before launch.")
		}
	}
	if err != nil {
		runErr = err
		return
	}
	t.mu.Lock()
	if record.StopReason != "" || t.closed || t.fault != nil {
		if t.fault != nil {
			runErr = t.fault
		}
		if t.closed && record.StopReason == "" {
			record.StopReason = "executor_stopped"
		}
		t.mu.Unlock()
		return
	}
	record.LogReady, record.LaunchIntent = true, true
	if err = t.persistLocked(); err == nil {
		err = cmd.Start()
	}
	if err == nil {
		now := time.Now().UTC()
		record.Status, record.StartedAt = "running", &now
		finished = make(chan error, 1)
		go func() { finished <- cmd.Wait() }()
		// This watchdog never waits for the journal mutex. A slow or failed
		// receipt fsync must not let the command exceed its execution deadline.
		watchdogDone := make(chan struct{})
		watchdog := time.AfterFunc(time.Duration(record.Spec.Limits.TimeoutSeconds)*time.Second, func() {
			defer close(watchdogDone)
			runtime.timedOut.Store(true)
			_ = group.Kill()
			select {
			case runtime.wake <- struct{}{}:
			default:
			}
		})
		defer func() {
			if !watchdog.Stop() {
				<-watchdogDone
			}
		}()
		err = t.persistLocked()
	}
	t.mu.Unlock()
	if err != nil {
		runErr = err
		if cmd.Process != nil {
			_ = group.Kill()
			select {
			case waitErr := <-finished:
				processExited = true
				runErr = errors.Join(runErr, waitErr)
			case <-time.After(3 * time.Second):
				runErr = errors.Join(runErr, errors.New("task exit could not be confirmed after a failed receipt write"))
			}
		}
		return
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case runErr = <-finished:
			processExited = true
			resourceReason, _, eventErr := group.Events()
			if resourceReason != "" || eventErr != nil {
				t.mu.Lock()
				if resourceReason == "" {
					resourceReason = "resource_control_unavailable"
				}
				_ = t.requestStopLocked(record, resourceReason)
				t.mu.Unlock()
			}
			return
		case <-t.service.ctx.Done():
			t.mu.Lock()
			_ = t.requestStopLocked(record, "executor_stopped")
			t.mu.Unlock()
		case <-tick.C:
			reason, _, err := group.Events()
			if err != nil {
				reason = "resource_control_unavailable"
			}
			if reason != "" {
				t.mu.Lock()
				_ = t.requestStopLocked(record, reason)
				t.mu.Unlock()
			}
		case <-runtime.wake:
		}
		t.mu.Lock()
		stop := record.StopReason != "" || runtime.timedOut.Load() || t.fault != nil || t.closed
		t.mu.Unlock()
		if stop {
			if err := group.Kill(); err != nil {
				runErr = err
				return
			}
			select {
			case waitErr := <-finished:
				processExited = true
				runErr = errors.Join(runErr, waitErr)
			case <-time.After(3 * time.Second):
				runErr = errors.New("the original task did not exit after cgroup cancellation")
			}
			return
		}
	}
}

type guestTaskLogWriter struct {
	tasks  *projectTasks
	record *guestTaskRecord
	file   *os.File
}

func (w *guestTaskLogWriter) Write(data []byte) (int, error) {
	t := w.tasks
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fault != nil {
		return 0, t.fault
	}
	if guestTaskFinal(w.record.Status) {
		return 0, errors.New("the task's durable log is already finalized")
	}
	remaining := w.record.Spec.Limits.MaxLogBytes - w.record.LogBytes
	written, err := w.file.Write(data[:min(int64(len(data)), remaining)])
	err = errors.Join(err, w.file.Sync())
	w.record.LogBytes += int64(written)
	if written != len(data) && err == nil {
		w.record.LogLimited = true
		err = io.ErrShortWrite
	}
	if err != nil {
		reason := "log_write_failed: " + err.Error()
		if w.record.LogLimited {
			reason = "log_limit"
		}
		_ = t.requestStopLocked(w.record, reason)
	}
	if persistErr := t.persistLocked(); err == nil {
		err = persistErr
	}
	return written, err
}

func stopTaskGroup(group taskGroup) error {
	if err := group.Kill(); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, populated, err := group.Events()
		if err != nil {
			return err
		}
		if !populated {
			return group.Remove()
		}
		if time.Now().After(deadline) {
			return errors.New("the original task cgroup did not become empty after cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func boundedTaskReason(reason string) string {
	reason = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, reason)
	if len(reason) <= 4096 {
		return reason
	}
	reason = reason[:4096]
	for !utf8.ValidString(reason) {
		reason = reason[:len(reason)-1]
	}
	return reason
}

func (s *service) initializeTasks() {
	groups, err := systemTaskGroups()
	if err == nil {
		s.tasks, err = openProjectTasks(s, "/var/lib/axv/tasks", groups, 0)
		if err != nil {
			if closer, ok := groups.(io.Closer); ok {
				closer.Close()
			}
		}
	}
	s.taskError = err
}

func (s *service) taskAvailabilityError() error {
	if s.tasks == nil {
		if s.taskError != nil {
			return s.taskError
		}
		return errors.New("durable guest task state and delegated cgroup v2 are unavailable")
	}
	s.tasks.mu.Lock()
	defer s.tasks.mu.Unlock()
	if s.tasks.fault != nil {
		return s.tasks.fault
	}
	if s.tasks.closed {
		return errors.New("the task executor is stopping")
	}
	return nil
}
