package storage

import "context"

// CodexRuns lists every recorded exact run, including superseded and archived
// conversations, for durable activity consumers. It performs no reconciliation.
func (s *Store) CodexRuns(ctx context.Context) ([]CodexSession, error) {
	return s.codexQuery(ctx, codexSelect+" ORDER BY r.created_at,r.id")
}
