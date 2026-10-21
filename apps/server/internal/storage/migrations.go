package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/cofy-x/axv/apps/server/internal/sqlitemigrate"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// This file-format marker is independent of the migration sequence.
const databaseApplicationID = 0x41585634 // AXV4

func (s *Store) migrate(ctx context.Context) error { return s.migrateFrom(ctx, migrationFiles) }

func (s *Store) migrateFrom(ctx context.Context, files fs.FS) error {
	return sqlitemigrate.Apply(ctx, s.db, files, databaseApplicationID, func(ctx context.Context, tx *sql.Tx) error {
		if err := seedDocuments(ctx, tx); err != nil {
			return fmt.Errorf("seed database documents: %w", err)
		}
		return nil
	})
}
