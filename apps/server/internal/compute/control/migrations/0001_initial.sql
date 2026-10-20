-- AXC3 identifies the compute database independently of the migration sequence.
PRAGMA application_id = 0x41584333;

-- One persistent host identity; reopening never allocates a replacement.
CREATE TABLE identity (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    id TEXT NOT NULL UNIQUE CHECK (id <> '')
);

-- JSON owns the durable models; indexed columns support admission and recovery.
CREATE TABLE machines (
    id TEXT PRIMARY KEY,
    data TEXT NOT NULL CHECK (json_valid(data)),
    status TEXT NOT NULL,
    desired TEXT NOT NULL,
    reserved INTEGER NOT NULL,
    cpus INTEGER NOT NULL,
    memory_mib INTEGER NOT NULL,
    disk_gib INTEGER NOT NULL,
    slot INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX machine_slot ON machines (slot) WHERE status <> 'deleted';
CREATE UNIQUE INDEX machine_name
    ON machines (json_extract(data, '$.spec.name') COLLATE BINARY)
    WHERE status <> 'deleted';

CREATE TABLE volumes (
    id TEXT PRIMARY KEY,
    data TEXT NOT NULL CHECK (json_valid(data)),
    size_gib INTEGER NOT NULL,
    machine_id TEXT NOT NULL,
    status TEXT NOT NULL
);

CREATE TABLE operations (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE,
    resource_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    status TEXT NOT NULL,
    data TEXT NOT NULL CHECK (json_valid(data)),
    created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX active_operation ON operations (resource_id)
    WHERE status IN ('queued', 'running');
CREATE INDEX queued_operations ON operations (status, created_at);

CREATE TABLE catalog (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    data TEXT NOT NULL CHECK (json_valid(data)),
    disk_gib INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX catalog_kind ON catalog (kind);

-- Project identity belongs to a retained volume, never its current VM.
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    volume_id TEXT NOT NULL,
    root TEXT NOT NULL,
    data TEXT NOT NULL CHECK (json_valid(data)),
    UNIQUE (volume_id, root),
    FOREIGN KEY (volume_id) REFERENCES volumes(id)
);
CREATE TABLE project_requests (
    request_id TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id)
);

-- Project commands have their own durable ledger. Lifecycle operation recovery
-- must never replay an arbitrary command after an uncertain execution outcome.
CREATE TABLE project_tasks (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    data TEXT NOT NULL CHECK (json_valid(data))
);
CREATE INDEX project_task_project ON project_tasks(project_id);
CREATE TABLE task_attempts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES project_tasks(id),
    status TEXT NOT NULL,
    log_bytes INTEGER NOT NULL DEFAULT 0,
    cached_bytes INTEGER NOT NULL DEFAULT 0,
    reserved_bytes INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    data TEXT NOT NULL CHECK (json_valid(data)),
    log BLOB NOT NULL DEFAULT X''
);
CREATE INDEX task_attempt_task ON task_attempts(task_id);
CREATE INDEX task_attempt_status ON task_attempts(status, updated_at);
CREATE TABLE task_requests (
    request_id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    fingerprint TEXT NOT NULL,
    task_id TEXT NOT NULL REFERENCES project_tasks(id),
    attempt_id TEXT NOT NULL REFERENCES task_attempts(id)
);
