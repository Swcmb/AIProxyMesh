ALTER TABLE egress_health_keys ADD COLUMN failure_pending_until INTEGER NOT NULL DEFAULT 0;
