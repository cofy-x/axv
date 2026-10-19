package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/codex/runner"
	"github.com/cofy-x/axv/apps/server/internal/hostdir"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

// StartInput describes one explicit new-conversation request.
type StartInput struct {
	RequestID   string
	ProjectPath string
	Cols        *int
	Rows        *int
}

// ResumeInput identifies the exact predecessor intended for continuation.
type ResumeInput struct {
	RequestID     string
	ExpectedRunID string
	Cols, Rows    *int
}

func launchInput(requestID string, cols, rows *int) (storage.CodexRunInput, error) {
	in := storage.CodexRunInput{RequestID: requestID, Cols: 80, Rows: 24}
	if cols != nil {
		in.Cols = *cols
	}
	if rows != nil {
		in.Rows = *rows
	}
	if len(requestID) < 1 || len(requestID) > 128 || strings.TrimSpace(requestID) != requestID || !utf8.ValidString(requestID) || strings.ContainsAny(requestID, "\x00\n\r") || !validSize(in.Cols, in.Rows) {
		return in, failure(Invalid, "invalid_session", "Provide a request identifier (1–128 bytes), columns 2–500 and rows 1–200.")
	}
	return in, nil
}

// Create reserves a new conversation and run before launch. Once reserved,
// startup uses the service lifetime so a lost caller can recover the same run.
func (m *Manager) Create(ctx context.Context, request StartInput) (Session, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer done()
	in, err := launchInput(request.RequestID, request.Cols, request.Rows)
	if err != nil {
		return Session{}, err
	}
	in.Mode = "new"
	if request.ProjectPath == "" {
		request.ProjectPath = m.settings.WorkspaceDir
		if err := os.MkdirAll(request.ProjectPath, 0700); err != nil {
			return Session{}, failure(Unavailable, "workspace_unavailable", "The default workspace could not be created. Check its path and permissions in the AXV configuration.")
		}
	}
	in.ProjectPath, err = hostdir.CanonicalDirectory(request.ProjectPath)
	if err != nil {
		return Session{}, err
	}
	if session, found, err := m.previousLaunch(ctx, in); found {
		return m.describe(session), err
	}
	if m.ctx.Err() != nil {
		return Session{}, failure(Unavailable, "server_stopping", "AXV is stopping. Try again after restarting it.")
	}
	info := m.probe(ctx)
	if !info.Ready {
		return Session{}, failure(Unavailable, "codex_unavailable", info.Reason)
	}
	in.HistoryHome, err = historyHome()
	if err != nil {
		return Session{}, failure(Unavailable, "history_unavailable", "The Codex history directory could not be resolved.")
	}
	return m.launch(ctx, info, in)
}

// Resume verifies saved provider history and creates a new run for the exact
// predecessor supplied by the caller. It never substitutes a new conversation.
func (m *Manager) Resume(ctx context.Context, id string, request ResumeInput) (Session, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer done()
	in, err := launchInput(request.RequestID, request.Cols, request.Rows)
	if err != nil {
		return Session{}, err
	}
	if request.ExpectedRunID == "" {
		return Session{}, failure(Invalid, "invalid_session", "Identify the run you intend to continue.")
	}
	in.Mode, in.SessionID, in.ExpectedRunID = "resume", id, request.ExpectedRunID
	if session, found, err := m.previousLaunch(ctx, in); found {
		return m.describe(session), err
	}
	if m.ctx.Err() != nil {
		return Session{}, failure(Unavailable, "server_stopping", "AXV is stopping. Try again after restarting it.")
	}
	session, err := m.store.CodexSession(ctx, in.SessionID)
	if err == nil {
		session, err = m.reconcile(ctx, session)
	}
	if err != nil {
		return Session{}, err
	}
	if !final(session.Status) {
		return Session{}, failure(Conflict, "conversation_active", "This conversation has an active run. Open that run instead.")
	}
	if session.RunID != in.ExpectedRunID {
		return Session{}, failure(Conflict, "run_changed", "This conversation has a newer run. Refresh before continuing it.")
	}
	if session.ConversationID == "" || session.HistoryHome == "" {
		return Session{}, failure(Conflict, "history_unavailable", "The official conversation identity was not recorded for this session.")
	}
	if err := m.awaitRunnerCleanup(ctx, session.ID); err != nil {
		return Session{}, err
	}
	in.ProjectPath, err = hostdir.CanonicalDirectory(session.ProjectPath)
	if err != nil {
		return Session{}, err
	}
	if in.ProjectPath != session.ProjectPath {
		return Session{}, failure(Conflict, "project_changed", "The project directory now resolves to a different location. Restore its original path before continuing.")
	}
	info := m.probe(ctx)
	if !info.Ready {
		return Session{}, failure(Unavailable, "codex_unavailable", info.Reason)
	}
	env, _ := m.environment(ctx, filepath.Dir(info.CodexPath))
	if err = m.verifyHistory(ctx, info.CodexPath, session.ProjectPath, session.HistoryHome, env, session.ConversationID); err != nil {
		if errors.Is(err, runner.ErrUnavailable) {
			return Session{}, failure(Unavailable, "codex_unavailable", "Codex could not verify the saved conversation. Check the host CLI and try again.")
		}
		return Session{}, failure(Conflict, "history_unavailable", "The original Codex conversation could not be verified in its saved history. No replacement conversation was started.")
	}
	in.HistoryHome = session.HistoryHome
	return m.launch(ctx, info, in)
}

// A lost response retries the original operation before rechecking conditions
// that may legitimately have changed since its run was reserved.
func (m *Manager) previousLaunch(ctx context.Context, in storage.CodexRunInput) (storage.CodexSession, bool, error) {
	existing, err := m.store.CodexRequest(ctx, in.RequestID)
	if err == nil {
		err = storage.MatchCodexRequest(existing, in)
		if err == nil {
			existing, err = m.reconcile(ctx, existing)
		}
		return existing, true, err
	}
	var absent *storage.Error
	if !errors.As(err, &absent) || absent.Code != "not_found" {
		return existing, true, err
	}
	return existing, false, nil
}

func (m *Manager) launch(ctx context.Context, info Info, in storage.CodexRunInput) (Session, error) {
	if _, err := m.list(ctx); err != nil {
		return Session{}, err
	}
	in.NamePrefix, in.Socket = m.namePrefix, m.socket
	session, created, err := m.store.ReserveCodexRun(m.ctx, in)
	if err != nil {
		return Session{}, err
	}
	if !created {
		session, err = m.reconcile(ctx, session)
		return m.describe(session), err
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, session.CreatedAt)
	startContext, stopStart := context.WithDeadline(m.ctx, createdAt.Add(20*time.Second))
	err = m.start(startContext, info, session, in.Cols, in.Rows)
	stopStart()
	if err != nil {
		reason := "Codex could not start. Check the host CLI and tmux installation."
		var unavailable *Error
		if errors.As(err, &unavailable) {
			reason = unavailable.Message
		}
		output, cleanupErr := m.sessionTmux(context.Background(), info.TmuxPath, session, "kill-session", "-t", target(session))
		if cleanupErr != nil && !missingRuntime(output) {
			_, saveErr := m.store.UpdateCodexRun(m.ctx, session.RunID, "starting", "Startup and cleanup could not be confirmed. Retry this start action or end this exact run.")
			if saveErr != nil {
				return Session{}, saveErr
			}
			return Session{}, failure(Unavailable, "startup_unconfirmed", "Startup could not be confirmed. Retry this action to recover its existing run or choose End Session.")
		}
		session, err = m.store.UpdateCodexRun(m.ctx, session.RunID, "failed", reason)
		return m.describe(session), err
	}
	session, err = m.store.CodexRun(m.ctx, session.ID, session.RunID)
	if err == nil && final(session.Status) {
		// A concurrent owner may have ended this exact run during setup.
		output, cleanupErr := m.sessionTmux(m.ctx, info.TmuxPath, session, "kill-session", "-t", target(session))
		if cleanupErr != nil && !missingRuntime(output) {
			err = failure(Unavailable, "session_cleanup_failed", "This run ended during startup, but its terminal cleanup could not be confirmed. Check the exact tmux run shown in its details.")
		}
	} else if err == nil {
		session, err = m.reconcile(m.ctx, session)
	}
	return m.describe(session), err
}

// GetRun reads the requested run even when its conversation has a newer one.
func (m *Manager) GetRun(ctx context.Context, id, runID string) (Session, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer done()
	session, err := m.store.CodexRun(ctx, id, runID)
	if err == nil {
		session, err = m.visibleSession(ctx, session)
	}
	if err == nil {
		latest, latestErr := m.store.CodexSession(ctx, session.ID)
		if latestErr == nil && latest.RunID != session.RunID {
			_, latestErr = m.visibleSession(ctx, latest)
			if latestErr == nil {
				session, latestErr = m.store.CodexRun(ctx, session.ID, session.RunID)
			}
		}
		err = latestErr
	}
	return m.describe(session), err
}

// End idempotently terminates only the explicitly addressed conversation/run.
func (m *Manager) End(ctx context.Context, id, runID string) (Session, error) {
	ctx, done, err := m.begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer done()
	release, err := m.runGates.acquire(ctx, runID)
	if err != nil {
		return Session{}, err
	}
	defer release()
	session, err := m.store.CodexRun(ctx, id, runID)
	if err != nil {
		return Session{}, err
	}
	if final(session.Status) {
		return m.describe(session), contextError(m.closeContext(ctx, session))
	}
	// Recover any confirmed identity, but an unreadable runner state must not
	// prevent the user from ending this explicitly selected runtime.
	if synced, _, syncErr := m.syncRunner(ctx, session); syncErr == nil {
		session = synced
	}
	path := executable("tmux")
	if path == "" {
		return Session{}, failure(Unavailable, "tmux_unavailable", "Install tmux to end this run.")
	}
	if startupPending(session) {
		output, inspectErr := m.sessionTmux(ctx, path, session, "has-session", "-t", target(session))
		if inspectErr != nil {
			if missingRuntime(output) {
				return Session{}, failure(Conflict, "run_starting", "This run is still being created. Retry End Session in a moment.")
			}
			return Session{}, failure(Unavailable, "session_unavailable", "The starting run could not be inspected. Try End Session again.")
		}
	}
	session, err = m.store.UpdateCodexRun(m.ctx, session.RunID, "stopping", "")
	if err != nil {
		return Session{}, err
	}
	contextCloseErr := m.closeContext(ctx, session)
	output, killErr := m.sessionTmux(m.ctx, path, session, "kill-session", "-t", target(session))
	if killErr != nil && !missingRuntime(output) {
		return Session{}, failure(Unavailable, "end_failed", "The Codex run could not be ended. Try again.")
	}
	if err := m.awaitRunnerCleanup(ctx, session.ID); err != nil {
		return m.describe(session), err
	}
	session, err = m.store.UpdateCodexRun(m.ctx, session.RunID, "ended", "Session ended.")
	if err == nil {
		err = contextError(contextCloseErr)
	}
	return m.describe(session), err
}
