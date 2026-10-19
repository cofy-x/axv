package codex

import (
	"context"
	"io"
	"sync"

	"github.com/cofy-x/axv/apps/server/internal/codex/internal/tmux"
)

// Attachment reserves one client slot for an exact run. Open starts its PTY
// client after a transport is ready; Close releases the slot without ending it.
type Attachment struct {
	manager      *Manager
	path         string
	target       tmux.Target
	runID        string
	cols, rows   int
	ctx          context.Context
	cancel       context.CancelFunc
	stopService  func() bool
	mu           sync.Mutex
	process      process
	opened       bool
	closed       bool
	once         sync.Once
	detachOnce   sync.Once
	closedSignal chan struct{}
}

// PrepareAttachment reserves capacity before a transport upgrade or setup.
func (m *Manager) PrepareAttachment(parent context.Context, id, runID string, cols, rows int) (*Attachment, error) {
	ctx, done, err := m.begin(parent)
	if err != nil {
		return nil, err
	}
	defer done()
	if !validSize(cols, rows) {
		return nil, failure(Invalid, "invalid_terminal_size", "Use cols 2–500 and rows 1–200; no other query parameters are accepted.")
	}
	session, err := m.store.CodexRun(ctx, id, runID)
	if err == nil {
		session, err = m.reconcile(ctx, session)
	}
	if err != nil {
		return nil, err
	}
	if final(session.Status) {
		return nil, failure(Conflict, "session_ended", session.Reason)
	}
	path := executable("tmux")
	if path == "" {
		return nil, failure(Unavailable, "tmux_unavailable", "Install tmux to reconnect this session.")
	}
	leaseCtx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(m.ctx, cancel)
	a := &Attachment{
		manager:      m,
		path:         path,
		target:       runtimeTarget(session),
		runID:        session.RunID,
		cols:         cols,
		rows:         rows,
		ctx:          leaseCtx,
		cancel:       cancel,
		stopService:  stop,
		closedSignal: make(chan struct{}),
	}
	m.mu.Lock()
	if m.closed || m.attachments >= 8 {
		m.mu.Unlock()
		stop()
		cancel()
		return nil, failure(Unavailable, "connection_limit", "All eight Codex connections are in use or AXV is stopping. Close another view and try again.")
	}
	m.attachments++
	m.leases[a] = true
	m.wg.Add(1)
	m.mu.Unlock()
	context.AfterFunc(leaseCtx, a.Detach)
	return a, nil
}

func (a *Attachment) operation(parent context.Context) (context.Context, func(), error) {
	ctx, done, err := a.manager.begin(parent)
	if err != nil {
		return nil, nil, err
	}
	if err := a.ctx.Err(); err != nil {
		done()
		return nil, nil, err
	}
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(a.ctx, cancel)
	return child, func() { stop(); cancel(); done() }, nil
}

// Open joins the recorded run and restores live input before attaching.
func (a *Attachment) Open(parent context.Context) error {
	ctx, done, err := a.operation(parent)
	if err != nil {
		return err
	}
	defer done()
	a.mu.Lock()
	if a.closed || a.opened {
		a.mu.Unlock()
		return io.ErrClosedPipe
	}
	a.opened = true
	a.mu.Unlock()
	m := a.manager
	m.mu.Lock()
	if m.closed || a.ctx.Err() != nil {
		m.mu.Unlock()
		return context.Canceled
	}
	if m.connections[a.runID] == nil {
		m.connections[a.runID] = make(map[*Attachment]bool)
	}
	m.connections[a.runID][a] = true
	m.mu.Unlock()
	release, err := m.inputGates.acquire(ctx, a.runID)
	if err == nil {
		err = m.scroll(ctx, a.path, a.runID, 0)
		release()
	}
	if err != nil {
		return failure(Unavailable, "attach_failed", "Could not restore live Codex input.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := m.backend().Attach(a.path, a.target, a.cols, a.rows)
	if err != nil {
		m.settings.Logger.Error("codex.attach_failed", err, "runId", a.runID, "executable", a.path)
		return failure(Unavailable, "attach_failed", "Could not attach to the persistent Codex session.")
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		p.Close()
		return context.Canceled
	}
	a.process = p
	a.mu.Unlock()
	return nil
}

func (a *Attachment) current() process { a.mu.Lock(); defer a.mu.Unlock(); return a.process }

func (a *Attachment) Read(data []byte) (int, error) {
	p := a.current()
	if p == nil {
		return 0, io.ErrClosedPipe
	}
	return p.Read(data)
}

// Inspect reconciles this exact run for an attachment. Unlike list/detail
// views, terminal clients must receive runtime availability failures directly.
func (a *Attachment) Inspect(parent context.Context) (Session, error) {
	ctx, done, err := a.operation(parent)
	if err != nil {
		return Session{}, err
	}
	defer done()
	session, err := a.manager.store.CodexRunByID(ctx, a.runID)
	if err == nil {
		session, err = a.manager.reconcile(ctx, session)
	}
	return a.manager.describe(session), err
}

func (a *Attachment) apply(parent context.Context, frame inputFrame) error {
	ctx, done, err := a.operation(parent)
	if err != nil {
		return err
	}
	defer done()
	p := a.current()
	if p == nil {
		return io.ErrClosedPipe
	}
	return a.manager.applyFrame(ctx, p, a.path, a.runID, frame)
}

func (a *Attachment) Write(ctx context.Context, data []byte) error {
	return a.apply(ctx, inputFrame{data: data})
}
func (a *Attachment) Resize(ctx context.Context, cols, rows int) error {
	if !validSize(cols, rows) {
		return failure(Invalid, "invalid_terminal_size", "Use columns 2–500 and rows 1–200.")
	}
	return a.apply(ctx, inputFrame{cols: cols, rows: rows})
}
func (a *Attachment) Scroll(ctx context.Context, lines int) error {
	if lines < -200 || lines > 200 {
		return failure(Invalid, "invalid_scroll", "Scroll must be an integer from -200 to 200 without resize.")
	}
	return a.apply(ctx, inputFrame{scroll: &lines})
}
func (a *Attachment) Done() <-chan struct{} {
	if p := a.current(); p != nil {
		return p.Done()
	}
	return a.closedSignal
}
func (a *Attachment) ExitError() error {
	if p := a.current(); p != nil {
		return p.ExitError()
	}
	return nil
}

// Detach interrupts native I/O so a transport can drain its workers. The client
// slot remains reserved until Close, after that transport has finished draining.
func (a *Attachment) Detach() {
	a.detachOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		p := a.process
		a.mu.Unlock()
		a.cancel()
		if p != nil {
			p.Close()
		}
	})
}

func (a *Attachment) Close() {
	a.once.Do(func() {
		a.Detach()
		a.stopService()
		m := a.manager
		m.mu.Lock()
		delete(m.leases, a)
		delete(m.connections[a.runID], a)
		if len(m.connections[a.runID]) == 0 {
			delete(m.connections, a.runID)
			m.copyModes.Delete(a.runID)
		}
		m.attachments--
		m.mu.Unlock()
		close(a.closedSignal)
		m.wg.Done()
	})
}
