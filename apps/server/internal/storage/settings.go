package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *Store) Bootstrap(ctx context.Context, clientID string) (Bootstrap, error) {
	b := Bootstrap{Layout: json.RawMessage("null")}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		if err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='databaseId'").Scan(&b.DatabaseID); err != nil {
			return err
		}
		b.Entries, err = list(ctx, tx)
		if err != nil {
			return err
		}
		var raw string
		if err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='preferences'").Scan(&raw); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(raw), &b.Preferences); err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, "SELECT value FROM layouts WHERE client_id=?", clientID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var layout map[string]json.RawMessage
		if len(raw) > 256<<10 || json.Unmarshal([]byte(raw), &layout) != nil || layout == nil {
			// Keep the damaged row for explicit recovery; it must not prevent files
			// from loading or be silently overwritten by the default desktop.
			b.LayoutError = "The saved layout cannot be read. Reset the layout to save a new desktop; your files are unchanged."
			return nil
		}
		b.Layout = json.RawMessage(raw)
		return nil
	})
	return b, err
}

func (s *Store) SavePreferences(ctx context.Context, p Preferences) error {
	if p.Wallpaper != "slate" && p.Wallpaper != "dither" && p.Wallpaper != "solid" {
		return problem("invalid_wallpaper", "Choose an available desktop pattern.")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE settings SET value=? WHERE key='preferences'", string(raw))
	return err
}

func (s *Store) SaveLayout(ctx context.Context, clientID string, layout json.RawMessage) error {
	if len(clientID) == 0 || len(clientID) > 128 {
		return problem("invalid_client", "A browser client identifier is required.")
	}
	var value map[string]json.RawMessage
	if len(layout) > 256<<10 || json.Unmarshal(layout, &value) != nil || value == nil {
		return problem("invalid_layout", "Layout must be a JSON object no larger than 256 KiB.")
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO layouts(client_id,value) VALUES(?,?) ON CONFLICT(client_id) DO UPDATE SET value=excluded.value", clientID, string(layout))
	return err
}
