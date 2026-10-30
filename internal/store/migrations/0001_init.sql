-- Phase 1 schema. Timestamps are UTC RFC 3339 text (store.FormatTime).

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE app_sessions (
    id          INTEGER PRIMARY KEY,
    started_at  TEXT NOT NULL,
    ended_at    TEXT,
    app_version TEXT NOT NULL
) STRICT;

-- Connection profiles. Passwords are never stored here; they live in the
-- OS keychain under the profile id.
CREATE TABLE connections (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    dialect    TEXT NOT NULL CHECK (dialect IN ('postgres', 'mysql', 'sqlite')),
    host       TEXT NOT NULL DEFAULT '',
    port       INTEGER NOT NULL DEFAULT 0,
    username   TEXT NOT NULL DEFAULT '',
    database   TEXT NOT NULL DEFAULT '',
    tls        TEXT NOT NULL DEFAULT 'prefer',
    env        TEXT NOT NULL DEFAULT 'dev' CHECK (env IN ('dev', 'staging', 'prod')),
    read_only  INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1)),
    max_conns  INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE UNIQUE INDEX connections_name ON connections (name);

CREATE TABLE query_history (
    id          INTEGER PRIMARY KEY,
    conn_id     TEXT NOT NULL,
    started_at  TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    kind        TEXT NOT NULL,
    status      TEXT NOT NULL,
    row_count   INTEGER NOT NULL,
    sql_text    TEXT NOT NULL,
    error       TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX query_history_conn ON query_history (conn_id, id DESC);

-- Append-only, hash-chained audit log. hash = SHA-256(prev_hash || row
-- fields); see internal/audit. Triggers forbid any change after insert.
CREATE TABLE audit_log (
    seq           INTEGER PRIMARY KEY,
    at            TEXT NOT NULL,
    conn_id       TEXT NOT NULL,
    conn_name     TEXT NOT NULL,
    dialect       TEXT NOT NULL,
    env           TEXT NOT NULL,
    kind          TEXT NOT NULL,
    status        TEXT NOT NULL,
    rows_affected INTEGER NOT NULL,
    duration_ms   INTEGER NOT NULL,
    sql_text      TEXT NOT NULL,
    error         TEXT NOT NULL,
    prev_hash     BLOB NOT NULL,
    hash          BLOB NOT NULL UNIQUE
) STRICT;

CREATE INDEX audit_log_conn ON audit_log (conn_id, seq);

CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;

CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
