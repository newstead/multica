ALTER TABLE agent_task_queue
    DROP COLUMN IF EXISTS policy_service_tier,
    DROP COLUMN IF EXISTS policy_thinking_level,
    DROP COLUMN IF EXISTS policy_model;

DROP TABLE IF EXISTS workspace_role_policy;

ALTER TABLE workspace
    DROP COLUMN IF EXISTS role_policy_enabled;
