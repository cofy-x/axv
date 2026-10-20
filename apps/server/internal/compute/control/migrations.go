package control

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/sqlitemigrate"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const databaseApplicationID = 0x41584333 // AXC3

func (s *Store) migrate() error {
	ctx := context.Background()
	if err := sqlitemigrate.Apply(ctx, s.db, migrationFiles, databaseApplicationID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO identity(singleton,id) VALUES(1,?)", compute.NewID("host"))
		return err
	}); err != nil {
		return fmt.Errorf("compute database: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM identity WHERE singleton=1").Scan(&s.HostID); err != nil {
		return fmt.Errorf("compute database identity: %w", err)
	}
	return nil
}
