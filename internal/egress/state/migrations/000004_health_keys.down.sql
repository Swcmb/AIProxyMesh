DROP TABLE IF EXISTS egress_environment;
DROP TABLE IF EXISTS egress_region_blocks;
DROP TABLE IF EXISTS egress_accuracy_samples;
DROP TABLE IF EXISTS egress_meta;
DROP TABLE IF EXISTS egress_canary_budget;
DROP INDEX IF EXISTS idx_health_updated;
DROP INDEX IF EXISTS idx_health_state;
DROP TABLE IF EXISTS egress_health_keys;
ALTER TABLE egress_nodes DROP COLUMN last_reachable;
