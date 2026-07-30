BEGIN;

WITH duplicate_runs AS (
    SELECT id
    FROM (
        SELECT
            id,
            row_number() OVER (
                PARTITION BY autopilot_id, trigger_id
                ORDER BY
                    CASE
                        WHEN status = 'running' AND task_id IS NOT NULL THEN 0
                        WHEN status = 'issue_created' AND issue_id IS NOT NULL THEN 1
                        ELSE 2
                    END,
                    planned_at NULLS LAST,
                    triggered_at,
                    created_at,
                    id
            ) AS rn
        FROM autopilot_run
        WHERE source = 'schedule'
          AND trigger_id IS NOT NULL
          AND status IN ('issue_created', 'running')
    ) ranked
    WHERE rn > 1
)
UPDATE agent_task_queue task
SET status = 'cancelled',
    completed_at = COALESCE(task.completed_at, now()),
    failure_reason = COALESCE(task.failure_reason, 'skipped_overlap'),
    error = COALESCE(task.error, 'skipped_overlap')
FROM duplicate_runs
WHERE task.autopilot_run_id = duplicate_runs.id
  AND task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred');

WITH duplicate_runs AS (
    SELECT id
    FROM (
        SELECT
            id,
            row_number() OVER (
                PARTITION BY autopilot_id, trigger_id
                ORDER BY
                    CASE
                        WHEN status = 'running' AND task_id IS NOT NULL THEN 0
                        WHEN status = 'issue_created' AND issue_id IS NOT NULL THEN 1
                        ELSE 2
                    END,
                    planned_at NULLS LAST,
                    triggered_at,
                    created_at,
                    id
            ) AS rn
        FROM autopilot_run
        WHERE source = 'schedule'
          AND trigger_id IS NOT NULL
          AND status IN ('issue_created', 'running')
    ) ranked
    WHERE rn > 1
)
UPDATE autopilot_run run
SET status = 'skipped',
    completed_at = COALESCE(run.completed_at, now()),
    failure_reason = COALESCE(run.failure_reason, 'skipped_overlap')
FROM duplicate_runs
WHERE run.id = duplicate_runs.id
  AND run.status IN ('issue_created', 'running');

COMMIT;
