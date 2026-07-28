-- Revert only the data repair performed by migration 225.

DO $$
DECLARE
    v_started_at TIMESTAMPTZ := clock_timestamp();
BEGIN
    WITH deleted AS (
        DELETE FROM migration_225_task_usage_cost_backfill
        RETURNING task_usage_id
    ),
    marked AS (
        SELECT DISTINCT task_usage_id FROM deleted
    )
    UPDATE task_usage tu
       SET cost_usd_ticks = NULL,
           updated_at = clock_timestamp()
      FROM marked m
     WHERE tu.id = m.task_usage_id;

    PERFORM rollup_task_usage_hourly_window(v_started_at - interval '1 second', clock_timestamp() + interval '1 second');
END $$;

DELETE FROM issue_pull_request ipr
USING migration_225_issue_pr_backfill m
WHERE ipr.issue_id = m.issue_id
  AND ipr.pull_request_id = m.pull_request_id;

DELETE FROM github_pull_request pr
USING migration_225_github_pr_backfill m
WHERE pr.id = m.pull_request_id;

DROP TABLE IF EXISTS migration_225_issue_pr_backfill;
DROP TABLE IF EXISTS migration_225_github_pr_backfill;
DROP TABLE IF EXISTS migration_225_task_usage_cost_backfill;

COMMENT ON COLUMN task_usage.cost_usd_ticks IS
    'Provider-reported cost in 1e-10 USD. NULL when the provider reports none; those rows are priced client-side from the static rate table.';
