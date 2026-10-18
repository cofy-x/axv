package storage

import (
	"context"
	"database/sql"
	"errors"
)

func descendants(entries []Entry, root string) []Entry {
	result := []Entry{}
	var walk func(string)
	walk = func(parentID string) {
		for _, e := range entries {
			if e.ParentID == parentID {
				result = append(result, e)
				walk(e.ID)
			}
		}
	}
	walk(root)
	return result
}

func (s *Store) Trash(ctx context.Context, itemID string) (Entry, error) {
	var result Entry
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		e, err := get(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if e.System {
			return problem("system_entry", "System folders cannot be moved to the Trash.")
		}
		if e.TrashedAt != "" {
			result = e
			return nil
		}
		entries, err := list(ctx, tx)
		if err != nil {
			return err
		}
		stamp, batch := now(), id()
		affected := append([]Entry{e}, descendants(entries, itemID)...)
		for _, item := range affected {
			if item.TrashedAt != "" {
				continue
			}
			if _, err = tx.ExecContext(ctx, "UPDATE entries SET trashed_at=?,trash_batch=?,updated_at=?,revision=revision+1 WHERE id=?", stamp, batch, stamp, item.ID); err != nil {
				return err
			}
		}
		result, err = get(ctx, tx, itemID)
		return err
	})
	return result, err
}

func (s *Store) Restore(ctx context.Context, itemID string) (Entry, error) {
	var result Entry
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		e, err := get(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if e.TrashedAt == "" {
			result = e
			return nil
		}
		p, err := get(ctx, tx, e.ParentID)
		if err != nil {
			var storeErr *Error
			if !errors.As(err, &storeErr) || storeErr.Code != "not_found" {
				return err
			}
		}
		if err != nil || p.TrashedAt != "" {
			e.ParentID = "documents"
		}
		if e.Name, err = uniqueName(ctx, tx, e.ParentID, e.Name, e.ID, true); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE entries SET parent_id=?,name=? WHERE id=?", e.ParentID, e.Name, e.ID); err != nil {
			return err
		}
		entries, err := list(ctx, tx)
		if err != nil {
			return err
		}
		affected := append([]Entry{e}, descendants(entries, e.ID)...)
		for _, item := range affected {
			if item.TrashBatch != e.TrashBatch {
				continue
			}
			if _, err = tx.ExecContext(ctx, "UPDATE entries SET trashed_at='',trash_batch='',updated_at=?,revision=revision+1 WHERE id=?", now(), item.ID); err != nil {
				return err
			}
		}
		result, err = get(ctx, tx, itemID)
		return err
	})
	return result, err
}

func (s *Store) Delete(ctx context.Context, itemID string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		e, err := get(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if e.TrashedAt == "" || e.System {
			return problem("not_trashed", "Only items in the Trash can be permanently deleted.")
		}
		entries, err := list(ctx, tx)
		if err != nil {
			return err
		}
		affected := append([]Entry{e}, descendants(entries, itemID)...)
		removed := map[string]bool{}
		for _, item := range affected {
			if item.TrashBatch == e.TrashBatch {
				removed[item.ID] = true
			}
		}
		for _, item := range affected {
			if !removed[item.ID] && removed[item.ParentID] {
				if _, err = tx.ExecContext(ctx, "UPDATE entries SET parent_id='documents' WHERE id=?", item.ID); err != nil {
					return err
				}
			}
		}
		for itemID := range removed {
			if _, err = tx.ExecContext(ctx, "DELETE FROM entries WHERE id=?", itemID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) EmptyTrash(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM entries WHERE trashed_at<>'' AND system=0")
		return err
	})
}
