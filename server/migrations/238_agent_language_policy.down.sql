ALTER TABLE agent
    DROP CONSTRAINT IF EXISTS agent_language_policy_check,
    DROP COLUMN IF EXISTS language_policy;
