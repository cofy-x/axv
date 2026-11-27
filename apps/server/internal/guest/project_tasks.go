package guest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
