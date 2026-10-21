package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

type CodexRunInput struct {
	RequestID, Mode, SessionID, ExpectedRunID    string
	ProjectPath, NamePrefix, Socket, HistoryHome string
	Cols, Rows                                   int
	VMProjectBinding                             *compute.ProjectBinding
	VMProject                                    *vmwork.Project
	VMSocket, VMConfigFingerprint                string
}

func MatchCodexRequest(session CodexSession, in CodexRunInput) error {
	if session.RunMode() != in.Mode || session.PreviousRunID != in.ExpectedRunID ||
		(in.Mode == "resume" && session.ID != in.SessionID) ||
		(in.Mode == "new" && in.VMProjectBinding == nil && session.ProjectPath != in.ProjectPath) ||
		(session.VMProject == nil) != (in.VMProjectBinding == nil) ||
		(session.VMProject != nil && session.VMProject.Binding != *in.VMProjectBinding) ||
		session.RequestCols != in.Cols || session.RequestRows != in.Rows {
		return problem("request_conflict", "This request identifier already belongs to a different start action.")
	}
	return nil
}

// ReserveCodexRun serializes capacity and identity checks across every service
// using this database before any process can be started.
func (s *Store) ReserveCodexRun(ctx context.Context, in CodexRunInput) (CodexSession, bool, error) {
	var session CodexSession
	created := false
	if in.Mode != "new" && in.Mode != "resume" {
		return session, false, problem("invalid_session", "Choose a new session or an explicit continuation.")
	}
	if in.Mode == "new" && (in.SessionID != "" || in.ExpectedRunID != "") ||
		in.Mode == "resume" && (in.SessionID == "" || in.ExpectedRunID == "") {
		return session, false, problem("invalid_session", "A continuation must identify its conversation and previous run.")
	}
	if in.NamePrefix == "" || in.Mode == "new" && in.HistoryHome == "" {
		return session, false, problem("invalid_session", "A managed run requires a tmux name prefix and conversation history directory.")
	}
	var vmProject string
	if in.VMProjectBinding != nil {
		if in.VMProject == nil || in.VMProject.Binding != *in.VMProjectBinding || vmwork.ValidateProject(*in.VMProject) != nil || !filepath.IsAbs(in.VMSocket) || in.VMConfigFingerprint == "" {
			return session, false, problem("invalid_session", "A VM project run requires its verified project and original compute connection.")
		}
		data, err := json.Marshal(in.VMProject)
		if err != nil {
			return session, false, err
		}
		vmProject = string(data)
	} else if in.VMProject != nil || in.VMSocket != "" || in.VMConfigFingerprint != "" {
		return session, false, problem("invalid_session", "A host project run cannot contain a VM connection.")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		// Acquire SQLite's writer reservation before reading capacity. A deferred
		// read transaction could otherwise race a second service's reservation.
		if _, err := tx.ExecContext(ctx, "UPDATE settings SET value=value WHERE key='databaseId'"); err != nil {
			return err
		}
		existing, err := scanCodex(tx.QueryRowContext(ctx, codexSelect+" WHERE r.request_id=?", in.RequestID))
		if err == nil {
			session = existing
			return MatchCodexRequest(session, in)
		}
		var absent *Error
		if !errors.As(err, &absent) || absent.Code != "not_found" {
			return err
		}
		if in.Mode == "resume" {
			session, err = scanCodex(tx.QueryRowContext(ctx, codexSelect+" WHERE s.id=? AND r.id=s.latest_run_id", in.SessionID))
			if err != nil {
				return err
			}
			if !codexFinal(session.Status) {
				return problem("conversation_active", "This conversation already has an active run. Open that run instead.")
			}
			if session.RunID != in.ExpectedRunID {
				return problem("run_changed", "This conversation has a newer run. Refresh before continuing it.")
			}
			if session.ConversationID == "" || session.HistoryHome == "" {
				return problem("history_unavailable", "The official conversation identity was not recorded for this session.")
			}
			if !sameCodexWorkProject(session.VMProject, in.VMProject) {
				return problem("project_changed", "This conversation belongs to a different work project. Start a separate conversation instead.")
			}
		} else {
			session = CodexSession{ID: id(), Target: "host", ProjectPath: in.ProjectPath, HistoryHome: in.HistoryHome, MetadataRevision: 1}
		}
		var active int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM codex_runs WHERE status IN ('starting','running','stopping')").Scan(&active); err != nil {
			return err
		}
		if active >= 4 {
			return problem("codex_limit", "Four Codex runs are active. End a run before starting another.")
		}
		now := codexNow()
		session.RunID, session.RequestID = id(), in.RequestID
		session.PreviousRunID, session.RequestCols, session.RequestRows = in.ExpectedRunID, in.Cols, in.Rows
		session.Status, session.CreatedAt = "starting", now
		session.VMProject, session.VMSocket, session.VMConfigFingerprint = in.VMProject, in.VMSocket, in.VMConfigFingerprint
		session.EndedAt, session.Reason = "", ""
		session.TmuxSession, session.TmuxSocket = in.NamePrefix+"-"+session.RunID, in.Socket
		if in.Mode == "new" {
			_, err = tx.ExecContext(ctx, "INSERT INTO codex_sessions(id,project_path,created_at,history_home,latest_run_id) VALUES(?,?,?,?,?)", session.ID, session.ProjectPath, now, session.HistoryHome, session.RunID)
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE codex_sessions SET latest_run_id=? WHERE id=?", session.RunID, session.ID)
		}
		if err != nil {
			return err
		}
		var previousRunID any
		if session.PreviousRunID != "" {
			previousRunID = session.PreviousRunID
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO codex_runs(id,session_id,request_id,previous_run_id,request_cols,request_rows,status,created_at,tmux_session,tmux_socket,vm_project,vm_socket,vm_config_fingerprint)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, session.RunID, session.ID, session.RequestID, previousRunID, session.RequestCols, session.RequestRows, session.Status, now, session.TmuxSession, session.TmuxSocket, vmProject, session.VMSocket, session.VMConfigFingerprint)
		created = err == nil
		return err
	})
	return session, created && err == nil, err
}

func sameCodexWorkProject(a, b *vmwork.Project) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return vmwork.SameProjectIdentity(*a, *b)
}

func codexFinal(status string) bool {
	return status == "ended" || status == "lost" || status == "failed"
}
func codexNow() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

// UpdateCodexRun cannot revive a terminal run or retarget its conversation.
func (s *Store) UpdateCodexRun(ctx context.Context, runID, status, reason string) (CodexSession, error) {
	var session CodexSession
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE settings SET value=value WHERE key='databaseId'"); err != nil {
			return err
		}
		var err error
		session, err = scanCodex(tx.QueryRowContext(ctx, codexSelect+" WHERE r.id=?", runID))
		if err != nil || codexFinal(session.Status) {
			return err
		}
		if !codexFinal(status) && status != "stopping" && status != session.Status {
			return problem("invalid_run_state", "This Codex run cannot make the requested state transition.")
		}
		now := codexNow()
		ended := ""
		if codexFinal(status) {
			ended = now
		}
		_, err = tx.ExecContext(ctx, "UPDATE codex_runs SET status=?,reason=?,ended_at=? WHERE id=?", status, reason, ended, runID)
		if err == nil {
			session.Status, session.Reason, session.EndedAt = status, reason, ended
		}
		return err
	})
	return session, err
}

func (s *Store) SetCodexRunSocket(ctx context.Context, runID, socket string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE codex_runs SET tmux_socket=? WHERE id=? AND status='starting'", socket, runID)
	return err
}

// BindCodexConversation accepts only the exact identity confirmed by this run.
func (s *Store) BindCodexConversation(ctx context.Context, runID, threadID, home string) (CodexSession, error) {
	var session CodexSession
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE settings SET value=value WHERE key='databaseId'"); err != nil {
			return err
		}
		var err error
		session, err = scanCodex(tx.QueryRowContext(ctx, codexSelect+" WHERE r.id=?", runID))
		if err != nil {
			return err
		}
		if threadID == "" || home == "" || session.HistoryHome != home || (session.ConversationID != "" && session.ConversationID != threadID) {
			return problem("history_identity_conflict", "The runner reported a different official conversation identity.")
		}
		var duplicate bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM codex_sessions WHERE history_home=? AND conversation_id=? AND id<>?)", home, threadID, session.ID).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			return problem("history_identity_conflict", "This official conversation is already managed by another AXV session.")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE codex_sessions SET conversation_id=? WHERE id=?", threadID, session.ID); err != nil {
			return err
		}
		session.ConversationID = threadID
		return nil
	})
	return session, err
}

// StartCodexRun commits the first confirmed start and recent project together.
func (s *Store) StartCodexRun(ctx context.Context, runID string) (CodexSession, error) {
	var session CodexSession
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE settings SET value=value WHERE key='databaseId'"); err != nil {
			return err
		}
		var err error
		session, err = scanCodex(tx.QueryRowContext(ctx, codexSelect+" WHERE r.id=?", runID))
		if err != nil || session.Status != "starting" {
			return err
		}
		if session.ConversationID == "" {
			return problem("history_unavailable", "The official conversation identity has not been confirmed.")
		}
		now := codexNow()
		if _, err = tx.ExecContext(ctx, "UPDATE codex_runs SET status='running',reason='' WHERE id=?", runID); err != nil {
			return err
		}
		if session.VMProject == nil {
			if _, err = tx.ExecContext(ctx, "INSERT INTO codex_projects(path,last_used_at) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET last_used_at=excluded.last_used_at", session.ProjectPath, now); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM codex_projects WHERE path NOT IN (SELECT path FROM codex_projects ORDER BY last_used_at DESC,path LIMIT 10)"); err != nil {
				return err
			}
		}
		session.Status, session.Reason = "running", ""
		return nil
	})
	return session, err
}
