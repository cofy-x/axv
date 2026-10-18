-- AXV3 identifies this development baseline independently of migration versions.
PRAGMA application_id = 0x41585633;

-- Virtual documents.
CREATE TABLE entries (
    id TEXT PRIMARY KEY,
    parent_id TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('folder', 'file')),
    mime TEXT NOT NULL DEFAULT '',
    content BLOB NOT NULL DEFAULT X'',
    size INTEGER NOT NULL DEFAULT 0,
    revision INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    trashed_at TEXT NOT NULL DEFAULT '',
    trash_batch TEXT NOT NULL DEFAULT '',
    editable INTEGER NOT NULL DEFAULT 0,
    previewable INTEGER NOT NULL DEFAULT 0,
    system INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX entry_names ON entries (parent_id, name COLLATE NOCASE)
    WHERE trashed_at = '';
CREATE INDEX entry_parent ON entries (parent_id);

-- Desktop state and the persistent identity used for browser recovery.
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE layouts (client_id TEXT PRIMARY KEY, value TEXT NOT NULL);

INSERT INTO settings (key, value)
VALUES
    ('preferences', '{"wallpaper":"slate","iconSnap":true,"clock24Hour":true}'),
    ('databaseId', lower(hex(randomblob(16))));

-- Stable conversation metadata is independent of process runs and virtual files.
CREATE TABLE codex_sessions (
    id TEXT PRIMARY KEY,
    project_path TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    archived_at TEXT NOT NULL DEFAULT '',
    metadata_revision INTEGER NOT NULL DEFAULT 1 CHECK (metadata_revision > 0),
    created_at TEXT NOT NULL,
    conversation_id TEXT NOT NULL DEFAULT '',
    history_home TEXT NOT NULL CHECK (history_home <> ''),
    latest_run_id TEXT NOT NULL REFERENCES codex_runs (id) DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX codex_conversation_identity
    ON codex_sessions (history_home, conversation_id) WHERE conversation_id <> '';

-- The predecessor distinguishes continuation from an independent new session.
CREATE TABLE codex_runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES codex_sessions (id),
    request_id TEXT NOT NULL UNIQUE,
    previous_run_id TEXT REFERENCES codex_runs (id),
    request_cols INTEGER NOT NULL CHECK (request_cols BETWEEN 2 AND 500),
    request_rows INTEGER NOT NULL CHECK (request_rows BETWEEN 1 AND 200),
    status TEXT NOT NULL CHECK (status IN ('starting', 'running', 'stopping', 'ended', 'lost', 'failed')),
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    tmux_session TEXT NOT NULL CHECK (tmux_session <> ''),
    tmux_socket TEXT NOT NULL
);

CREATE UNIQUE INDEX codex_one_active_run ON codex_runs (session_id)
    WHERE status IN ('starting', 'running', 'stopping');

-- Only confirmed starts add recent projects; reads and reconnects do not.
CREATE TABLE codex_projects (
    path TEXT PRIMARY KEY,
    last_used_at TEXT NOT NULL
);

-- Explicit project registration is independent of Codex's recent directories.
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind = 'host'),
    name TEXT NOT NULL,
    root_path TEXT NOT NULL,
    root_identity TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (root_path, root_identity)
);

CREATE TABLE document_links (
    id TEXT PRIMARY KEY,
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('project', 'machine', 'conversation')),
    project_id TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    machine_id TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE (entry_id, kind, project_id, host_id, machine_id, conversation_id)
);

-- Snapshots deliberately have no source-entry foreign key: deleting a source
-- must not delete the immutable content already handed to a project or agent.
CREATE TABLE document_snapshots (
    id TEXT PRIMARY KEY,
    database_id TEXT NOT NULL,
    entry_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    name TEXT NOT NULL,
    content TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (database_id, entry_id, revision)
);

CREATE TABLE document_origins (
    entry_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id),
    path TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- Import commits its entry and receipt together. Export records its owned
-- staging name before publishing a no-replace hard link, allowing safe replay.
CREATE TABLE document_transfers (
    request_id TEXT PRIMARY KEY,
    payload_hash TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('import', 'export')),
    project_id TEXT NOT NULL REFERENCES projects(id),
    path TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    stage_path TEXT NOT NULL DEFAULT '',
    parent_identity TEXT NOT NULL DEFAULT '' CHECK (kind = 'import' OR parent_identity <> ''),
    result TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
