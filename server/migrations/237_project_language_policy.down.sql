ALTER TABLE project
    DROP CONSTRAINT IF EXISTS project_language_policy_check,
    DROP COLUMN IF EXISTS language_policy;
