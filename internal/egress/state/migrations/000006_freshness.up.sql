ALTER TABLE egress_nodes ADD COLUMN quota_stale_until INTEGER NOT NULL DEFAULT 0;
ALTER TABLE egress_health_keys ADD COLUMN rate_window BLOB;
