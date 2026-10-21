PRAGMA application_id = 1096303922; -- AXE2

CREATE TABLE extension_identity (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    store_id TEXT NOT NULL CHECK (length(store_id) = 36)
);

-- Code is immutable and retained independently of installation and user data.
CREATE TABLE extension_code (
    digest TEXT PRIMARY KEY NOT NULL CHECK (length(digest) = 64),
    package_json TEXT NOT NULL CHECK (json_valid(package_json))
);

CREATE TABLE extension_apps (
    id TEXT PRIMARY KEY NOT NULL,
    digest TEXT NOT NULL REFERENCES extension_code(digest),
    generation INTEGER NOT NULL CHECK (typeof(generation) = 'integer' AND generation > 0),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    installed INTEGER NOT NULL CHECK (installed IN (0, 1)),
    CHECK (installed = 1 OR enabled = 0)
);

CREATE TABLE extension_data (
    app_id TEXT PRIMARY KEY NOT NULL REFERENCES extension_apps(id),
    value TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(value)),
    revision INTEGER NOT NULL DEFAULT 0 CHECK (typeof(revision) = 'integer' AND revision >= 0)
);
