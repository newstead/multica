-- Single statement: CREATE INDEX CONCURRENTLY cannot run inside a transaction
-- or share a multi-command migration file.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_memory_provider_delivery_due
    ON memory_provider_delivery (status, next_attempt_at)
    WHERE status IN ('queued', 'retry');
