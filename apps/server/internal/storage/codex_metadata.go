package storage

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
	"unicode/utf8"
)

type CodexMetadataPatch struct {
	Revision int
	Title    *string
	Archived *bool
}

// PatchCodexMetadata changes presentation only, independently of process state.
func (s *Store) PatchCodexMetadata(ctx context.Context, sessionID string, patch CodexMetadataPatch) (CodexSession, error) {
	if patch.Revision < 1 || patch.Title == nil && patch.Archived == nil {
		return CodexSession{}, problem("invalid_metadata", "Provide a positive metadata revision and a title or archived value.")
	}
	var title string
	if patch.Title != nil {
		title = *patch.Title
		if !utf8.ValidString(title) || strings.ContainsFunc(title, unicode.IsControl) {
			return CodexSession{}, problem("invalid_metadata", "Session titles cannot contain control characters or invalid text.")
		}
		title = strings.TrimSpace(title)
		if utf8.RuneCountInString(title) > 120 {
			return CodexSession{}, problem("invalid_metadata", "Session titles must contain at most 120 characters.")
		}
	}
	var session CodexSession
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE settings SET value=value WHERE key='databaseId'"); err != nil {
			return err
		}
		var err error
		session, err = scanCodex(tx.QueryRowContext(ctx, codexSelect+" WHERE s.id=? AND r.id=s.latest_run_id", sessionID))
		if err != nil {
			return err
		}
		if session.MetadataRevision != patch.Revision {
			return problem("metadata_conflict", "This session's details changed. Refresh them before retrying your edit.")
		}
		archivedAt := session.ArchivedAt
		if patch.Archived != nil {
			if *patch.Archived && archivedAt == "" {
				archivedAt = codexNow()
			} else if !*patch.Archived {
				archivedAt = ""
			}
		}
		if patch.Title == nil {
			title = session.Title
		}
		if title == session.Title && archivedAt == session.ArchivedAt {
			return nil
		}
		_, err = tx.ExecContext(ctx, "UPDATE codex_sessions SET title=?,archived_at=?,metadata_revision=metadata_revision+1 WHERE id=?", title, archivedAt, sessionID)
		if err != nil {
			return err
		}
		session.Title, session.ArchivedAt = title, archivedAt
		session.MetadataRevision++
		return nil
	})
	return session, err
}
