ALTER TABLE review_settings ADD COLUMN repository_vm_targets TEXT NOT NULL DEFAULT '{}';

CREATE TABLE review_notification_store (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    store_id TEXT NOT NULL
);

CREATE TABLE review_notification_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    account TEXT NOT NULL COLLATE NOCASE,
    repository TEXT NOT NULL COLLATE NOCASE,
    number INTEGER NOT NULL CHECK (number > 0),
    base_sha TEXT NOT NULL,
    head_sha TEXT NOT NULL,
    observed_at INTEGER NOT NULL
);
