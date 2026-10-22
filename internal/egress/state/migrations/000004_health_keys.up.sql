CREATE TABLE IF NOT EXISTS egress_health_keys (
    node_hash       TEXT NOT NULL,
    target          TEXT NOT NULL,
    model           TEXT NOT NULL,
    protocol        TEXT NOT NULL,
    mode            TEXT NOT NULL,
    valid_required  INTEGER NOT NULL DEFAULT 1,
    state           TEXT NOT NULL,
    http_success    INTEGER NOT NULL DEFAULT 0,
    http_failure    INTEGER NOT NULL DEFAULT 0,
    valid_success   INTEGER NOT NULL DEFAULT 0,
    valid_failure   INTEGER NOT NULL DEFAULT 0,
    primary_success INTEGER NOT NULL DEFAULT 0,
    primary_failure INTEGER NOT NULL DEFAULT 0,
    stream_success  INTEGER NOT NULL DEFAULT 0,
    stream_failure  INTEGER NOT NULL DEFAULT 0,
    consecutive_stream_fails INTEGER NOT NULL DEFAULT 0,
    unknown_streak  INTEGER NOT NULL DEFAULT 0,
    last_unknown_at INTEGER NOT NULL DEFAULT 0,
    rate_limited    INTEGER NOT NULL DEFAULT 0,
    admission_http_success INTEGER NOT NULL DEFAULT 0,
    admission_http_failure INTEGER NOT NULL DEFAULT 0,
    admission_valid_success INTEGER NOT NULL DEFAULT 0,
    admission_valid_failure INTEGER NOT NULL DEFAULT 0,
    admission_http_buckets INTEGER NOT NULL DEFAULT 0,
    admission_valid_buckets INTEGER NOT NULL DEFAULT 0,
    bucket_start    INTEGER NOT NULL DEFAULT 0,
    bucket_http_seen INTEGER NOT NULL DEFAULT 0,
    bucket_valid_seen INTEGER NOT NULL DEFAULT 0,
    bucket_http_reservoir BLOB,
    bucket_valid_reservoir BLOB,
    alpha200        REAL NOT NULL DEFAULT 1,
    beta200         REAL NOT NULL DEFAULT 1,
    alpha_valid     REAL NOT NULL DEFAULT 1,
    beta_valid      REAL NOT NULL DEFAULT 1,
    decayed_at      INTEGER NOT NULL DEFAULT 0,
    ttfb_ewma_ns    INTEGER NOT NULL DEFAULT 0,
    total_ewma_ns   INTEGER NOT NULL DEFAULT 0,
    latency_hist    BLOB,
    ttfb_samples    INTEGER NOT NULL DEFAULT 0,
    last_sample_at  INTEGER NOT NULL DEFAULT 0,
    consecutive_fails INTEGER NOT NULL DEFAULT 0,
    consecutive_success INTEGER NOT NULL DEFAULT 0,
    open_count      INTEGER NOT NULL DEFAULT 0,
    open_until      INTEGER NOT NULL DEFAULT 0,
    last_success_at INTEGER NOT NULL DEFAULT 0,
    last_failure_at INTEGER NOT NULL DEFAULT 0,
    last_probe_at   INTEGER NOT NULL DEFAULT 0,
    last_reachable_at INTEGER NOT NULL DEFAULT 0,
    window_buckets  BLOB,
    window_index    INTEGER NOT NULL DEFAULT 0,
    active_epoch    INTEGER NOT NULL DEFAULT 0,
    environment_epoch INTEGER NOT NULL DEFAULT 0,
    pending_reverify INTEGER NOT NULL DEFAULT 0,
    reverify_success INTEGER NOT NULL DEFAULT 0,
    last_check_samples INTEGER NOT NULL DEFAULT 0,
    last_check_valid_samples INTEGER NOT NULL DEFAULT 0,
    http_check_round INTEGER NOT NULL DEFAULT 0,
    valid_check_round INTEGER NOT NULL DEFAULT 0,
    next_probe_at   INTEGER NOT NULL DEFAULT 0,
    recovery_start  INTEGER NOT NULL DEFAULT 0,
    epoch_success   INTEGER NOT NULL DEFAULT 0,
    epoch_failure   INTEGER NOT NULL DEFAULT 0,
    prev_state      TEXT NOT NULL DEFAULT '',
    last_status     INTEGER NOT NULL DEFAULT 0,
    not_found_count INTEGER NOT NULL DEFAULT 0,
    last_not_found_at INTEGER NOT NULL DEFAULT 0,
    half_open_success INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    state_version   INTEGER NOT NULL DEFAULT 0,
    payload_json    TEXT NOT NULL DEFAULT '',
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (node_hash, target, model, protocol, mode),
    FOREIGN KEY (node_hash) REFERENCES egress_nodes(hash) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_health_state ON egress_health_keys(state);
CREATE INDEX IF NOT EXISTS idx_health_updated ON egress_health_keys(updated_at);

ALTER TABLE egress_nodes ADD COLUMN last_reachable INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS egress_canary_budget (
    node_hash TEXT NOT NULL,
    target TEXT NOT NULL,
    model TEXT NOT NULL,
    protocol TEXT NOT NULL,
    mode TEXT NOT NULL,
    day TEXT NOT NULL,
    count INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (node_hash, target, model, protocol, mode, day),
    FOREIGN KEY (node_hash) REFERENCES egress_nodes(hash) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS egress_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS egress_accuracy_samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_hash TEXT NOT NULL,
    target TEXT NOT NULL,
    model TEXT NOT NULL,
    protocol TEXT NOT NULL,
    mode TEXT NOT NULL,
    active_epoch INTEGER NOT NULL,
    request_id TEXT NOT NULL,
    success INTEGER NOT NULL,
    recorded_at INTEGER NOT NULL,
    UNIQUE(node_hash, target, model, protocol, mode, active_epoch)
);

CREATE TABLE IF NOT EXISTS egress_region_blocks (
    target  TEXT NOT NULL,
    country TEXT NOT NULL,
    until   INTEGER NOT NULL,
    PRIMARY KEY (target, country)
);

CREATE TABLE IF NOT EXISTS egress_environment (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    epoch           INTEGER NOT NULL DEFAULT 0,
    state           TEXT NOT NULL DEFAULT 'healthy',
    last_probe_at   INTEGER NOT NULL DEFAULT 0,
    probe_failures  INTEGER NOT NULL DEFAULT 0,
    bootstrap_failures INTEGER NOT NULL DEFAULT 0,
    environment_failures INTEGER NOT NULL DEFAULT 0,
    bootstrap_latency_ms INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL DEFAULT 0
);
