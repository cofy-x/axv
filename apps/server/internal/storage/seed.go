package storage

import (
	"context"
	"database/sql"
)

// Samples become user documents after creation. Only a new database is seeded;
// schema upgrades must never replace edits or recreate deleted samples.
func seedDocuments(ctx context.Context, tx *sql.Tx) error {
	for _, v := range []struct {
		id, parent, name, kind, content string
		system                          bool
	}{
		{"root", "", "Workspace", "folder", "", true},
		{"desktop", "root", "Desktop", "folder", "", true},
		{"documents", "root", "Documents", "folder", "", true},
		{"readme", "documents", "Read Me.txt", "file", "Welcome to AXV!\n\nAXV — Agents × Virtualization. Open Welcome or About AXV for the capabilities available in your installed version.\n\nOpen Files to organize these documents. Text Editor saves only when you choose Save, with separate browser recovery for unsaved drafts.\n\nDrag windows by their title bars. Double-click a title to fold it. Use the Dock and Clip to organize your desktop.\n\nThese samples are created once. You can edit or delete them; updates do not replace them.\n", false},
		{"desktop-note", "desktop", "Getting Started.txt", "file", "AXV — Agents × Virtualization\n\nOpen Welcome for application shortcuts and About AXV for current capabilities. Use Documents to organize your files and Preferences to customize the desktop.\n\nDesktop workspaces organize windows. Your documents, preferences, and window positions are saved by AXV.\n\nThis is an editable sample document.\n", false},
	} {
		e := makeEntry(v.parent, v.name, v.kind, []byte(v.content))
		e.ID = v.id
		e.System = v.system
		if err := insert(ctx, tx, e, []byte(v.content)); err != nil {
			return err
		}
	}
	return nil
}
