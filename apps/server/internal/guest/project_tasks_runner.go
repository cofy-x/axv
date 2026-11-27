package guest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

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
