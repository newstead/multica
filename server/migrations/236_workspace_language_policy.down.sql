ALTER TABLE workspace
    DROP CONSTRAINT IF EXISTS workspace_language_policy_check,
    DROP COLUMN IF EXISTS language_policy;
