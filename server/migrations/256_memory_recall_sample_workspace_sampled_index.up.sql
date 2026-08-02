-- Single statement: CREATE INDEX CONCURRENTLY cannot run inside a transaction
-- or share a multi-command migration file.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_memory_recall_sample_workspace_sampled
    ON memory_recall_sample (workspace_id, sampled_at DESC);
