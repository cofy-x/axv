package extensions

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"

	"github.com/cofy-x/axv/apps/server/internal/sqlitemigrate"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const databaseApplicationID = 0x41584532 // AXE2; independent of desktop.sqlite.

type Store struct {
	db *sql.DB
	id string
}

func Open(filename string) (*Store, error) {
	created := false
	file, createErr := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if createErr == nil {
		created = true
		if err := file.Close(); err != nil {
			return nil, err
		}
	} else if !errors.Is(createErr, os.ErrExist) {
		return nil, createErr
	}
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	err = func() error {
		if _, err := db.Exec("PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;"); err != nil {
			return err
		}
		if !created {
			var marker, version int
			if err := db.QueryRow("PRAGMA application_id").Scan(&marker); err != nil {
				return err
			}
			if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				return err
			}
			if marker != databaseApplicationID || version < 1 {
				return fmt.Errorf("incompatible or uninitialized extension database; no automatic reset is provided")
			}
		}
		if err := sqlitemigrate.Apply(context.Background(), db, migrationFiles, databaseApplicationID, func(ctx context.Context, tx *sql.Tx) error {
			id, err := randomUUID()
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO extension_identity(singleton,store_id) VALUES(1,?)", id)
			return err
		}); err != nil {
			return err
		}
		if err := s.validate(context.Background()); err != nil {
			return err
		}
		_, err := db.Exec("PRAGMA journal_mode=WAL;")
		return err
	}()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open extension database: %w", err)
	}
	return s, nil
}

func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) ID() string   { return s.id }

// validateSchema compares SQLite's complete schema catalog against a pristine
// database produced by the same embedded migration runner. This includes column
// types, keys, defaults, CHECKs, references, indexes and triggers, without a
// separate hand-maintained schema list or permissive SQL normalization.
func (s *Store) validateSchema(ctx context.Context) error {
	baseline, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer baseline.Close()
	baseline.SetMaxOpenConns(1)
	if err = sqlitemigrate.Apply(ctx, baseline, migrationFiles, databaseApplicationID, nil); err != nil {
		return err
	}
	expected, err := schemaFingerprint(ctx, baseline)
	if err != nil {
		return err
	}
	actual, err := schemaFingerprint(ctx, s.db)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("unknown extension database layout")
	}
	return nil
}

func schemaFingerprint(ctx context.Context, db *sql.DB) ([32]byte, error) {
	rows, err := db.QueryContext(ctx, "SELECT type,name,tbl_name,sql FROM sqlite_schema ORDER BY type,name")
	if err != nil {
		return [32]byte{}, err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for rows.Next() {
		var kind, name, table string
		var ddl sql.NullString
		if err = rows.Scan(&kind, &name, &table, &ddl); err != nil {
			return [32]byte{}, err
		}
		if err = encoder.Encode([]any{kind, name, table, ddl}); err != nil {
			return [32]byte{}, err
		}
	}
	if err = rows.Err(); err != nil {
		return [32]byte{}, err
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func (s *Store) validate(ctx context.Context) error {
	var check string
	if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("invalid extension database integrity: %s: %w", check, err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT store_id FROM extension_identity WHERE singleton=1").Scan(&s.id); err != nil {
		return err
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(s.id) {
		return fmt.Errorf("invalid extension store identity")
	}
	if err := s.validateSchema(ctx); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = rows.Err()
	_ = rows.Close()
	if invalid || err != nil {
		return fmt.Errorf("invalid extension database references")
	}
	// Reject changed code and manifests rather than treating a damaged store as
	// an empty catalog or repairing it during startup.
	rows, err = s.db.QueryContext(ctx, "SELECT digest,package_json FROM extension_code")
	if err != nil {
		return err
	}
	for rows.Next() {
		var digest string
		var code []byte
		if err = rows.Scan(&digest, &code); err != nil {
			break
		}
		var p Package
		if err = decodeJSON(code, &p); err != nil {
			break
		}
		canonical, got, validateErr := validatePackage(p)
		if validateErr != nil || got != digest || !bytes.Equal(canonical, code) {
			err = fmt.Errorf("invalid extension code digest or manifest")
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	var badData bool
	if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM extension_apps a LEFT JOIN extension_data d ON a.id=d.app_id WHERE d.app_id IS NULL)
OR EXISTS(SELECT 1 FROM extension_data WHERE app_id IS NULL OR NOT json_valid(value) OR length(CAST(value AS BLOB)) > ? OR typeof(revision) != 'integer' OR revision < 0)
OR EXISTS(SELECT 1 FROM extension_apps WHERE id IS NULL OR typeof(generation) != 'integer' OR generation < 1)`, MaxDataBytes).Scan(&badData); err != nil {
		return err
	}
	if badData {
		return fmt.Errorf("invalid extension data layout")
	}
	_, err = s.List(ctx)
	return err
}

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Metadata and grant checks must not materialize app HTML in Go. SQLite still
// reads the package JSON, but projects only the manifest across the SQL boundary.
// Complete immutable packages are validated on startup and content reads retain
// their own package decoding; this query projects only the small manifest.
const recordQuery = `SELECT a.id,json_extract(c.package_json,'$.manifest'),a.digest,a.generation,a.enabled,a.installed FROM extension_apps a JOIN extension_code c ON c.digest=a.digest`

type scanner interface{ Scan(...any) error }

func scanRecord(row scanner) (Record, error) {
	var r Record
	var raw []byte
	if err := row.Scan(&r.ID, &raw, &r.Digest, &r.Generation, &r.Enabled, &r.Installed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, failure("extension_not_found", "The extension was not found.")
		}
		return r, err
	}
	var manifest Manifest
	if err := decodeJSON(raw, &manifest); err != nil {
		return r, err
	}
	if err := validateManifest(manifest); err != nil {
		return r, err
	}
	if r.ID != manifest.ID || r.Generation < 1 || (!r.Installed && r.Enabled) {
		return r, fmt.Errorf("invalid extension installation record")
	}
	r.Title, r.Version, r.APIVersion, r.Capabilities = manifest.Title, manifest.Version, manifest.APIVersion, manifest.Capabilities
	r.Icon = manifest.Icon
	return r, nil
}

func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, recordQuery+" ORDER BY a.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Record{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

func readApp(ctx context.Context, tx *sql.Tx, id string) (Record, error) {
	return scanRecord(tx.QueryRowContext(ctx, recordQuery+" WHERE a.id=?", id))
}

func isCode(err error, code string) bool { var e *Error; return errors.As(err, &e) && e.Code == code }
func conflict() error {
	return failure("extension_conflict", "The extension changed. Refresh its information and review the action again.")
}

func (s *Store) Install(ctx context.Context, p Package, expected *int64) (app Record, created bool, err error) {
	code, digest, err := validatePackage(p)
	if err != nil {
		return app, false, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		current, err := readApp(ctx, tx, p.Manifest.ID)
		if err != nil && !isCode(err, "extension_not_found") {
			return err
		}
		created = isCode(err, "extension_not_found")
		if created && expected != nil || !created && (expected == nil || *expected != current.Generation) {
			return conflict()
		}
		generation := int64(1)
		enabled := true
		if !created {
			if current.Generation == math.MaxInt64 {
				return conflict()
			}
			generation = current.Generation + 1
			if current.Installed {
				enabled = current.Enabled
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO extension_code(digest,package_json) VALUES(?,?) ON CONFLICT(digest) DO NOTHING", digest, string(code)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO extension_apps(id,digest,generation,enabled,installed) VALUES(?,?,?,?,1)
ON CONFLICT(id) DO UPDATE SET digest=excluded.digest,generation=excluded.generation,enabled=excluded.enabled,installed=1`, p.Manifest.ID, digest, generation, enabled); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO extension_data(app_id) VALUES(?) ON CONFLICT(app_id) DO NOTHING", p.Manifest.ID); err != nil {
			return err
		}
		app, err = readApp(ctx, tx, p.Manifest.ID)
		return err
	})
	return app, created, err
}

func (s *Store) Change(ctx context.Context, id string, expected int64, enabled, installed bool) (app Record, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		current, err := readApp(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Generation != expected || current.Generation == math.MaxInt64 || !current.Installed {
			return conflict()
		}
		if !installed {
			enabled = false
		}
		if _, err = tx.ExecContext(ctx, "UPDATE extension_apps SET enabled=?,installed=?,generation=generation+1 WHERE id=?", enabled, installed, id); err != nil {
			return err
		}
		app, err = readApp(ctx, tx, id)
		return err
	})
	return app, err
}

func readDocument(ctx context.Context, tx *sql.Tx, id string) (Document, error) {
	var d Document
	var value string
	err := tx.QueryRowContext(ctx, "SELECT value,revision FROM extension_data WHERE app_id=?", id).Scan(&value, &d.Revision)
	d.Value = json.RawMessage(value)
	if errors.Is(err, sql.ErrNoRows) {
		return d, failure("extension_not_found", "The extension data was not found.")
	}
	if err == nil && (!json.Valid(d.Value) || d.Revision < 0) {
		err = fmt.Errorf("invalid extension data")
	}
	return d, err
}

func (s *Store) Data(ctx context.Context, id string) (doc Document, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := readApp(ctx, tx, id); err != nil {
			return err
		}
		doc, err = readDocument(ctx, tx, id)
		return err
	})
	return doc, err
}

func (s *Store) Purge(ctx context.Context, id string, expected int64) (doc Document, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		app, err := readApp(ctx, tx, id)
		if err != nil {
			return err
		}
		if app.Installed {
			return failure("extension_conflict", "Uninstall the extension before clearing its data.")
		}
		doc, err = writeDocument(ctx, tx, id, json.RawMessage("null"), expected)
		return err
	})
	return doc, err
}

func writeDocument(ctx context.Context, tx *sql.Tx, id string, value json.RawMessage, expected int64) (Document, error) {
	if expected < 0 || expected == math.MaxInt64 {
		return Document{}, failure("revision_conflict", "The extension data changed. Review the saved data before trying again.")
	}
	if len(value) == 0 || len(value) > MaxDataBytes || !json.Valid(value) {
		return Document{}, failure("invalid_extension_data", "Provide JSON data of at most 128 KiB.")
	}
	result, err := tx.ExecContext(ctx, "UPDATE extension_data SET value=?,revision=revision+1 WHERE app_id=? AND revision=?", string(value), id, expected)
	if err != nil {
		return Document{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Document{}, err
	}
	if n != 1 {
		return Document{}, failure("revision_conflict", "The extension data changed. Review the saved data before trying again.")
	}
	return readDocument(ctx, tx, id)
}

func (s *Store) scope(ctx context.Context, id, digest string, generation int64) (app Record, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error { app, err = checkScope(ctx, tx, id, digest, generation); return err })
	return app, err
}

func checkScope(ctx context.Context, tx *sql.Tx, id, digest string, generation int64) (Record, error) {
	app, err := readApp(ctx, tx, id)
	if err != nil && !isCode(err, "extension_not_found") {
		return app, err
	}
	if err != nil || !app.Installed || !app.Enabled || app.Digest != digest || app.Generation != generation {
		return app, expired()
	}
	return app, nil
}

func (s *Store) sessionData(ctx context.Context, session session, value json.RawMessage, expected *int64) (doc Document, err error) {
	if session.storeID != s.ID() {
		return doc, storeChanged()
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		app, err := checkScope(ctx, tx, session.app, session.digest, session.generation)
		if err != nil {
			return err
		}
		if len(app.Capabilities) != 1 || app.Capabilities[0] != "storage" {
			return failure("extension_forbidden", "This extension has no storage grant.")
		}
		if expected == nil {
			doc, err = readDocument(ctx, tx, app.ID)
		} else {
			doc, err = writeDocument(ctx, tx, app.ID, value, *expected)
		}
		return err
	})
	return doc, err
}

func (s *Store) Content(ctx context.Context, id, digest string) (html string, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		app, err := readApp(ctx, tx, id)
		if err != nil {
			return err
		}
		if !app.Installed || !app.Enabled || app.Digest != digest {
			return failure("extension_not_found", "This extension content is unavailable.")
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, "SELECT package_json FROM extension_code WHERE digest=?", digest).Scan(&raw); err != nil {
			return err
		}
		var p Package
		if err = decodeJSON(raw, &p); err != nil {
			return err
		}
		html = p.HTML
		return nil
	})
	return html, err
}
