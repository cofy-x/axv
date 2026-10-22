package control

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

type taskFixture struct {
	*projectFixture
	bound            compute.ProjectBindingResult
	mu               sync.Mutex
	records          map[string]protocol.TaskExecution
	logs             map[string][]byte
	growAfterInspect map[string][]byte
	logOverride      func(protocol.TaskLog) protocol.TaskLog
	submits          map[string]int
	cancels          []string
	loseReply        bool
	offline          bool
}

func newTaskFixture(t *testing.T, capabilities ...string) *taskFixture {
	t.Helper()
	project := newProjectFixture(t, append([]string{"project-tasks"}, capabilities...)...)
	f := &taskFixture{projectFixture: project, bound: project.registered(t), records: map[string]protocol.TaskExecution{}, logs: map[string][]byte{}, submits: map[string]int{}}
	project.mu.Lock()
	project.file = f.serve
	project.mu.Unlock()
	return f
}

func (f *taskFixture) serve(w http.ResponseWriter, r *http.Request) {
	// Consume the body before closing or returning a synthetic error. The real
	// guest decodes its request first; early TCP close can lose the test response.
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			connection.Close()
		}
		return
	}
	if strings.HasSuffix(r.URL.Path, "/submit") {
		var in protocol.TaskSubmit
		if err := json.Unmarshal(data, &in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		identity := protocol.TaskIdentity{ProjectFileScope: in.ProjectFileScope, TaskID: in.TaskID, AttemptID: in.AttemptID}
		f.submits[in.AttemptID]++
		record, exists := f.records[in.AttemptID]
		if !exists {
			record = protocol.TaskExecution{TaskIdentity: identity, Spec: in.Spec, Status: "accepted", CreatedAt: time.Now().UTC()}
			f.records[in.AttemptID] = record
		}
		if f.loseReply {
			f.loseReply = false
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
			return
		}
		json.NewEncoder(w).Encode(record)
		return
	}
	var in protocol.TaskLogRequest
	if err := json.Unmarshal(data, &in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	record, exists := f.records[in.AttemptID]
	if !exists || record.TaskIdentity != in.TaskIdentity {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(compute.Problem("task_not_found", "Original attempt receipt is missing."))
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/inspect"):
		json.NewEncoder(w).Encode(record)
		if grown, ok := f.growAfterInspect[in.AttemptID]; ok {
			record.LogBytes = int64(len(grown))
			f.records[in.AttemptID], f.logs[in.AttemptID] = record, grown
			delete(f.growAfterInspect, in.AttemptID)
		}
	case strings.HasSuffix(r.URL.Path, "/cancel"):
		f.cancels = append(f.cancels, in.AttemptID)
		now := time.Now().UTC()
		record.Status, record.FinishedAt = "cancelled", &now
		f.records[in.AttemptID] = record
		json.NewEncoder(w).Encode(record)
	case strings.HasSuffix(r.URL.Path, "/logs"):
		data := f.logs[in.AttemptID]
		if in.Offset < 0 || in.Offset > int64(len(data)) || in.Limit < 1 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(compute.Problem("invalid_request", "Invalid log range."))
			return
		}
		end := min(int64(len(data)), in.Offset+int64(in.Limit))
		chunk := protocol.TaskLog{Offset: in.Offset, Data: data[in.Offset:end], NextOffset: end, LogBytes: int64(len(data))}
		if f.logOverride != nil {
			chunk = f.logOverride(chunk)
		}
		json.NewEncoder(w).Encode(chunk)
	default:
		http.NotFound(w, r)
	}
}

func (f *taskFixture) submit(t *testing.T, requestID string) compute.ProjectTaskResult {
	t.Helper()
	result, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, compute.SubmitProjectTaskRequest{RequestID: requestID, Binding: f.bound.Binding, Spec: compute.TaskSpec{Argv: []string{"sh", "-ec", "printf task"}}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f *taskFixture) reconcile(t *testing.T, id string) compute.ProjectTaskResult {
	t.Helper()
	before, err := f.service.ProjectTask(t.Context(), f.bound.Project.ID, id, f.bound.Binding.HostID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.reconcileTask(t.Context(), before.Attempt); err != nil {
		t.Fatal(err)
	}
	after, err := f.service.ProjectTask(t.Context(), f.bound.Project.ID, id, f.bound.Binding.HostID)
	if err != nil {
		t.Fatal(err)
	}
	return after
}

func (f *taskFixture) finish(id string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.records[id]
	now, code := time.Now().UTC(), 0
	record.Status, record.FinishedAt, record.ExitCode, record.LogBytes = "succeeded", &now, &code, int64(len(data))
	if record.Spec.Initialization != nil {
		record.CreatedRoot = &protocol.ProjectFileRoot{Root: path.Join(record.Root, record.Spec.Initialization.Directory), RootIdentity: "inode:123"}
	}
	f.records[id], f.logs[id] = record, append([]byte{}, data...)
}

func TestProjectTaskLostGuestResponseSurvivesHostRestartWithoutResubmit(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "lost-submit-original")
	f.mu.Lock()
	f.loseReply = true
	f.mu.Unlock()
	unknown := f.reconcile(t, accepted.Task.ID)
	if unknown.Attempt.Status != "unknown" {
		t.Fatalf("lost acknowledgement was not retained as uncertain: %+v", unknown.Attempt)
	}
	if _, err := f.service.RetryProjectTask(t.Context(), f.bound.Project.ID, accepted.Task.ID, compute.RetryProjectTaskRequest{RequestID: "retry-while-unknown", Binding: f.bound.Binding}); compute.ErrorCode(err) != "task_conflict" {
		t.Fatalf("unknown attempt permitted overlapping retry: %v", err)
	}
	directory, hostID := f.service.store.directory, f.service.store.HostID
	if err := f.service.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	f.service.store = reopened
	if reopened.HostID != hostID {
		t.Fatal("controller restart changed task host identity")
	}
	receipt, err := f.service.ProjectTaskByRequest(t.Context(), f.bound.Project.ID, "lost-submit-original", hostID)
	if err != nil || receipt.Task.ID != accepted.Task.ID || receipt.Attempt.ID != accepted.Attempt.ID {
		t.Fatalf("original receipt was not durable: %+v %v", receipt, err)
	}
	observed := f.reconcile(t, accepted.Task.ID)
	if observed.Attempt.Status != "dispatching" {
		t.Fatalf("guest accepted receipt was not preserved without reclaim: %+v", observed.Attempt)
	}
	f.reconcile(t, accepted.Task.ID)
	f.mu.Lock()
	count := f.submits[accepted.Attempt.ID]
	f.mu.Unlock()
	if count != 1 {
		t.Fatalf("recovery redispatched the accepted guest command %d times", count)
	}
	// Receipt recovery precedes guest checks even after the executor disappears.
	f.projectFixture.mu.Lock()
	f.health.Ready = false
	f.projectFixture.mu.Unlock()
	replay, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, compute.SubmitProjectTaskRequest{RequestID: "lost-submit-original", Binding: f.bound.Binding, Spec: accepted.Task.Spec})
	if err != nil || replay.Attempt.ID != accepted.Attempt.ID {
		t.Fatalf("durable replay required a current guest or created another attempt: %+v %v", replay, err)
	}
}

func TestProjectTaskRetryAndCancellationRetainExactAttempts(t *testing.T) {
	f := newTaskFixture(t)
	first := f.submit(t, "task-first-original")
	f.reconcile(t, first.Task.ID)
	f.finish(first.Attempt.ID, nil)
	f.reconcile(t, first.Task.ID)
	retryRequest := compute.RetryProjectTaskRequest{RequestID: "retry-first-original", Binding: f.bound.Binding}
	second, err := f.service.RetryProjectTask(t.Context(), f.bound.Project.ID, first.Task.ID, retryRequest)
	if err != nil || second.Attempt.ID == first.Attempt.ID || second.Attempt.Number != 2 || len(second.Attempts) != 2 {
		t.Fatalf("retry replaced history rather than creating an attempt: %+v %v", second, err)
	}
	f.reconcile(t, second.Task.ID)
	other := f.submit(t, "other-task-original")
	f.reconcile(t, other.Task.ID)
	for _, target := range []compute.ProjectTaskResult{first, second} {
		_, err := f.service.CancelProjectTaskAttempt(t.Context(), f.bound.Project.ID, target.Task.ID, target.Attempt.ID, compute.CancelProjectTaskRequest{Binding: f.bound.Binding})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.reconcile(t, second.Task.ID)
	unchanged, err := f.service.ProjectTask(t.Context(), f.bound.Project.ID, other.Task.ID, f.bound.Binding.HostID)
	if err != nil || unchanged.Attempt.Cancel || compute.TaskFinal(unchanged.Attempt.Status) {
		t.Fatalf("cancellation affected unrelated task: %+v %v", unchanged, err)
	}
	f.mu.Lock()
	cancels := append([]string{}, f.cancels...)
	f.mu.Unlock()
	if len(cancels) != 1 || cancels[0] != second.Attempt.ID {
		t.Fatalf("old cancellation was sent to a new/unrelated attempt: %v", cancels)
	}
	receipt, err := f.service.ProjectTaskByRequest(t.Context(), f.bound.Project.ID, first.Task.RequestID, f.bound.Binding.HostID)
	if err != nil || receipt.Attempt.ID != first.Attempt.ID || receipt.Task.CurrentAttemptID != second.Attempt.ID {
		t.Fatalf("original receipt substituted a later attempt: %+v %v", receipt, err)
	}
	f.projectFixture.mu.Lock()
	f.health.Ready = false
	f.projectFixture.mu.Unlock()
	replayed, err := f.service.RetryProjectTask(t.Context(), f.bound.Project.ID, first.Task.ID, retryRequest)
	if err != nil || replayed.Attempt.ID != second.Attempt.ID || len(replayed.Attempts) != 2 {
		t.Fatalf("retry receipt redispatched or required new environment: %+v %v", replayed, err)
	}
}

func TestProjectTaskCachedBinaryLogsStayReadableOffline(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "binary-log-original")
	f.reconcile(t, accepted.Task.ID)
	data := append([]byte{0xff, 0, '\n'}, bytes.Repeat([]byte("世界"), 12000)...)
	f.finish(accepted.Attempt.ID, data)
	f.reconcile(t, accepted.Task.ID)
	f.mu.Lock()
	f.offline = true
	f.mu.Unlock()
	partial := f.reconcile(t, accepted.Task.ID)
	if partial.Attempt.Status != "succeeded" || partial.Attempt.SyncError == "" || partial.Attempt.CachedBytes != 65536 {
		t.Fatalf("missing log bytes overwrote a final result or hid sync failure: %+v", partial.Attempt)
	}
	first, err := f.service.ProjectTaskLogs(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, f.bound.Binding.HostID, 0, 65536)
	if err != nil || !bytes.Equal(first.Data, data[:65536]) || first.NextOffset != 65536 || first.Status != "succeeded" {
		t.Fatalf("offline cache changed original binary log: %+v %v", first, err)
	}
	f.mu.Lock()
	f.offline = false
	f.mu.Unlock()
	complete := f.reconcile(t, accepted.Task.ID)
	if complete.Attempt.SyncError != "" || complete.Attempt.CachedBytes != int64(len(data)) {
		t.Fatalf("log recovery did not clear synchronization error: %+v", complete.Attempt)
	}
	f.mu.Lock()
	f.offline = true
	f.mu.Unlock()
	last, err := f.service.ProjectTaskLogs(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, f.bound.Binding.HostID, first.NextOffset, 65536)
	if err != nil || !bytes.Equal(last.Data, data[65536:]) || last.NextOffset != int64(len(data)) {
		t.Fatalf("log offset did not resume exact bytes: %+v %v", last, err)
	}
	if _, err := f.service.ProjectTaskLogs(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, f.bound.Binding.HostID, int64(len(data))+1, 1); compute.ErrorCode(err) != "invalid_request" {
		t.Fatalf("uncached offset accepted: %v", err)
	}
}

func TestProjectTaskCachesInspectedLogPrefixWhileGuestOutputGrows(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "growing-log-original")
	f.reconcile(t, accepted.Task.ID)
	f.mu.Lock()
	record := f.records[accepted.Attempt.ID]
	record.Status, record.LogBytes = "running", 3
	f.records[accepted.Attempt.ID] = record
	f.logs[accepted.Attempt.ID] = []byte("abc")
	f.growAfterInspect = map[string][]byte{accepted.Attempt.ID: []byte("abcdef")}
	f.mu.Unlock()
	first := f.reconcile(t, accepted.Task.ID)
	if first.Attempt.Status != "running" || first.Attempt.LogBytes != 3 || first.Attempt.CachedBytes != 3 || first.Attempt.SyncError != "" {
		t.Fatalf("concurrent guest log growth prevented caching the inspected prefix: %+v", first.Attempt)
	}
	chunk, err := f.service.ProjectTaskLogs(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, f.bound.Binding.HostID, 0, 65536)
	if err != nil || string(chunk.Data) != "abc" || chunk.NextOffset != 3 {
		t.Fatalf("first cache exceeded the inspected guest snapshot: %+v %v", chunk, err)
	}
	second := f.reconcile(t, accepted.Task.ID)
	if second.Attempt.LogBytes != 6 || second.Attempt.CachedBytes != 6 || second.Attempt.SyncError != "" {
		t.Fatalf("later guest output did not resume caching: %+v", second.Attempt)
	}
	chunk, err = f.service.ProjectTaskLogs(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, f.bound.Binding.HostID, 3, 65536)
	if err != nil || string(chunk.Data) != "def" || chunk.NextOffset != 6 {
		t.Fatalf("later cache lost exact byte offsets: %+v %v", chunk, err)
	}
}

func TestProjectTaskMalformedLogPrefixPreservesResultAndRecovers(t *testing.T) {
	for name, malformed := range map[string]func(protocol.TaskLog) protocol.TaskLog{
		"empty": func(chunk protocol.TaskLog) protocol.TaskLog {
			chunk.Data, chunk.NextOffset = []byte{}, chunk.Offset
			return chunk
		},
		"short": func(chunk protocol.TaskLog) protocol.TaskLog {
			chunk.Data = chunk.Data[:len(chunk.Data)-1]
			chunk.NextOffset--
			return chunk
		},
		"wrong offset": func(chunk protocol.TaskLog) protocol.TaskLog {
			chunk.Offset++
			chunk.NextOffset++
			return chunk
		},
		"wrong next offset": func(chunk protocol.TaskLog) protocol.TaskLog {
			chunk.NextOffset--
			return chunk
		},
		"smaller size": func(chunk protocol.TaskLog) protocol.TaskLog {
			chunk.LogBytes--
			return chunk
		},
		"extra final tail": func(chunk protocol.TaskLog) protocol.TaskLog {
			chunk.LogBytes++
			return chunk
		},
		"changed final limit": func(chunk protocol.TaskLog) protocol.TaskLog {
			chunk.LogLimited = !chunk.LogLimited
			return chunk
		},
		"excessive size": func(chunk protocol.TaskLog) protocol.TaskLog {
			chunk.LogBytes = 9 << 20
			return chunk
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTaskFixture(t)
			accepted := f.submit(t, "invalid-log-prefix")
			f.reconcile(t, accepted.Task.ID)
			data := []byte("retained original output")
			f.finish(accepted.Attempt.ID, data)
			f.mu.Lock()
			f.logOverride = malformed
			f.mu.Unlock()
			broken := f.reconcile(t, accepted.Task.ID)
			if broken.Attempt.Status != "succeeded" || broken.Attempt.SyncError == "" || broken.Attempt.LogBytes != int64(len(data)) || broken.Attempt.CachedBytes != 0 {
				t.Fatalf("malformed log hid its missing prefix or changed a known final result: %+v", broken.Attempt)
			}
			f.mu.Lock()
			f.logOverride = nil
			f.mu.Unlock()
			recovered := f.reconcile(t, accepted.Task.ID)
			if recovered.Attempt.Status != "succeeded" || recovered.Attempt.SyncError != "" || recovered.Attempt.CachedBytes != int64(len(data)) {
				t.Fatalf("corrected log did not recover the retained prefix: %+v", recovered.Attempt)
			}
			chunk, err := f.service.ProjectTaskLogs(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, f.bound.Binding.HostID, 0, 65536)
			if err != nil || !bytes.Equal(chunk.Data, data) {
				t.Fatalf("malformed response corrupted the log cache: %q %v", chunk.Data, err)
			}
		})
	}
}

func TestProjectTaskGuestCannotRegressKnownLogBounds(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "append-only-log-bounds")
	f.reconcile(t, accepted.Task.ID)
	data := bytes.Repeat([]byte("x"), 65537)
	f.finish(accepted.Attempt.ID, data)
	partial := f.reconcile(t, accepted.Task.ID)
	if partial.Attempt.LogBytes != int64(len(data)) || partial.Attempt.CachedBytes != 65536 {
		t.Fatalf("fixture has no pending log tail: %+v", partial.Attempt)
	}
	f.mu.Lock()
	original := f.records[accepted.Attempt.ID]
	regressed := original
	regressed.LogBytes = partial.Attempt.CachedBytes
	f.records[accepted.Attempt.ID] = regressed
	f.mu.Unlock()
	invalid := f.reconcile(t, accepted.Task.ID)
	if invalid.Attempt.Status != "succeeded" || invalid.Attempt.SyncError == "" || invalid.Attempt.LogBytes != int64(len(data)) || invalid.Attempt.CachedBytes != 65536 {
		t.Fatalf("regressed receipt silently discarded the known log tail: %+v", invalid.Attempt)
	}
	f.mu.Lock()
	f.records[accepted.Attempt.ID] = original
	f.mu.Unlock()
	recovered := f.reconcile(t, accepted.Task.ID)
	if recovered.Attempt.SyncError != "" || recovered.Attempt.CachedBytes != int64(len(data)) {
		t.Fatalf("append-only log recovery did not resume: %+v", recovered.Attempt)
	}
}

func TestProjectTaskGuestCannotChangeFinalLogBounds(t *testing.T) {
	for _, change := range []string{"larger log", "changed log limit", "running receipt"} {
		t.Run(change, func(t *testing.T) {
			f := newTaskFixture(t)
			accepted := f.submit(t, "immutable-final-log")
			f.reconcile(t, accepted.Task.ID)
			data := bytes.Repeat([]byte("x"), 65537)
			f.finish(accepted.Attempt.ID, data)
			partial := f.reconcile(t, accepted.Task.ID)
			if partial.Attempt.Status != "succeeded" || !partial.Attempt.GuestFinal || partial.Attempt.LogBytes != int64(len(data)) || partial.Attempt.CachedBytes != 65536 {
				t.Fatalf("fixture has no final result with an uncached tail: %+v", partial.Attempt)
			}
			directory := f.service.store.directory
			if err := f.service.store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStore(directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reopened.Close() })
			f.service.store = reopened
			f.mu.Lock()
			original := f.records[accepted.Attempt.ID]
			changed := original
			switch change {
			case "larger log":
				changed.LogBytes++
				f.logs[accepted.Attempt.ID] = append(append([]byte{}, data...), 'y')
			case "changed log limit":
				changed.LogLimited = true
				f.logOverride = func(chunk protocol.TaskLog) protocol.TaskLog {
					chunk.LogLimited = true
					return chunk
				}
			case "running receipt":
				changed.Status, changed.FinishedAt = "running", nil
			}
			f.records[accepted.Attempt.ID] = changed
			f.mu.Unlock()
			if err := f.service.store.saveTaskExecution(t.Context(), accepted.Attempt.ID, changed, nil); compute.ErrorCode(err) != "guest_identity_mismatch" {
				t.Fatalf("the store accepted a changed final receipt after reopening: %v", err)
			}
			invalid := f.reconcile(t, accepted.Task.ID)
			if invalid.Attempt.Status != "succeeded" || invalid.Attempt.SyncError == "" || invalid.Attempt.LogBytes != partial.Attempt.LogBytes || invalid.Attempt.LogLimited != partial.Attempt.LogLimited || invalid.Attempt.CachedBytes != partial.Attempt.CachedBytes {
				t.Fatalf("changed final receipt modified the verified result or log cache: %+v", invalid.Attempt)
			}
			f.mu.Lock()
			f.records[accepted.Attempt.ID] = original
			f.logs[accepted.Attempt.ID] = data
			f.logOverride = nil
			f.mu.Unlock()
			recovered := f.reconcile(t, accepted.Task.ID)
			if recovered.Attempt.Status != "succeeded" || recovered.Attempt.SyncError != "" || recovered.Attempt.CachedBytes != int64(len(data)) {
				t.Fatalf("the original final receipt did not recover its remaining log: %+v", recovered.Attempt)
			}
			chunk, err := f.service.ProjectTaskLogs(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, f.bound.Binding.HostID, 65536, 65536)
			if err != nil || !bytes.Equal(chunk.Data, data[65536:]) || chunk.NextOffset != int64(len(data)) {
				t.Fatalf("recovery cached bytes outside the verified final log: %+v %v", chunk, err)
			}
		})
	}
}

func TestProjectTaskHostInterruptionCanRecoverOriginalGuestLogTail(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "interrupted-log-recovery")
	f.reconcile(t, accepted.Task.ID)
	data := bytes.Repeat([]byte("x"), 65537)
	f.mu.Lock()
	record := f.records[accepted.Attempt.ID]
	record.Status, record.LogBytes = "running", int64(len(data))
	f.records[accepted.Attempt.ID], f.logs[accepted.Attempt.ID] = record, data
	f.mu.Unlock()
	partial := f.reconcile(t, accepted.Task.ID)
	if partial.Attempt.GuestFinal || partial.Attempt.CachedBytes != 65536 {
		t.Fatalf("fixture did not retain an active log tail: %+v", partial.Attempt)
	}
	if err := f.service.store.saveTaskFailure(t.Context(), accepted.Attempt.ID, "interrupted", "The original VM stopped before completion could be verified."); err != nil {
		t.Fatal(err)
	}
	interrupted, err := f.service.ProjectTask(t.Context(), f.bound.Project.ID, accepted.Task.ID, f.bound.Binding.HostID)
	if err != nil || interrupted.Attempt.GuestFinal || interrupted.Attempt.Status != "interrupted" {
		t.Fatalf("host interruption was mistaken for a guest terminal receipt: %+v %v", interrupted.Attempt, err)
	}
	data = append(data, 'y', 'z')
	f.finish(accepted.Attempt.ID, data)
	recovered := f.reconcile(t, accepted.Task.ID)
	if recovered.Attempt.Status != "interrupted" || recovered.Attempt.Reason != interrupted.Attempt.Reason || !recovered.Attempt.FinishedAt.Equal(*interrupted.Attempt.FinishedAt) || !recovered.Attempt.GuestFinal || recovered.Attempt.LogBytes != int64(len(data)) || recovered.Attempt.CachedBytes != int64(len(data)) || recovered.Attempt.SyncError != "" {
		t.Fatalf("guest log recovery changed the host terminal result or lost the remaining tail: %+v", recovered.Attempt)
	}
	chunk, err := f.service.ProjectTaskLogs(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, f.bound.Binding.HostID, 65536, 65536)
	if err != nil || !bytes.Equal(chunk.Data, data[65536:]) || chunk.NextOffset != int64(len(data)) {
		t.Fatalf("host interruption prevented recovery of the original log bytes: %+v %v", chunk, err)
	}
}

func TestProjectTaskOriginalCancellationWorksWhenProjectBecomesUnavailable(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "project-unavailable-original")
	f.reconcile(t, accepted.Task.ID)
	f.projectFixture.mu.Lock()
	f.health.Ready = false
	f.projectFixture.mu.Unlock()
	if _, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, compute.SubmitProjectTaskRequest{RequestID: "project-unavailable-new", Binding: f.bound.Binding, Spec: accepted.Task.Spec}); compute.ErrorCode(err) != "guest_unavailable" {
		t.Fatalf("new submission ignored unavailable project: %v", err)
	}
	if _, err := f.service.CancelProjectTaskAttempt(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, compute.CancelProjectTaskRequest{Binding: f.bound.Binding}); err != nil {
		t.Fatal(err)
	}
	observed := f.reconcile(t, accepted.Task.ID)
	if observed.Attempt.Status != "cancelled" {
		t.Fatalf("unavailable project prevented original task cancellation: %+v", observed.Attempt)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cancels) != 1 || f.cancels[0] != accepted.Attempt.ID {
		t.Fatalf("cancellation lost its original execution target: %v", f.cancels)
	}
}

func TestProjectTaskUnavailableCapabilityPreservesGuestDiagnosis(t *testing.T) {
	f := newTaskFixture(t)
	diagnosis := "Project tasks unavailable: original task journal is missing; refusing to reset it"
	f.projectFixture.mu.Lock()
	f.health.Capabilities = []string{"project-files"}
	f.health.Reason = diagnosis
	f.projectFixture.mu.Unlock()
	var observation compute.Observation
	f.service.observeGuest(t.Context(), f.machine, f.driver, &observation)
	if !observation.Ready || observation.GuestState != "ready" || observation.GuestReason != diagnosis || len(observation.GuestCapabilities) != 1 || observation.GuestCapabilities[0] != "project-files" {
		t.Fatalf("task failure hid its diagnosis or changed basic guest readiness: %+v", observation)
	}
	if _, err := f.service.ProjectBinding(t.Context(), f.bound.Project.ID, f.bound.Binding.HostID); err != nil {
		t.Fatalf("task failure disabled independent project file access: %v", err)
	}
	request := compute.SubmitProjectTaskRequest{RequestID: "task-capability-unavailable", Binding: f.bound.Binding, Spec: compute.TaskSpec{Argv: []string{"true"}}}
	if _, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, request); compute.ErrorCode(err) != "unsupported" || !strings.Contains(err.Error(), diagnosis) {
		t.Fatalf("task admission hid its actual state/resource failure: %v", err)
	}
	if _, exists, err := f.service.store.taskByRequest(t.Context(), f.bound.Project.ID, request.RequestID, ""); err != nil || exists {
		t.Fatalf("unsupported executor accepted a task receipt: exists=%v error=%v", exists, err)
	}
	f.projectFixture.mu.Lock()
	f.health.Capabilities = []string{"project-files", "project-tasks"}
	f.health.Reason = ""
	f.projectFixture.mu.Unlock()
	f.service.observeGuest(t.Context(), f.machine, f.driver, &observation)
	if !observation.Ready || observation.GuestReason != "" {
		t.Fatalf("healthy executor retained a stale task diagnosis: %+v", observation)
	}
}

func TestProjectTaskAdmissionRejectsBindingsAndCrossNamespaceReuse(t *testing.T) {
	f := newTaskFixture(t)
	base := compute.SubmitProjectTaskRequest{RequestID: "task-admission-original", Binding: f.bound.Binding, Spec: compute.TaskSpec{Argv: []string{"true"}}}
	for _, field := range []string{"host", "project", "revision", "volume", "volumeRevision", "machine", "boot", "instance"} {
		t.Run(field, func(t *testing.T) {
			r := base
			switch field {
			case "host":
				r.Binding.HostID = "replacement-host"
			case "project":
				r.Binding.ProjectID = "replacement-project"
			case "revision":
				r.Binding.ProjectRevision++
			case "volume":
				r.Binding.VolumeID = "replacement-volume"
			case "volumeRevision":
				r.Binding.VolumeRevision++
			case "machine":
				r.Binding.MachineID = "replacement-machine"
			case "boot":
				r.Binding.BootID = "replacement-boot"
			case "instance":
				r.Binding.InstanceID = "replacement-instance"
			}
			if _, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, r); err == nil {
				t.Fatal("stale task binding was accepted")
			}
		})
	}
	for _, reused := range []string{f.request.RequestID, "guest-fixture-create"} {
		r := base
		r.RequestID = reused
		if _, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, r); compute.ErrorCode(err) != "idempotency_conflict" {
			t.Fatalf("task reused a project/operation request ID: %v", err)
		}
	}
	accepted, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, base)
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.Spec.Argv = []string{"different-command"}
	if _, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, changed); compute.ErrorCode(err) != "idempotency_conflict" {
		t.Fatalf("changed command reused original receipt: %v", err)
	}
	registration := f.request
	registration.RequestID = base.RequestID
	if _, err := f.service.RegisterProject(t.Context(), registration); compute.ErrorCode(err) != "idempotency_conflict" {
		t.Fatalf("project registration reused a task receipt: %v", err)
	}
	if _, err := f.service.Action(t.Context(), f.machine.ID, compute.ActionRequest{RequestID: base.RequestID, Revision: f.machine.Revision, Action: "stop"}); compute.ErrorCode(err) != "idempotency_conflict" {
		t.Fatalf("lifecycle operation reused a task receipt: %v", err)
	}
	wrong := f.bound.Binding
	wrong.InstanceID = "replacement-instance"
	if _, err := f.service.CancelProjectTaskAttempt(t.Context(), f.bound.Project.ID, accepted.Task.ID, accepted.Attempt.ID, compute.CancelProjectTaskRequest{Binding: wrong}); compute.ErrorCode(err) != "binding_changed" {
		t.Fatalf("cancel adopted another executor binding: %v", err)
	}
}

func TestProjectTaskAdmissionHonorsVMAndReservedLogCapacity(t *testing.T) {
	f := newTaskFixture(t)
	request := compute.SubmitProjectTaskRequest{RequestID: "resource-limit-original", Binding: f.bound.Binding, Spec: compute.NormalizeTaskSpec(compute.TaskSpec{Argv: []string{"true"}})}
	request.Spec.Limits.CPUs = f.machine.Spec.Resources.CPUs + 1
	if _, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, request); compute.ErrorCode(err) != "invalid_request" {
		t.Fatalf("task CPU limit exceeded its VM: %v", err)
	}
	request.Spec.Limits.CPUs = 1
	request.Spec.Limits.MemoryMiB = f.machine.Spec.Resources.MemoryMiB + 1
	if _, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, request); compute.ErrorCode(err) != "invalid_request" {
		t.Fatalf("task memory limit exceeded its VM: %v", err)
	}
	accepted := f.submit(t, "retained-history-original")
	if _, err := f.service.store.db.Exec("UPDATE task_attempts SET reserved_bytes=? WHERE id=?", 256<<20, accepted.Attempt.ID); err != nil {
		t.Fatal(err)
	}
	request.Spec.Limits.MemoryMiB = 64
	if _, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, request); compute.ErrorCode(err) != "capacity_exceeded" {
		t.Fatalf("task history discarded reservation to admit more logs: %v", err)
	}
	receipt, err := f.service.ProjectTaskByRequest(t.Context(), f.bound.Project.ID, "retained-history-original", f.bound.Binding.HostID)
	if err != nil || receipt.Attempt.ID != accepted.Attempt.ID {
		t.Fatalf("quota failure lost an older receipt: %+v %v", receipt, err)
	}
}

func TestProjectTaskMetadataAdmissionPreservesBoundedEscapedHistory(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "metadata-history-original")
	// Model retained history at the largest root/reason escaping expansion.
	// A full list duplicates each task's current attempt as well as its history.
	root := strings.Repeat(strings.Repeat("<", 250)+"/", 16) + strings.Repeat("<", 80)
	if err := compute.ValidateProjectRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.transaction(t.Context(), func(tx *sql.Tx) error {
		for index := range 84 {
			task, attempt := accepted.Task, accepted.Attempt
			if index != 0 {
				task.ID, attempt.ID = compute.NewID("task"), compute.NewID("attempt")
				task.RequestID = compute.NewID("history")
			}
			task.CurrentAttemptID = attempt.ID
			attempt.TaskID, attempt.RequestID = task.ID, task.RequestID
			attempt.Root, attempt.Reason, attempt.SyncError = root, strings.Repeat("<", 4096), strings.Repeat("<", 4096)
			attempt.Status = "failed"
			now := time.Now().UTC()
			attempt.FinishedAt = &now
			if index == 0 {
				if _, err := tx.ExecContext(t.Context(), "UPDATE project_tasks SET data=? WHERE id=?", encode(task), task.ID); err != nil {
					return err
				}
				if err := writeAttempt(t.Context(), tx, attempt); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.ExecContext(t.Context(), "INSERT INTO project_tasks(id,project_id,data) VALUES(?,?,?)", task.ID, task.ProjectID, encode(task)); err != nil {
				return err
			}
			if _, err := tx.ExecContext(t.Context(), "INSERT INTO task_attempts(id,task_id,status,reserved_bytes,updated_at,data) VALUES(?,?,?,?,?,?)", attempt.ID, task.ID, attempt.Status, 1024, now.Format(time.RFC3339Nano), encode(attempt)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, compute.SubmitProjectTaskRequest{RequestID: "metadata-over-capacity", Binding: f.bound.Binding, Spec: accepted.Task.Spec})
	if compute.ErrorCode(err) != "capacity_exceeded" || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("escaped history could grow beyond the client's complete-list budget: %v", err)
	}
	tasks, err := f.service.ProjectTasks(t.Context(), f.bound.Project.ID, f.bound.Binding.HostID)
	if err != nil || len(tasks) != 84 {
		t.Fatalf("metadata admission failed to preserve retained history: %d %v", len(tasks), err)
	}
	list, err := json.Marshal(map[string]any{"tasks": tasks})
	if err != nil || len(list) >= 16<<20 {
		t.Fatalf("complete escaped history exceeds the client response cap: bytes=%d err=%v", len(list), err)
	}
	receipt, err := f.service.ProjectTaskByRequest(t.Context(), f.bound.Project.ID, accepted.Task.RequestID, f.bound.Binding.HostID)
	if err != nil || receipt.Attempt.ID != accepted.Attempt.ID {
		t.Fatalf("metadata rejection discarded an original receipt: %+v %v", receipt, err)
	}
}

func TestProjectTaskMetadataReservationCountsUTF8Bytes(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "unicode-metadata-original")
	if err := f.service.store.transaction(t.Context(), func(tx *sql.Tx) error {
		for index := range 63 {
			task, attempt := accepted.Task, accepted.Attempt
			if index != 0 {
				task.ID, attempt.ID = compute.NewID("task"), compute.NewID("attempt")
			}
			task.RequestID = strings.Repeat("<", 120) + fmt.Sprintf("%08d", index)
			task.Spec.Argv = []string{"true", strings.Repeat("界", 10920)}
			if err := compute.ValidateTaskSpec(task.Spec); err != nil {
				return err
			}
			task.CurrentAttemptID = attempt.ID
			attempt.TaskID, attempt.RequestID = task.ID, task.RequestID
			attempt.Status = "failed"
			now := time.Now().UTC()
			attempt.FinishedAt = &now
			if index == 0 {
				if _, err := tx.ExecContext(t.Context(), "UPDATE project_tasks SET data=? WHERE id=?", encode(task), task.ID); err != nil {
					return err
				}
				if err := writeAttempt(t.Context(), tx, attempt); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.ExecContext(t.Context(), "INSERT INTO project_tasks(id,project_id,data) VALUES(?,?,?)", task.ID, task.ProjectID, encode(task)); err != nil {
				return err
			}
			if _, err := tx.ExecContext(t.Context(), "INSERT INTO task_attempts(id,task_id,status,reserved_bytes,updated_at,data) VALUES(?,?,?,?,?,?)", attempt.ID, task.ID, attempt.Status, 1024, now.Format(time.RFC3339Nano), encode(attempt)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.SubmitProjectTask(t.Context(), f.bound.Project.ID, compute.SubmitProjectTaskRequest{RequestID: "unicode-metadata-over-capacity", Binding: f.bound.Binding, Spec: accepted.Task.Spec})
	if compute.ErrorCode(err) != "capacity_exceeded" || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("UTF-8 character count under-reserved encoded metadata bytes: %v", err)
	}
	var count int
	if err := f.service.store.db.QueryRow("SELECT count(*) FROM project_tasks").Scan(&count); err != nil || count != 63 {
		t.Fatalf("metadata admission changed retained history: %d %v", count, err)
	}
}

func TestProjectTaskVMStopResolvesUnknownWithoutRedirectingCancellation(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "stop-unknown-original")
	f.reconcile(t, accepted.Task.ID)
	f.mu.Lock()
	f.offline = true
	f.mu.Unlock()
	unknown := f.reconcile(t, accepted.Task.ID)
	if unknown.Attempt.Status != "unknown" {
		t.Fatalf("unavailable original guest not visible: %+v", unknown.Attempt)
	}
	if err := f.service.store.transaction(t.Context(), func(tx *sql.Tx) error {
		m, err := machine(t.Context(), tx, f.machine.ID)
		if err != nil {
			return err
		}
		m.Status, m.Desired = "stopped", "stopped"
		return writeMachine(t.Context(), tx, m)
	}); err != nil {
		t.Fatal(err)
	}
	interrupted := f.reconcile(t, accepted.Task.ID)
	if interrupted.Attempt.Status != "interrupted" || interrupted.Attempt.FinishedAt == nil || interrupted.Attempt.Binding != accepted.Attempt.Binding {
		t.Fatalf("confirmed VM stop did not resolve exact old attempt: %+v", interrupted.Attempt)
	}
}
