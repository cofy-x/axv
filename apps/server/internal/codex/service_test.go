package codex

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func rejectServiceHostWork(t *testing.T, manager *Manager) {
	t.Helper()
	manager.probe = func(context.Context) Info {
		t.Fatal("operation unexpectedly inspected the host")
		return Info{}
	}
	manager.run = func(context.Context, string, ...string) (string, error) {
		t.Fatal("operation unexpectedly executed tmux")
		return "", nil
	}
}

func requireServiceError(t *testing.T, err error, kind ErrorKind, code string) {
	t.Helper()
	var feature *Error
	if !errors.As(err, &feature) || feature.Kind != kind || feature.Code != code || feature.Message == "" {
		t.Fatalf("unexpected service error: %v", err)
	}
}

func serviceRunningRun(t *testing.T, manager *Manager, request string) storage.CodexSession {
	t.Helper()
	ctx := context.Background()
	run, _, err := manager.store.ReserveCodexRun(ctx, storage.CodexRunInput{
		Mode: "new", RequestID: request, ProjectPath: "/service/project", HistoryHome: "/service/history",
		NamePrefix: manager.namePrefix, Socket: manager.socket, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.store.BindCodexConversation(ctx, run.RunID, "01234567-89ab-cdef-0123-456789abcdef", run.HistoryHome); err != nil {
		t.Fatal(err)
	}
	run, err = manager.store.StartCodexRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestServiceLaunchValidationPrecedesHostWorkAndReservation(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	ctx := context.Background()
	narrow, tall := 1, 201
	for _, request := range []StartInput{
		{},
		{RequestID: " padded "},
		{RequestID: string([]byte{0xff})},
		{RequestID: "line\nbreak"},
		{RequestID: "too-narrow", Cols: &narrow},
		{RequestID: "too-tall", Rows: &tall},
	} {
		_, err := manager.Create(ctx, request)
		requireServiceError(t, err, Invalid, "invalid_session")
	}
	for _, request := range []ResumeInput{
		{ExpectedRunID: "prior"},
		{RequestID: "missing-predecessor"},
		{RequestID: "invalid-size", ExpectedRunID: "prior", Cols: &narrow},
	} {
		_, err := manager.Resume(ctx, "conversation", request)
		requireServiceError(t, err, Invalid, "invalid_session")
	}
	sessions, err := manager.store.CodexSessions(ctx)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("invalid launch reserved durable state: %+v %v", sessions, err)
	}
	if _, err := os.Stat(manager.settings.WorkspaceDir); !os.IsNotExist(err) {
		t.Fatalf("invalid launch created the default workspace: %v", err)
	}
}

func TestServiceMetadataUsesStorageRevisionsWithoutHostWork(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	ctx := context.Background()
	projects, err := manager.Projects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("unexpected initial projects: %+v %v", projects, err)
	}
	run := serviceRunningRun(t, manager, "metadata-service")
	projects, err = manager.Projects(ctx)
	if err != nil || len(projects) != 1 || projects[0].Path != run.ProjectPath || projects[0].LastUsedAt == "" {
		t.Fatalf("confirmed project missing from service: %+v %v", projects, err)
	}
	title, archive := "  中文 🚀  ", true
	patched, err := manager.Patch(ctx, run.ID, storage.CodexMetadataPatch{Revision: run.MetadataRevision, Title: &title, Archived: &archive})
	if err != nil || patched.Title != strings.TrimSpace(title) || patched.MetadataRevision != run.MetadataRevision+1 || patched.ArchivedAt == "" || patched.Status != run.Status || patched.RunID != run.RunID || patched.AttachCommand == "" {
		t.Fatalf("metadata changed runtime identity or lost its revision: %+v %v", patched, err)
	}
	title, archive = "stale title", false
	_, err = manager.Patch(ctx, run.ID, storage.CodexMetadataPatch{Revision: run.MetadataRevision, Title: &title, Archived: &archive})
	var conflict *storage.Error
	if !errors.As(err, &conflict) || conflict.Code != "metadata_conflict" {
		t.Fatalf("stale revision was accepted: %v", err)
	}
	title = "invalid\ntitle"
	_, err = manager.Patch(ctx, run.ID, storage.CodexMetadataPatch{Revision: patched.MetadataRevision, Title: &title, Archived: &archive})
	var invalid *storage.Error
	if !errors.As(err, &invalid) || invalid.Code != "invalid_metadata" {
		t.Fatalf("invalid title was accepted: %v", err)
	}
	kept, err := manager.store.CodexSession(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(kept, patched.CodexSession) {
		t.Fatalf("rejected metadata partially changed the stored session: %+v %v", kept, err)
	}
	if _, err := os.Stat(manager.settings.WorkspaceDir); !os.IsNotExist(err) {
		t.Fatalf("metadata created the default workspace: %v", err)
	}
}

func serviceOperations(manager *Manager) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"info":     func(ctx context.Context) error { _, err := manager.Info(ctx); return err },
		"list":     func(ctx context.Context) error { _, err := manager.List(ctx); return err },
		"projects": func(ctx context.Context) error { _, err := manager.Projects(ctx); return err },
		"context list": func(ctx context.Context) error {
			_, err := manager.ContextBatches(ctx, "conversation", "run")
			return err
		},
		"context queue": func(ctx context.Context) error {
			_, err := manager.QueueContext(ctx, "conversation", "run", ContextInput{RequestID: "context-request", SnapshotIDs: []string{"snapshot"}})
			return err
		},
		"context cancel": func(ctx context.Context) error {
			_, err := manager.CancelContext(ctx, "conversation", "run", "batch")
			return err
		},
		"create": func(ctx context.Context) error {
			_, err := manager.Create(ctx, StartInput{RequestID: "new-service-request"})
			return err
		},
		"resume": func(ctx context.Context) error {
			_, err := manager.Resume(ctx, "conversation", ResumeInput{RequestID: "resume-service-request", ExpectedRunID: "prior"})
			return err
		},
		"get":     func(ctx context.Context) error { _, err := manager.Get(ctx, "conversation"); return err },
		"get run": func(ctx context.Context) error { _, err := manager.GetRun(ctx, "conversation", "run"); return err },
		"end":     func(ctx context.Context) error { _, err := manager.End(ctx, "conversation", "run"); return err },
		"patch": func(ctx context.Context) error {
			title := "changed title"
			_, err := manager.Patch(ctx, "conversation", storage.CodexMetadataPatch{Revision: 1, Title: &title})
			return err
		},
	}
}

func TestServiceRejectsCanceledCallersAndWorkAfterClose(t *testing.T) {
	for _, closed := range []bool{false, true} {
		name := "canceled caller"
		if closed {
			name = "closed service"
		}
		t.Run(name, func(t *testing.T) {
			manager := testManager(t)
			rejectServiceHostWork(t, manager)
			ctx := context.Background()
			if closed {
				manager.Close()
			} else {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			for name, operation := range serviceOperations(manager) {
				t.Run(name, func(t *testing.T) {
					err := operation(ctx)
					if closed {
						requireServiceError(t, err, Unavailable, "server_stopping")
					} else if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled caller entered service work: %v", err)
					}
				})
			}
			sessions, err := manager.store.CodexSessions(context.Background())
			if err != nil || len(sessions) != 0 {
				t.Fatalf("rejected service work changed storage: %+v %v", sessions, err)
			}
			if _, err := os.Stat(manager.settings.WorkspaceDir); !os.IsNotExist(err) {
				t.Fatalf("rejected service work created a workspace: %v", err)
			}
		})
	}
}

func TestServiceInspectionCancelsWithCallerAndShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "caller cancellation"
		if shutdown {
			name = "service shutdown"
		}
		t.Run(name, func(t *testing.T) {
			manager := testManager(t)
			entered := make(chan struct{})
			manager.probe = func(ctx context.Context) Info {
				close(entered)
				<-ctx.Done()
				return Info{Reason: "interrupted inspection"}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() { _, err := manager.Info(ctx); finished <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("ordinary inspection did not start")
			}
			closed := make(chan struct{})
			if shutdown {
				go func() { manager.Close(); close(closed) }()
			} else {
				cancel()
				close(closed)
			}
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("interrupted inspection reported success: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("ordinary inspection ignored cancellation")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("service shutdown did not drain ordinary inspection")
			}
		})
	}
}

func TestServiceHistoricalRunReadAndEndKeepNewerRunIdentity(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery; runtime inspection is injected")
	}
	manager := testManager(t)
	ctx := context.Background()
	first := serviceRunningRun(t, manager, "first-service-run")
	first, err := manager.store.UpdateCodexRun(ctx, first.RunID, "ended", "First run ended.")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := manager.store.ReserveCodexRun(ctx, storage.CodexRunInput{
		Mode: "resume", RequestID: "second-service-run", SessionID: first.ID, ExpectedRunID: first.RunID,
		NamePrefix: manager.namePrefix, Socket: "second-recorded-socket", Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err = manager.store.StartCodexRun(ctx, second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	manager.probe = func(context.Context) Info {
		t.Fatal("historical operation probed the host")
		return Info{}
	}
	manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if !slices.Contains(args, "list-panes") || !slices.Contains(args, "="+second.TmuxSession+":0") || !slices.Contains(args, second.TmuxSocket) {
			t.Fatalf("historical operation changed or retargeted a runtime: %q", args)
		}
		return second.TmuxSession + "\t0\t0:", nil
	}
	historical, err := manager.GetRun(ctx, first.ID, first.RunID)
	if err != nil || historical.RunID != first.RunID || historical.Status != first.Status || historical.TmuxSession != first.TmuxSession || historical.Continuation.State != "active" || historical.Continuation.ActiveRunID != second.RunID {
		t.Fatalf("historical read adopted the newer run: %+v %v", historical, err)
	}
	ended, err := manager.End(ctx, first.ID, first.RunID)
	if err != nil || ended.RunID != first.RunID || ended.Status != "ended" {
		t.Fatalf("historical End changed its target: %+v %v", ended, err)
	}
	_, err = manager.End(ctx, "different-conversation", second.RunID)
	var absent *storage.Error
	if !errors.As(err, &absent) || absent.Code != "not_found" {
		t.Fatalf("run escaped its conversation: %v", err)
	}
	_, err = manager.Resume(ctx, first.ID, ResumeInput{RequestID: "stale-resume", ExpectedRunID: first.RunID})
	requireServiceError(t, err, Conflict, "conversation_active")
	kept, err := manager.store.CodexRun(ctx, second.ID, second.RunID)
	if err != nil || !reflect.DeepEqual(kept, second) {
		t.Fatalf("historical operation changed the newer run: %+v %v", kept, err)
	}
	_, err = manager.store.CodexRequest(ctx, "stale-resume")
	if !errors.As(err, &absent) || absent.Code != "not_found" {
		t.Fatalf("rejected continuation reserved another run: %v", err)
	}
}

type serviceLeaseProcess struct {
	reader  *io.PipeReader
	entered chan struct{}
	done    chan struct{}
	reading sync.Once
	closing sync.Once
}

func (p *serviceLeaseProcess) Read(data []byte) (int, error) {
	p.reading.Do(func() { close(p.entered) })
	return p.reader.Read(data)
}
func (*serviceLeaseProcess) Write(data []byte) (int, error) { return len(data), nil }
func (*serviceLeaseProcess) Resize(int, int) error          { return nil }
func (p *serviceLeaseProcess) Done() <-chan struct{}        { return p.done }
func (*serviceLeaseProcess) ExitError() error               { return nil }
func (p *serviceLeaseProcess) Close() {
	p.closing.Do(func() {
		_ = p.reader.Close()
		close(p.done)
	})
}

func TestServiceAttachmentDetachRetainsCapacityUntilCloseAndShutdownDrainsLeases(t *testing.T) {
	if executable("tmux") == "" {
		t.Skip("requires tmux discovery; no native attachment is started")
	}
	manager := testManager(t)
	ctx := context.Background()
	run := serviceRunningRun(t, manager, "attachment-leases")
	manager.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if !slices.Contains(args, "list-panes") || !slices.Contains(args, "="+run.TmuxSession+":0") {
			t.Fatalf("pending lease unexpectedly controlled a native runtime: %q", args)
		}
		return run.TmuxSession + "\t0\t0:", nil
	}
	prepare := func(parent context.Context) *Attachment {
		t.Helper()
		lease, err := manager.PrepareAttachment(parent, run.ID, run.RunID, 80, 24)
		if err != nil {
			t.Fatalf("reserve attachment lease: %v", err)
		}
		t.Cleanup(lease.Close)
		return lease
	}
	installProcess := func(lease *Attachment) *serviceLeaseProcess {
		t.Helper()
		reader, writer := io.Pipe()
		t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
		p := &serviceLeaseProcess{reader: reader, entered: make(chan struct{}), done: make(chan struct{})}
		lease.mu.Lock()
		lease.process = p
		lease.opened = true
		lease.mu.Unlock()
		return p
	}
	full := func() {
		t.Helper()
		lease, err := manager.PrepareAttachment(ctx, run.ID, run.RunID, 80, 24)
		if lease != nil {
			lease.Close()
			t.Fatal("pending or detached lease released capacity before Close")
		}
		requireServiceError(t, err, Unavailable, "connection_limit")
	}
	first := prepare(ctx)
	process := installProcess(first)
	parent, cancelParent := context.WithCancel(ctx)
	defer cancelParent()
	second := prepare(parent)
	canceledProcess := installProcess(second)
	leases := []*Attachment{first, second}
	for range 6 {
		leases = append(leases, prepare(ctx))
	}
	full()
	readFinished := make(chan error, 1)
	go func() { _, err := first.Read(make([]byte, 1)); readFinished <- err }()
	select {
	case <-process.entered:
	case <-time.After(time.Second):
		t.Fatal("attachment read did not start")
	}
	first.Detach()
	select {
	case err := <-readFinished:
		if err == nil {
			t.Fatal("detach did not interrupt native I/O")
		}
	case <-time.After(time.Second):
		t.Fatal("detach left native I/O blocked")
	}
	if _, err := first.Inspect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("detached lease retained an active operation context: %v", err)
	}
	full()
	cancelParent()
	select {
	case <-canceledProcess.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not detach native I/O")
	}
	full()
	first.Close()
	first.Close()
	replacement := prepare(ctx)
	leases = append(leases, replacement)
	full()
	closed := make(chan struct{})
	go func() { manager.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("service shutdown did not release pending attachment leases")
	}
	for _, lease := range leases {
		select {
		case <-lease.Done():
		default:
			t.Fatal("service shutdown retained a pending attachment")
		}
		lease.Close()
	}
	kept, err := manager.store.CodexRun(ctx, run.ID, run.RunID)
	if err != nil || !reflect.DeepEqual(kept, run) {
		t.Fatalf("attachment lifetime changed the persistent run: %+v %v", kept, err)
	}
}
