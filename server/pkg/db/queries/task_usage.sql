-- name: UpsertTaskUsage :exec
-- Bumps `updated_at` on INSERT and on conflict so the hourly-rollup worker
-- detects the row as dirty and re-aggregates its bucket.
-- Without the conflict-side bump, a correction to historical token counts
-- would never propagate to the rollup.
-- cost_usd_ticks is the provider's own price for this usage (1e-10 USD), NULL
-- when it reports none. It is overwritten like the token counters so a
-- corrected report replaces the previous figure rather than accumulating.
INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cost_usd_ticks, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, sqlc.narg('cost_usd_ticks'), now())
ON CONFLICT (task_id, provider, model)
DO UPDATE SET
    input_tokens = EXCLUDED.input_tokens,
    output_tokens = EXCLUDED.output_tokens,
    cache_read_tokens = EXCLUDED.cache_read_tokens,
    cache_write_tokens = EXCLUDED.cache_write_tokens,
    reasoning_tokens = EXCLUDED.reasoning_tokens,
    cost_usd_ticks = EXCLUDED.cost_usd_ticks,
    updated_at = now();

-- name: GetTaskUsage :many
SELECT * FROM task_usage
WHERE task_id = $1
ORDER BY model;

-- name: GetIssueUsageSummary :one
SELECT
    COALESCE(SUM(tu.input_tokens), 0)::bigint AS total_input_tokens,
    COALESCE(SUM(tu.output_tokens), 0)::bigint AS total_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens), 0)::bigint AS total_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens), 0)::bigint AS total_cache_write_tokens,
    COALESCE(SUM(tu.reasoning_tokens), 0)::bigint AS total_reasoning_tokens,
    COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS total_cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_write_tokens,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
WHERE atq.issue_id = $1;

-- name: ListDashboardUsageDaily :many
-- Daily per-(date, provider, model) token aggregates for the workspace, served
-- from the UTC-bucketed `task_usage_hourly` table and
-- sliced to calendar days under the caller-supplied @tz. Optionally
-- scoped to a single project via sqlc.narg('project_id'). Powers the
-- workspace dashboard's daily cost chart.
-- The viewer's tz is applied here at query time, so a viewer in
-- Asia/Shanghai gets their "today" cut at +08 and one in
-- America/Los_Angeles gets theirs at -08 against the same UTC rows.
--
-- @since is already the viewer's local start-of-day-(N) as a UTC
-- instant (computed by parseSinceParamInTZ). It must NOT be re-truncated
-- with DATE_TRUNC here — DATE_TRUNC operates in the session tz and would
-- snap the cutoff back to UTC midnight, dragging in an extra partial
-- local day for any non-UTC viewer.
-- provider is LOWER()-normalized so mixed-case historical rows (written
-- before the handler lowercased provider on write) merge with new rows
-- instead of forming a separate case-variant bucket.
SELECT
    DATE(bucket_hour AT TIME ZONE sqlc.arg('tz')::text) AS date,
    LOWER(provider) AS provider,
    model,
    SUM(input_tokens)::bigint        AS input_tokens,
    SUM(output_tokens)::bigint       AS output_tokens,
    SUM(cache_read_tokens)::bigint   AS cache_read_tokens,
    SUM(cache_write_tokens)::bigint  AS cache_write_tokens,
    SUM(reasoning_tokens)::bigint    AS reasoning_tokens,
    SUM(cost_usd_ticks)::bigint                                          AS cost_usd_ticks,
    SUM(COALESCE(uncosted_input_tokens, input_tokens))::bigint           AS uncosted_input_tokens,
    SUM(COALESCE(uncosted_output_tokens, output_tokens))::bigint         AS uncosted_output_tokens,
    SUM(COALESCE(uncosted_cache_read_tokens, cache_read_tokens))::bigint AS uncosted_cache_read_tokens,
    SUM(COALESCE(uncosted_cache_write_tokens, cache_write_tokens))::bigint AS uncosted_cache_write_tokens,
    SUM(task_count)::int             AS task_count
FROM task_usage_hourly
WHERE workspace_id = $1
  AND bucket_hour >= sqlc.arg('since')::timestamptz
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'))
GROUP BY DATE(bucket_hour AT TIME ZONE sqlc.arg('tz')::text), LOWER(provider), model
ORDER BY DATE(bucket_hour AT TIME ZONE sqlc.arg('tz')::text) DESC, LOWER(provider), model;

-- name: ListDashboardUsageByAgent :many
-- Per-(agent, provider, model) token aggregates from `task_usage_hourly`. No
-- date grouping in the result, so this query takes no `@tz` — the
-- @since cutoff is a raw timestamptz the Go layer has already computed
-- in the viewer's tz. Model dimension is preserved so the client can
-- compute cost from its per-model pricing table; the client folds rows
-- by agent for the "by agent" list on the dashboard.
--
-- task_count is summed across hourly buckets — one task that spans
-- multiple hours lands in multiple buckets, so this over-counts by
-- hour the same way the daily version over-counted by day. The
-- frontend prefers `ListDashboardAgentRunTime` for the user-facing
-- "tasks" column, so this stays informational only.
-- provider is LOWER()-normalized so mixed-case historical rows merge with
-- new rows (see ListDashboardUsageDaily).
SELECT
    agent_id,
    LOWER(provider) AS provider,
    model,
    SUM(input_tokens)::bigint        AS input_tokens,
    SUM(output_tokens)::bigint       AS output_tokens,
    SUM(cache_read_tokens)::bigint   AS cache_read_tokens,
    SUM(cache_write_tokens)::bigint  AS cache_write_tokens,
    SUM(reasoning_tokens)::bigint    AS reasoning_tokens,
    SUM(cost_usd_ticks)::bigint                                          AS cost_usd_ticks,
    SUM(COALESCE(uncosted_input_tokens, input_tokens))::bigint           AS uncosted_input_tokens,
    SUM(COALESCE(uncosted_output_tokens, output_tokens))::bigint         AS uncosted_output_tokens,
    SUM(COALESCE(uncosted_cache_read_tokens, cache_read_tokens))::bigint AS uncosted_cache_read_tokens,
    SUM(COALESCE(uncosted_cache_write_tokens, cache_write_tokens))::bigint AS uncosted_cache_write_tokens,
    SUM(task_count)::int             AS task_count
FROM task_usage_hourly
WHERE workspace_id = $1
  AND bucket_hour >= @since::timestamptz
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'))
GROUP BY agent_id, LOWER(provider), model
ORDER BY agent_id, LOWER(provider), model;

-- name: ListDashboardRunTimeDaily :many
-- Daily per-date run time + task counts for the workspace, optionally
-- scoped to a single project. Powers the workspace dashboard's "Time"
-- and "Tasks" metrics on the same toggle as Tokens / Cost. Bucketed by
-- completed_at (terminal time) sliced into calendar days under the
-- caller-supplied @tz — same Viewing-tz treatment as ListDashboardUsageDaily
-- so the Time / Tasks tabs cut their day boundary identically to the
-- Cost / Tokens tabs (a viewer east of UTC would otherwise see the four
-- tabs disagree on a "1d" window). Only terminal tasks (completed or
-- failed) with both started_at and completed_at populated contribute.
--
-- @since is already the viewer's local start-of-day-(N) (parseSinceParamInTZ)
-- — passed straight through, NOT re-truncated; see ListDashboardUsageDaily.
SELECT
    DATE(atq.completed_at AT TIME ZONE sqlc.arg('tz')::text) AS date,
    COALESCE(
        SUM(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))::bigint,
        0
    )::bigint AS total_seconds,
    COUNT(*)::int AS task_count,
    COUNT(*) FILTER (WHERE atq.status = 'failed')::int AS failed_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
WHERE a.workspace_id = $1
  AND atq.status IN ('completed', 'failed')
  AND atq.started_at IS NOT NULL
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at >= sqlc.arg('since')::timestamptz
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
GROUP BY DATE(atq.completed_at AT TIME ZONE sqlc.arg('tz')::text)
ORDER BY DATE(atq.completed_at AT TIME ZONE sqlc.arg('tz')::text) DESC;

-- name: ListDashboardAgentRunTime :many
-- Per-agent total task run time and task count for the workspace, optionally
-- scoped to a single project. Counts only terminal runs (completed or failed)
-- with both started_at and completed_at populated — queued/running tasks have
-- no finite duration. Anchored on completed_at so the window matches the
-- token cost window (which is anchored on tu.created_at, ~= completion time).
--
-- No date bucketing, so no @tz — but @since is the viewer's local
-- start-of-day-(N) so the "last N days" window lines up with the per-agent
-- cost card; passed straight through without re-truncation.
SELECT
    atq.agent_id,
    COALESCE(
        SUM(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))::bigint,
        0
    )::bigint AS total_seconds,
    COUNT(*)::int AS task_count,
    COUNT(*) FILTER (WHERE atq.status = 'failed')::int AS failed_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
WHERE a.workspace_id = $1
  AND atq.status IN ('completed', 'failed')
  AND atq.started_at IS NOT NULL
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at >= @since::timestamptz
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
GROUP BY atq.agent_id
ORDER BY total_seconds DESC;

-- name: ListDashboardAgentSessions :many
-- Per-agent terminal task counts, failure reason breakdown, queue wait
-- percentiles, and run duration percentiles for the workspace dashboard.
-- The window is anchored on completed_at, matching the existing runtime
-- dashboard queries.
WITH terminal_tasks AS (
    SELECT
        atq.agent_id,
        atq.status,
        atq.failure_reason,
        CASE
            WHEN atq.started_at IS NOT NULL
            THEN EXTRACT(EPOCH FROM (atq.started_at - atq.created_at))::double precision
            ELSE NULL
        END AS queue_wait_seconds,
        CASE
            WHEN atq.started_at IS NOT NULL AND atq.completed_at IS NOT NULL
            THEN EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at))::double precision
            ELSE NULL
        END AS run_duration_seconds
    FROM agent_task_queue atq
    JOIN agent a ON a.id = atq.agent_id
    LEFT JOIN issue i ON i.id = atq.issue_id
    WHERE a.workspace_id = $1
      AND atq.status IN ('completed', 'failed')
      AND atq.completed_at IS NOT NULL
      AND atq.completed_at >= @since::timestamptz
      AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
),
agent_rollup AS (
    SELECT
        agent_id,
        COUNT(*)::int AS task_count,
        COUNT(*) FILTER (WHERE status = 'completed')::int AS completed_count,
        COUNT(*) FILTER (WHERE status = 'failed')::int AS failed_count,
        COALESCE(ROUND(
            percentile_cont(0.5) WITHIN GROUP (ORDER BY queue_wait_seconds)
            FILTER (WHERE queue_wait_seconds IS NOT NULL)
        )::bigint, 0)::bigint AS queue_wait_p50_seconds,
        COALESCE(ROUND(
            percentile_cont(0.95) WITHIN GROUP (ORDER BY queue_wait_seconds)
            FILTER (WHERE queue_wait_seconds IS NOT NULL)
        )::bigint, 0)::bigint AS queue_wait_p95_seconds,
        COALESCE(ROUND(
            percentile_cont(0.5) WITHIN GROUP (ORDER BY run_duration_seconds)
            FILTER (WHERE run_duration_seconds IS NOT NULL)
        )::bigint, 0)::bigint AS run_duration_p50_seconds,
        COALESCE(ROUND(
            percentile_cont(0.95) WITHIN GROUP (ORDER BY run_duration_seconds)
            FILTER (WHERE run_duration_seconds IS NOT NULL)
        )::bigint, 0)::bigint AS run_duration_p95_seconds
    FROM terminal_tasks
    GROUP BY agent_id
),
failure_counts AS (
    SELECT
        agent_id,
        COALESCE(NULLIF(failure_reason, ''), 'unknown') AS failure_reason,
        COUNT(*)::int AS count
    FROM terminal_tasks
    WHERE status = 'failed'
    GROUP BY agent_id, COALESCE(NULLIF(failure_reason, ''), 'unknown')
),
ranked_failure_counts AS (
    SELECT
        agent_id,
        failure_reason,
        count,
        row_number() OVER (PARTITION BY agent_id ORDER BY count DESC, failure_reason) AS rn
    FROM failure_counts
),
failure_breakdown AS (
    SELECT
        agent_id,
        jsonb_agg(
            jsonb_build_object('failure_reason', failure_reason, 'count', count)
            ORDER BY count DESC, failure_reason
        ) AS failure_reasons
    FROM ranked_failure_counts
    WHERE rn <= 5
    GROUP BY agent_id
)
SELECT
    ar.agent_id,
    ar.task_count,
    ar.completed_count,
    ar.failed_count,
    COALESCE(fb.failure_reasons, '[]'::jsonb) AS failure_reasons,
    ar.queue_wait_p50_seconds,
    ar.queue_wait_p95_seconds,
    ar.run_duration_p50_seconds,
    ar.run_duration_p95_seconds
FROM agent_rollup ar
LEFT JOIN failure_breakdown fb ON fb.agent_id = ar.agent_id
ORDER BY ar.task_count DESC, ar.agent_id;

-- name: ListDashboardAgentCode :many
-- Per-agent task-level diff stats from agent_task_queue.result->'diff_stats'
-- plus PR-level GitHub stats from non-reference linked pull requests. PRs are
-- attributed once, to the earliest in-window task for the linked issue.
WITH task_diff_stats AS (
    SELECT
        atq.agent_id,
        COALESCE(SUM(COALESCE((atq.result->'diff_stats'->>'additions')::int, 0)), 0)::bigint AS additions,
        COALESCE(SUM(COALESCE((atq.result->'diff_stats'->>'deletions')::int, 0)), 0)::bigint AS deletions,
        COALESCE(SUM(COALESCE(
            (atq.result->'diff_stats'->>'files_changed')::int,
            (atq.result->'diff_stats'->>'changed_files')::int,
            0
        )), 0)::bigint AS files_changed,
        COUNT(*)::int AS task_count
    FROM agent_task_queue atq
    JOIN agent a ON a.id = atq.agent_id
    LEFT JOIN issue i ON i.id = atq.issue_id
    WHERE a.workspace_id = $1
      AND atq.created_at >= @since::timestamptz
      AND atq.result ? 'diff_stats'
      AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
    GROUP BY atq.agent_id
),
ranked_pull_requests AS (
    SELECT
        atq.agent_id,
        gpr.id AS pull_request_id,
        gpr.additions,
        gpr.deletions,
        gpr.changed_files,
        row_number() OVER (
            PARTITION BY gpr.id
            ORDER BY atq.created_at ASC, atq.id ASC
        ) AS rn
    FROM agent_task_queue atq
    JOIN agent a ON a.id = atq.agent_id
    JOIN issue i ON i.id = atq.issue_id
    JOIN issue_pull_request ipr ON ipr.issue_id = i.id
    JOIN github_pull_request gpr ON gpr.id = ipr.pull_request_id
    WHERE a.workspace_id = $1
      AND gpr.workspace_id = $1
      AND atq.created_at >= @since::timestamptz
      AND COALESCE(ipr.reference_only, false) = false
      AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
),
pr_stats AS (
    SELECT
        agent_id,
        COALESCE(SUM(additions), 0)::bigint AS pr_additions,
        COALESCE(SUM(deletions), 0)::bigint AS pr_deletions,
        COALESCE(SUM(changed_files), 0)::bigint AS pr_changed_files,
        COUNT(*)::int AS pull_request_count
    FROM ranked_pull_requests
    WHERE rn = 1
    GROUP BY agent_id
),
agent_ids AS (
    SELECT agent_id FROM task_diff_stats
    UNION
    SELECT agent_id FROM pr_stats
)
SELECT
    ai.agent_id,
    COALESCE(tds.additions, 0)::bigint AS additions,
    COALESCE(tds.deletions, 0)::bigint AS deletions,
    COALESCE(tds.files_changed, 0)::bigint AS files_changed,
    COALESCE(tds.task_count, 0)::int AS task_count,
    COALESCE(ps.pr_additions, 0)::bigint AS pr_additions,
    COALESCE(ps.pr_deletions, 0)::bigint AS pr_deletions,
    COALESCE(ps.pr_changed_files, 0)::bigint AS pr_changed_files,
    COALESCE(ps.pull_request_count, 0)::int AS pull_request_count
FROM agent_ids ai
LEFT JOIN task_diff_stats tds ON tds.agent_id = ai.agent_id
LEFT JOIN pr_stats ps ON ps.agent_id = ai.agent_id
ORDER BY (COALESCE(tds.additions, 0) + COALESCE(tds.deletions, 0) + COALESCE(ps.pr_additions, 0) + COALESCE(ps.pr_deletions, 0)) DESC,
         ai.agent_id;
