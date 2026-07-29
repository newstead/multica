ALTER TABLE memory_provider_delivery
    ADD COLUMN IF NOT EXISTS delivery_lag_ms BIGINT NOT NULL DEFAULT 0;

ALTER TABLE memory_recall_sample
    ADD COLUMN IF NOT EXISTS recall_correlation_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS read_mode TEXT NOT NULL DEFAULT '';
