-- AXV5 identifies this development baseline independently of migration versions.
PRAGMA application_id = 0x41585635;

-- Desktop state and the persistent identity used for browser recovery.
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE layouts (client_id TEXT PRIMARY KEY, value TEXT NOT NULL);

INSERT INTO settings (key, value)
VALUES
    ('preferences', '{"wallpaper":"slate","iconSnap":true,"clock24Hour":true}'),
    ('databaseId', lower(hex(randomblob(16))));

-- Stable conversation metadata is independent of process runs.
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
    tmux_socket TEXT NOT NULL,
    vm_project TEXT NOT NULL DEFAULT '',
    vm_socket TEXT NOT NULL DEFAULT '',
    vm_config_fingerprint TEXT NOT NULL DEFAULT '',
    CHECK ((vm_project = '' AND vm_socket = '' AND vm_config_fingerprint = '') OR
           (vm_project <> '' AND vm_socket <> '' AND vm_config_fingerprint <> ''))
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
