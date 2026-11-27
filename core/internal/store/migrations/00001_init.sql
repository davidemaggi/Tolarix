-- Initial schema. Timestamps are RFC 3339 UTC strings; JSON columns hold
-- documents whose Go types live next to the code that owns them.

-- +goose Up

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Exactly one row (id = 1): the single admin account used in web mode.
CREATE TABLE admin_user (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    username             TEXT    NOT NULL DEFAULT 'admin',
    password_hash        TEXT    NOT NULL,
    must_change_password INTEGER NOT NULL DEFAULT 1,
    updated_at           TEXT    NOT NULL
);

-- Server-side sessions. Only a hash of the cookie value is stored.
CREATE TABLE sessions (
    id_hash      TEXT PRIMARY KEY,
    csrf_token   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL
);
CREATE INDEX sessions_expires_at ON sessions (expires_at);

-- A configured wiki endpoint. config is non-secret JSON; secret_enc is the
-- AES-GCM sealed secret JSON (bound to the row id), NULL when none.
CREATE TABLE connections (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    kind       TEXT NOT NULL,
    config     TEXT NOT NULL DEFAULT '{}',
    secret_enc BLOB,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- A flow: one Master source + scope -> one or more targets.
CREATE TABLE flows (
    id                   TEXT PRIMARY KEY,
    name                 TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    enabled              INTEGER NOT NULL DEFAULT 1,
    source_connection_id TEXT    NOT NULL REFERENCES connections (id) ON DELETE RESTRICT,
    scope                TEXT    NOT NULL DEFAULT '{"mode":"all"}',
    options              TEXT    NOT NULL DEFAULT '{}',
    schedule             TEXT    NOT NULL DEFAULT '{"kind":"manual"}',
    schedule_enabled     INTEGER NOT NULL DEFAULT 1,
    timezone             TEXT    NOT NULL DEFAULT 'UTC',
    created_at           TEXT    NOT NULL,
    updated_at           TEXT    NOT NULL
);

-- A Mirror of a flow: a target connection plus where to write inside it.
CREATE TABLE flow_targets (
    id            TEXT PRIMARY KEY,
    flow_id       TEXT    NOT NULL REFERENCES flows (id) ON DELETE CASCADE,
    connection_id TEXT    NOT NULL REFERENCES connections (id) ON DELETE RESTRICT,
    location      TEXT    NOT NULL DEFAULT '{}',
    position      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL
);
CREATE INDEX flow_targets_flow ON flow_targets (flow_id, position);

-- Master page <-> mirror page, per target. Hashes drive incremental sync and
-- mirror drift detection; the mirror id makes renames/moves real moves.
CREATE TABLE page_mappings (
    target_id      TEXT NOT NULL REFERENCES flow_targets (id) ON DELETE CASCADE,
    source_page_id TEXT NOT NULL,
    mirror_page_id TEXT NOT NULL,
    mirror_path    TEXT NOT NULL,
    source_hash    TEXT NOT NULL,
    mirror_hash    TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT 'active', -- active | archived
    updated_at     TEXT NOT NULL,
    PRIMARY KEY (target_id, source_page_id)
);
CREATE INDEX page_mappings_mirror ON page_mappings (target_id, mirror_page_id);

-- Attachments already uploaded to a target, keyed by content hash.
CREATE TABLE attachment_mappings (
    target_id    TEXT NOT NULL REFERENCES flow_targets (id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    mirror_ref   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (target_id, content_hash)
);

CREATE TABLE runs (
    id          TEXT PRIMARY KEY,
    flow_id     TEXT    NOT NULL REFERENCES flows (id) ON DELETE CASCADE,
    trigger     TEXT    NOT NULL,                    -- manual | schedule | cli | run-due
    dry_run     INTEGER NOT NULL DEFAULT 0,
    status      TEXT    NOT NULL,                    -- running | succeeded | partial | failed | cancelled
    started_at  TEXT    NOT NULL,
    finished_at TEXT,
    counts      TEXT    NOT NULL DEFAULT '{}',
    report      TEXT    NOT NULL DEFAULT '{}',       -- plan, warnings, errors
    error       TEXT
);
CREATE INDEX runs_flow_started ON runs (flow_id, started_at DESC);
CREATE INDEX runs_started ON runs (started_at DESC);

-- Live log lines of a run; the SSE endpoint replays and tails this table.
CREATE TABLE run_events (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    ts     TEXT NOT NULL,
    level  TEXT NOT NULL,
    msg    TEXT NOT NULL,
    attrs  TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX run_events_run ON run_events (run_id, id);

-- One run per flow at a time, shared by server, scheduler and CLI.
-- A lock whose heartbeat is stale may be taken over.
CREATE TABLE flow_locks (
    flow_id      TEXT PRIMARY KEY REFERENCES flows (id) ON DELETE CASCADE,
    run_id       TEXT NOT NULL,
    holder       TEXT NOT NULL,                      -- host/pid, for diagnostics
    acquired_at  TEXT NOT NULL,
    heartbeat_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE flow_locks;
DROP TABLE run_events;
DROP TABLE runs;
DROP TABLE attachment_mappings;
DROP TABLE page_mappings;
DROP TABLE flow_targets;
DROP TABLE flows;
DROP TABLE connections;
DROP TABLE sessions;
DROP TABLE admin_user;
DROP TABLE settings;
