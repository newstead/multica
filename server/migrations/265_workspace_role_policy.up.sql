-- Workspace role → AI execution config policy (ROLEPOL-0 / ROL-214).
--
-- Per-workspace matrix binding agent roles (role_code) to concrete execution
-- configs: either a specific AI agent (bind_agent, XOR with exec fields) or
-- exec overrides layered on top of the assigned agent (exec_override). Workspace
-- routing applies it to NEW tasks only; already enqueued/running tasks are never
-- rewritten. role_policy_enabled defaults to FALSE so existing agent-centric
-- behavior is unchanged everywhere until an owner/admin opts in.
--
-- Note: the users table is quoted "user" (the contract draft referenced
-- app_user, which does not exist in this schema).

ALTER TABLE workspace
    ADD COLUMN role_policy_enabled BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE workspace_role_policy (
    workspace_id  uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    role_code     text NOT NULL,
    -- Variant A: hard binding to a concrete AI agent (XOR with exec config).
    agent_id      uuid NULL REFERENCES agent(id) ON DELETE SET NULL,
    -- Variant B: exec config layered over the assigned agent (all fields optional).
    runtime_id    uuid NULL REFERENCES agent_runtime(id) ON DELETE SET NULL,
    model         text NULL,
    thinking_level text NULL,
    service_tier  text NULL,
    fallback      text NOT NULL DEFAULT 'agent_default', -- 'agent_default' | 'disabled'
    updated_by    uuid NULL REFERENCES "user"(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, role_code),
    CONSTRAINT workspace_role_policy_agent_xor_exec CHECK (
        agent_id IS NULL OR (runtime_id IS NULL AND model IS NULL AND thinking_level IS NULL AND service_tier IS NULL)
    ),
    CONSTRAINT workspace_role_policy_fallback_check CHECK (fallback IN ('agent_default','disabled')),
    CONSTRAINT workspace_role_policy_role_code_check CHECK (
        role_code IN ('TL','BE','FE','FS','QA','OPS','ML','DA','SRE','SEC')
    )
);

CREATE INDEX workspace_role_policy_workspace_idx ON workspace_role_policy(workspace_id);

-- Exec-overrides stamped on the task row at enqueue; agent-binding and
-- runtime-binding reuse the existing agent_id / runtime_id task columns.
ALTER TABLE agent_task_queue
    ADD COLUMN policy_model text NULL,
    ADD COLUMN policy_thinking_level text NULL,
    ADD COLUMN policy_service_tier text NULL;
