-- =====================
-- Memory Gateway
-- =====================

-- name: GetMemoryWorkspaceConfig :one
SELECT * FROM memory_workspace_config
WHERE workspace_id = $1;

-- name: UpsertMemoryWorkspaceConfig :one
INSERT INTO memory_workspace_config (
    workspace_id, enabled, primary_provider, shadow_provider, read_mode,
    provider_settings, provider_credentials_encrypted
) VALUES (
    $1, $2, $3, sqlc.narg('shadow_provider'), $4, $5, $6
)
ON CONFLICT (workspace_id) DO UPDATE SET
    enabled                        = EXCLUDED.enabled,
    primary_provider               = EXCLUDED.primary_provider,
    shadow_provider                = EXCLUDED.shadow_provider,
    read_mode                      = EXCLUDED.read_mode,
    provider_settings              = EXCLUDED.provider_settings,
    provider_credentials_encrypted = EXCLUDED.provider_credentials_encrypted,
    updated_at                     = now()
RETURNING *;

-- name: UpsertMemoryEvent :one
INSERT INTO memory_event (
    workspace_id, project_id, agent_id, issue_id, task_id, actor_type, actor_id,
    event_type, idempotency_key, envelope, status, available_at
) VALUES (
    $1, sqlc.narg('project_id'), sqlc.narg('agent_id'), sqlc.narg('issue_id'),
    sqlc.narg('task_id'), $2, sqlc.narg('actor_id'), $3, $4, $5, 'queued', $6
)
ON CONFLICT (workspace_id, idempotency_key) DO UPDATE SET
    updated_at = memory_event.updated_at
RETURNING *, (xmax = 0) AS inserted;

-- name: GetMemoryEventInWorkspace :one
SELECT * FROM memory_event
WHERE id = $1 AND workspace_id = $2;

-- name: ListMemoryEventsByWorkspace :many
SELECT * FROM memory_event
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpsertMemoryProviderDelivery :one
INSERT INTO memory_provider_delivery (
    workspace_id, memory_event_id, provider, status, next_attempt_at
) VALUES (
    $1, $2, $3, 'queued', $4
)
ON CONFLICT (memory_event_id, provider) DO UPDATE SET
    updated_at = memory_provider_delivery.updated_at
RETURNING *, (xmax = 0) AS inserted;

-- name: UpdateMemoryProviderDeliveryResult :one
UPDATE memory_provider_delivery
SET status = $3,
    attempt_count = $4,
    next_attempt_at = $5,
    last_attempt_at = now(),
    terminal_at = sqlc.narg('terminal_at'),
    provider_memory_id = sqlc.narg('provider_memory_id'),
    response = $6,
    error = sqlc.narg('error'),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: CreateMemoryRecallSample :one
INSERT INTO memory_recall_sample (
    workspace_id, project_id, agent_id, issue_id, task_id, provider,
    query, results, provenance, sampled_at
) VALUES (
    $1, sqlc.narg('project_id'), sqlc.narg('agent_id'), sqlc.narg('issue_id'),
    sqlc.narg('task_id'), $2, $3, $4, $5, $6
)
RETURNING *;

-- name: ListMemoryRecallSamplesByWorkspace :many
SELECT * FROM memory_recall_sample
WHERE workspace_id = $1
ORDER BY sampled_at DESC
LIMIT $2 OFFSET $3;
