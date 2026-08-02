-- MultMemory gateway foundation: workspace config, auditable event ledger,
-- provider delivery outbox, and recall provenance samples.
--
-- These tables intentionally carry no foreign keys. Every row stores
-- workspace_id directly, and application queries must scope by workspace_id.
-- Secondary indexes live in single-statement CREATE INDEX CONCURRENTLY
-- migrations that follow this file.

CREATE TABLE IF NOT EXISTS memory_workspace_config (
    workspace_id                    UUID PRIMARY KEY,
    enabled                         BOOLEAN NOT NULL DEFAULT FALSE,
    primary_provider                TEXT NOT NULL DEFAULT '',
    shadow_provider                 TEXT,
    read_mode                       TEXT NOT NULL DEFAULT 'primary'
        CHECK (read_mode IN ('primary', 'shadow', 'dual')),
    provider_settings               JSONB NOT NULL DEFAULT '{}'::jsonb,
    provider_credentials_encrypted  JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memory_event (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID NOT NULL,
    project_id        UUID,
    agent_id          UUID,
    issue_id          UUID,
    task_id           UUID,
    actor_type        TEXT NOT NULL DEFAULT ''
        CHECK (actor_type IN ('', 'member', 'agent', 'system')),
    actor_id          UUID,
    event_type        TEXT NOT NULL
        CHECK (event_type IN ('retain', 'update', 'invalidate', 'delete')),
    idempotency_key   TEXT NOT NULL,
    envelope          JSONB NOT NULL,
    status            TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'delivering', 'delivered', 'failed', 'terminal_failed')),
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    available_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_attempt_at   TIMESTAMPTZ,
    terminal_at       TIMESTAMPTZ,
    error             TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS memory_provider_delivery (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID NOT NULL,
    memory_event_id     UUID NOT NULL,
    provider            TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'delivering', 'delivered', 'retry', 'terminal_failed', 'skipped')),
    attempt_count       INTEGER NOT NULL DEFAULT 0,
    next_attempt_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_attempt_at     TIMESTAMPTZ,
    terminal_at         TIMESTAMPTZ,
    provider_memory_id  TEXT,
    response            JSONB NOT NULL DEFAULT '{}'::jsonb,
    error               TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (memory_event_id, provider)
);

CREATE TABLE IF NOT EXISTS memory_recall_sample (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL,
    project_id    UUID,
    agent_id      UUID,
    issue_id      UUID,
    task_id       UUID,
    provider      TEXT NOT NULL,
    query         TEXT NOT NULL,
    results       JSONB NOT NULL DEFAULT '[]'::jsonb,
    provenance    JSONB NOT NULL DEFAULT '{}'::jsonb,
    sampled_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
