CREATE TABLE IF NOT EXISTS request_logs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id     TEXT NOT NULL,
    at             INTEGER NOT NULL,
    session        TEXT NOT NULL DEFAULT '',
    server_key_id  TEXT NOT NULL DEFAULT '',
    model          TEXT NOT NULL DEFAULT '',
    tier           TEXT NOT NULL DEFAULT '',
    key_id         TEXT NOT NULL DEFAULT '',
    channel        TEXT NOT NULL DEFAULT '',
    anonymous      INTEGER NOT NULL DEFAULT 0,
    proxy_node     TEXT NOT NULL DEFAULT '',
    exit_node      TEXT NOT NULL DEFAULT '',
    exit_id        TEXT NOT NULL DEFAULT '',
    attempts       INTEGER NOT NULL DEFAULT 0,
    status         INTEGER NOT NULL DEFAULT 0,
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    success        INTEGER NOT NULL DEFAULT 0,
    outcome        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_request_logs_at ON request_logs (at DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_model ON request_logs (model);
CREATE INDEX IF NOT EXISTS idx_request_logs_channel ON request_logs (channel);
CREATE INDEX IF NOT EXISTS idx_request_logs_exit ON request_logs (exit_id);
CREATE INDEX IF NOT EXISTS idx_request_logs_server_key ON request_logs (server_key_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_request_logs_request ON request_logs (request_id);

CREATE TABLE IF NOT EXISTS request_attempts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id   TEXT NOT NULL,
    at           INTEGER NOT NULL,
    attempt      INTEGER NOT NULL,
    model        TEXT NOT NULL DEFAULT '',
    tier         TEXT NOT NULL DEFAULT '',
    key_id       TEXT NOT NULL DEFAULT '',
    channel      TEXT NOT NULL DEFAULT '',
    anonymous    INTEGER NOT NULL DEFAULT 0,
    proxy_node   TEXT NOT NULL DEFAULT '',
    exit_node    TEXT NOT NULL DEFAULT '',
    exit_id      TEXT NOT NULL DEFAULT '',
    status       INTEGER NOT NULL DEFAULT 0,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    success      INTEGER NOT NULL DEFAULT 0,
    outcome      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_request_attempts_request ON request_attempts (request_id, attempt);
CREATE INDEX IF NOT EXISTS idx_request_attempts_at ON request_attempts (at DESC);
CREATE INDEX IF NOT EXISTS idx_request_attempts_exit ON request_attempts (exit_id);
