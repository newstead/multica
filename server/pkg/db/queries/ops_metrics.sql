-- name: GetOpsMetricsIssueCounts :one
SELECT
  count(*) FILTER (WHERE status = 'blocked')::bigint AS blocked_count,
  count(*) FILTER (WHERE status = 'in_progress')::bigint AS in_progress_count
FROM issue
WHERE workspace_id = @workspace_id;

-- name: ListOpsMetricsRecentBlockers :many
SELECT
  i.id,
  (w.issue_prefix || '-' || i.number::text)::text AS identifier,
  i.title,
  i.priority,
  i.status,
  COALESCE(NULLIF(btrim(i.metadata->>'blocked_reason'), ''), '')::text AS blocked_reason,
  COALESCE(NULLIF(btrim(i.metadata->>'waiting_on'), ''), '')::text AS waiting_on,
  i.updated_at
FROM issue i
JOIN workspace w ON w.id = i.workspace_id
WHERE i.workspace_id = @workspace_id
  AND i.status = 'blocked'
ORDER BY i.updated_at DESC
LIMIT @limit_count::int;

-- name: GetOpsMetricsActiveTaskCounts :one
SELECT
  count(*) FILTER (WHERE atq.status = 'queued')::bigint AS queued_count,
  count(*) FILTER (WHERE atq.status = 'dispatched')::bigint AS dispatched_count,
  count(*) FILTER (WHERE atq.status = 'running')::bigint AS running_count,
  count(*) FILTER (WHERE atq.status = 'waiting_local_directory')::bigint AS waiting_local_directory_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = @workspace_id
  AND atq.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory');

-- name: ListOpsMetricsActiveTasks :many
SELECT
  atq.id,
  atq.status,
  atq.agent_id,
  a.name AS agent_name,
  atq.issue_id,
  CASE
    WHEN i.id IS NULL THEN ''
    ELSE w.issue_prefix || '-' || i.number::text
  END::text AS issue_identifier,
  i.title AS issue_title,
  atq.runtime_id,
  ar.status AS runtime_status,
  atq.created_at,
  atq.dispatched_at,
  atq.started_at,
  atq.wait_reason
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
LEFT JOIN workspace w ON w.id = i.workspace_id
LEFT JOIN agent_runtime ar ON ar.id = atq.runtime_id
WHERE a.workspace_id = @workspace_id
  AND atq.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
ORDER BY
  CASE atq.status
    WHEN 'running' THEN 0
    WHEN 'waiting_local_directory' THEN 1
    WHEN 'dispatched' THEN 2
    ELSE 3
  END,
  COALESCE(atq.started_at, atq.dispatched_at, atq.created_at) DESC
LIMIT @limit_count::int;

-- name: GetOpsMetricsAgentCapacity :one
WITH active_tasks AS (
  SELECT agent_id, count(*)::bigint AS active_count
  FROM agent_task_queue
  WHERE status IN ('dispatched', 'running', 'waiting_local_directory')
  GROUP BY agent_id
)
SELECT
  count(a.id)::bigint AS total_agents,
  count(a.id) FILTER (WHERE COALESCE(active_tasks.active_count, 0) > 0)::bigint AS active_agents,
  count(a.id) FILTER (WHERE COALESCE(active_tasks.active_count, 0) = 0)::bigint AS idle_agents,
  COALESCE(sum(a.max_concurrent_tasks), 0)::bigint AS total_slots,
  COALESCE(sum(LEAST(COALESCE(active_tasks.active_count, 0), a.max_concurrent_tasks::bigint)), 0)::bigint AS active_slots
FROM agent a
LEFT JOIN active_tasks ON active_tasks.agent_id = a.id
WHERE a.workspace_id = @workspace_id
  AND a.archived_at IS NULL
  AND a.kind = 'user';

-- name: GetOpsMetricsRuntimeHealth :one
SELECT
  count(*)::bigint AS total_runtimes,
  count(*) FILTER (WHERE status = 'online')::bigint AS online_runtimes,
  count(*) FILTER (WHERE status <> 'online')::bigint AS offline_runtimes,
  count(*) FILTER (
    WHERE status = 'online'
      AND last_seen_at IS NOT NULL
      AND last_seen_at < now() - make_interval(secs => @stale_seconds::double precision)
  )::bigint AS stale_runtimes,
  max(last_seen_at)::timestamptz AS last_seen_at
FROM agent_runtime
WHERE workspace_id = @workspace_id;
