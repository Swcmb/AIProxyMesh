CREATE TABLE IF NOT EXISTS egress_nodes (
    hash            TEXT PRIMARY KEY,
    raw             BLOB NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    failures        INTEGER NOT NULL DEFAULT 0,
    cooldown_until  INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS egress_node_subscriptions (
    node_hash       TEXT NOT NULL REFERENCES egress_nodes(hash) ON DELETE CASCADE,
    subscription_id TEXT NOT NULL,
    PRIMARY KEY (node_hash, subscription_id)
);

CREATE TABLE IF NOT EXISTS egress_latency (
    node_hash       TEXT NOT NULL REFERENCES egress_nodes(hash) ON DELETE CASCADE,
    host            TEXT NOT NULL,
    ewma_ns         INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (node_hash, host)
);

CREATE TABLE IF NOT EXISTS egress_upstream_stats (
    node_hash       TEXT NOT NULL REFERENCES egress_nodes(hash) ON DELETE CASCADE,
    target          TEXT NOT NULL,
    successes       INTEGER NOT NULL DEFAULT 0,
    failures        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (node_hash, target)
);

CREATE TABLE IF NOT EXISTS egress_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    at              INTEGER NOT NULL,
    request_id      TEXT NOT NULL DEFAULT '',
    node_hash       TEXT NOT NULL DEFAULT '',
    node_name       TEXT NOT NULL DEFAULT '',
    target          TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL,
    status          INTEGER NOT NULL DEFAULT 0,
    attempt         INTEGER NOT NULL DEFAULT 0,
    reason          TEXT NOT NULL DEFAULT '',
    detail          TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_egress_events_at ON egress_events(at);
