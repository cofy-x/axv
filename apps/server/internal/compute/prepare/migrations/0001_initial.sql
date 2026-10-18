-- AXP1 identifies preparation journals independently of desktop and compute.
PRAGMA application_id = 0x41585031;

CREATE TABLE plans (
    id TEXT PRIMARY KEY,
    data TEXT NOT NULL CHECK (json_valid(data)),
    expires_at TEXT NOT NULL
);

CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    singleton INTEGER NOT NULL DEFAULT 1 CHECK (singleton = 1),
    data TEXT NOT NULL CHECK (json_valid(data)),
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX one_active_preparation ON jobs (singleton)
    WHERE status IN ('queued', 'running');

CREATE TABLE requests (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    job_id TEXT NOT NULL REFERENCES jobs (id)
);
