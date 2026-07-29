ALTER TABLE memory_recall_sample
    DROP COLUMN IF EXISTS read_mode,
    DROP COLUMN IF EXISTS recall_correlation_id;

ALTER TABLE memory_provider_delivery
    DROP COLUMN IF EXISTS delivery_lag_ms;
