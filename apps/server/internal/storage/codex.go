package storage

import (
	"context"
	"database/sql"
	"errors"
)

// CodexSession combines stable conversation metadata with one exact run.
type CodexSession struct {
	ID               string `json:"id"`
	RunID            string `json:"runId"`
	RequestID        string `json:"-"`
	PreviousRunID    string `json:"-"`
	RequestCols      int    `json:"-"`
	RequestRows      int    `json:"-"`
	Target           string `json:"target"`
	ProjectPath      string `json:"projectPath"`
	ConversationID   string `json:"conversationId,omitempty"`
	HistoryHome      string `json:"historyHome,omitempty"`
	Status           string `json:"status"`
	CreatedAt        string `json:"createdAt"`
	EndedAt          string `json:"endedAt,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Connections      int    `json:"connections"`
	TmuxSession      string `json:"tmuxSession"`
	TmuxSocket       string `json:"tmuxSocket"`
	Title            string `json:"title"`
	ArchivedAt       string `json:"archivedAt,omitempty"`
	MetadataRevision int    `json:"metadataRevision"`
}

func (s CodexSession) RunMode() string {
	if s.PreviousRunID == "" {
		return "new"
	}
	return "resume"
}

const codexSelect = `SELECT s.id,r.id,r.request_id,COALESCE(r.previous_run_id,''),r.request_cols,r.request_rows,
 s.project_path,s.conversation_id,s.history_home,r.status,r.created_at,r.ended_at,r.reason,
 r.tmux_session,r.tmux_socket,s.title,s.archived_at,s.metadata_revision
 FROM codex_sessions s JOIN codex_runs r ON r.session_id=s.id`

func scanCodex(row interface{ Scan(...any) error }) (CodexSession, error) {
	s := CodexSession{Target: "host"}
	err := row.Scan(&s.ID, &s.RunID, &s.RequestID, &s.PreviousRunID, &s.RequestCols, &s.RequestRows,
		&s.ProjectPath, &s.ConversationID, &s.HistoryHome, &s.Status, &s.CreatedAt, &s.EndedAt, &s.Reason,
		&s.TmuxSession, &s.TmuxSocket, &s.Title, &s.ArchivedAt, &s.MetadataRevision)
	if errors.Is(err, sql.ErrNoRows) {
		err = problem("not_found", "The Codex session or run does not exist.")
	}
	return s, err
}

func (s *Store) DatabaseID(ctx context.Context) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='databaseId'").Scan(&value)
	return value, err
}

func (s *Store) CodexSessions(ctx context.Context) ([]CodexSession, error) {
	return s.codexQuery(ctx, codexSelect+" WHERE r.id=s.latest_run_id ORDER BY s.created_at DESC,s.id")
}

func (s *Store) codexQuery(ctx context.Context, query string, args ...any) ([]CodexSession, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CodexSession{}
	for rows.Next() {
		session, err := scanCodex(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, rows.Err()
}

func (s *Store) CodexSession(ctx context.Context, sessionID string) (CodexSession, error) {
	return scanCodex(s.db.QueryRowContext(ctx, codexSelect+" WHERE s.id=? AND r.id=s.latest_run_id", sessionID))
}

func (s *Store) CodexRun(ctx context.Context, sessionID, runID string) (CodexSession, error) {
	return scanCodex(s.db.QueryRowContext(ctx, codexSelect+" WHERE s.id=? AND r.id=?", sessionID, runID))
}

func (s *Store) CodexRunByID(ctx context.Context, runID string) (CodexSession, error) {
	return scanCodex(s.db.QueryRowContext(ctx, codexSelect+" WHERE r.id=?", runID))
}

func (s *Store) CodexRequest(ctx context.Context, requestID string) (CodexSession, error) {
	return scanCodex(s.db.QueryRowContext(ctx, codexSelect+" WHERE r.request_id=?", requestID))
}

func (s *Store) ActiveCodexRun(ctx context.Context, sessionID string) (CodexSession, error) {
	return scanCodex(s.db.QueryRowContext(ctx, codexSelect+" WHERE s.id=? AND r.status IN ('starting','running','stopping')", sessionID))
}
