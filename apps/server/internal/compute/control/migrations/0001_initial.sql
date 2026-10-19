-- AXC2 identifies the compute database independently of the migration sequence.
PRAGMA application_id = 0x41584332;

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
