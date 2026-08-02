ALTER TABLE workspace
    ADD COLUMN IF NOT EXISTS language_policy TEXT;

ALTER TABLE workspace
    DROP CONSTRAINT IF EXISTS workspace_language_policy_check;

ALTER TABLE workspace
    ADD CONSTRAINT workspace_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en')
    );
