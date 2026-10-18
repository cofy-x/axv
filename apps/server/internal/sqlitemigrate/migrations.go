// Package sqlitemigrate applies feature-owned SQLite migrations atomically.
package sqlitemigrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var migrationFilename = regexp.MustCompile("^([0-9]{4})_[a-z][a-z0-9_]*[.]sql$")

type migration struct {
	version int
	name    string
	sql     string
}

// Filenames are the migration catalog; there is no separate latest version or
// registration list. Published migrations must remain in place and unchanged.
func loadMigrations(files fs.FS) ([]migration, error) {
	names, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list database migrations: %w", err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no database migrations found")
	}

	migrations := make([]migration, 0, len(names))
	// Glob sorts names, and fixed-width numeric prefixes preserve version order.
	for _, name := range names {
		match := migrationFilename.FindStringSubmatch(path.Base(name))
		if match == nil {
			return nil, fmt.Errorf("invalid database migration filename %q: expected NNNN_description.sql", name)
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("read database migration version %q: %w", name, err)
		}
		if expected := len(migrations) + 1; version != expected {
			return nil, fmt.Errorf("database migration %q has version %d, expected %d: versions must be unique and consecutive", name, version, expected)
		}
		contents, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("read database migration %q: %w", name, err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return nil, fmt.Errorf("database migration %q is empty", name)
		}
		migrations = append(migrations, migration{version: version, name: name, sql: string(contents)})
	}
	return migrations, nil
}

// Apply owns the migration transaction. Feature initialization runs only for a
// genuinely empty database and commits with the schema and its version markers.
// applicationID identifies one database kind and its incompatible baseline.
func Apply(ctx context.Context, db *sql.DB, files fs.FS, applicationID int, initialize func(context.Context, *sql.Tx) error) error {
	if applicationID <= 0 {
		return fmt.Errorf("database format marker must be positive")
	}
	migrations, err := loadMigrations(files)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = apply(ctx, tx, migrations, applicationID, initialize); err != nil {
		return err
	}
	return tx.Commit()
}

func apply(ctx context.Context, tx *sql.Tx, migrations []migration, expectedApplicationID int, initialize func(context.Context, *sql.Tx) error) error {
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read database schema version: %w", err)
	}
	if version < 0 {
		return fmt.Errorf("invalid database schema version %d", version)
	}
	var applicationID int
	if err := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
		return fmt.Errorf("read database format: %w", err)
	}
	if applicationID != expectedApplicationID && (version > 0 || applicationID != 0) {
		return fmt.Errorf("incompatible development database format: stop its writers and reset this AXV-owned development database; no migration is provided")
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema %d is newer than this application (latest %d)", version, len(migrations))
	}
	if version == 0 {
		var hasSchema bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*')").Scan(&hasSchema); err != nil {
			return fmt.Errorf("inspect unversioned database: %w", err)
		}
		if hasSchema {
			return fmt.Errorf("cannot initialize a nonempty database without a schema version")
		}
	}

	// All pending migrations, their versions, and first-run initialization commit
	// together. A later failure must also roll back earlier migration files.
	for _, migration := range migrations[version:] {
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply database migration %q: %w", migration.name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", migration.version)); err != nil {
			return fmt.Errorf("record database migration %q: %w", migration.name, err)
		}
	}
	var actualApplicationID int
	if err := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&actualApplicationID); err != nil {
		return err
	}
	if actualApplicationID != expectedApplicationID {
		return fmt.Errorf("database migration format marker %d does not match expected %d", actualApplicationID, expectedApplicationID)
	}
	if version == 0 && initialize != nil {
		if err := initialize(ctx, tx); err != nil {
			return fmt.Errorf("initialize database: %w", err)
		}
	}
	return nil
}
